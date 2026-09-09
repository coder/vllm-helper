package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/nodes"
)

func TestShardRayEnvPrefixesUseNodeLocalHostIP(t *testing.T) {
	head := shardEnvPrefix("enp1s0f1np1", "10.44.0.1")
	for _, want := range []string{
		"VLLM_HOST_IP='10.44.0.1'",
		"NCCL_SOCKET_IFNAME='enp1s0f1np1'",
		"GLOO_SOCKET_IFNAME='enp1s0f1np1'",
	} {
		if !strings.Contains(head, want) {
			t.Fatalf("head env prefix missing %q: %s", want, head)
		}
	}

	worker := shardWorkerEnvPrefix("enp1s0f1np1")
	for _, want := range []string{
		"VLLM_HOST_IP=$node_ip",
		"NCCL_SOCKET_IFNAME='enp1s0f1np1'",
		"GLOO_SOCKET_IFNAME='enp1s0f1np1'",
	} {
		if !strings.Contains(worker, want) {
			t.Fatalf("worker env prefix missing %q: %s", want, worker)
		}
	}
	if strings.Contains(worker, "'$node_ip'") {
		t.Fatalf("worker node IP must be expanded by the remote shell: %s", worker)
	}

	if got := shardEnvPrefix("", ""); got != "env" {
		t.Fatalf("empty env prefix = %q, want env", got)
	}
}

func TestShardServeEnvUsesHeadIP(t *testing.T) {
	env := shardEnv([]string{"BASE=x"}, "enp1s0f1np1", "10.44.0.1")
	for _, want := range []string{
		"BASE=x",
		"RAY_ADDRESS=auto",
		"VLLM_HOST_IP=10.44.0.1",
		"NCCL_SOCKET_IFNAME=enp1s0f1np1",
		"GLOO_SOCKET_IFNAME=enp1s0f1np1",
	} {
		if !hasString(env, want) {
			t.Fatalf("env missing %q: %v", want, env)
		}
	}
}

func TestRunOnNodeDryRunUsesScriptStdin(t *testing.T) {
	out, err := captureStdout(t, func() error {
		return runOnNode(nodes.Node{Name: "worker", SSH: "coder@example"}, "head", "/tmp/key with space", "echo \"$HOME\"; true", true)
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"ssh '-o' 'BatchMode=yes' '-o' 'StrictHostKeyChecking=accept-new' '-i' '/tmp/key with space' 'coder@example' bash -s <<'SCRIPT'",
		"echo \"$HOME\"; true",
		"SCRIPT",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("dry-run output missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "bash -lc") {
		t.Fatalf("remote commands should be piped to bash -s, not embedded in bash -lc: %s", out)
	}
}

func hasString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { os.Stdout = old }()
	os.Stdout = w

	callErr := fn()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	os.Stdout = old
	b, readErr := io.ReadAll(r)
	if closeErr := r.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if readErr != nil {
		t.Fatal(readErr)
	}
	return string(b), callErr
}
