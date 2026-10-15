package recipe

import (
	"strings"
	"testing"
)

func deepseek(t *testing.T) TestedRecipe {
	t.Helper()
	tt, err := LoadTested()
	if err != nil {
		t.Fatalf("LoadTested: %v", err)
	}
	r, ok := tt.Get("deepseek-v4-flash-2spark")
	if !ok {
		t.Fatal("deepseek-v4-flash-2spark missing")
	}
	return r
}

// An unresolved placeholder must fail rather than reach vLLM as an empty
// string, which is how a run silently falls back to the wrong interface.
func TestNodeEnvRefusesUndiscoveredPlaceholders(t *testing.T) {
	r := deepseek(t)
	if _, err := r.NodeEnv(map[string]string{}); err == nil {
		t.Fatal("NodeEnv should fail when nothing was discovered")
	}
	full := map[string]string{
		PlaceholderIface:  "eth9",
		PlaceholderIBIf:   "rocea,roceb",
		PlaceholderNodeIP: "10.0.0.7",
	}
	env, err := r.NodeEnv(full)
	if err != nil {
		t.Fatalf("NodeEnv: %v", err)
	}
	if env["NCCL_SOCKET_IFNAME"] != "eth9" || env["NCCL_IB_HCA"] != "rocea,roceb" || env["VLLM_HOST_IP"] != "10.0.0.7" {
		t.Errorf("placeholders not substituted: %v", env)
	}
	if env["CUTE_DSL_ARCH"] != "sm_121a" {
		t.Errorf("literal value was rewritten: %q", env["CUTE_DSL_ARCH"])
	}
	for _, name := range r.Placeholders() {
		if full[name] == "" {
			t.Errorf("Placeholders() reported %q, which this test does not supply", name)
		}
	}
}

// Only rank 0 serves the API; a second non-headless rank would race for the port.
func TestDistArgsHeadlessForWorkersOnly(t *testing.T) {
	r := deepseek(t)
	head := strings.Join(r.DistArgs(0, "10.0.0.1", 29501), " ")
	if strings.Contains(head, "--headless") {
		t.Errorf("rank 0 must serve the API: %s", head)
	}
	if !strings.Contains(head, "--node-rank 0") || !strings.Contains(head, "--master-addr 10.0.0.1") {
		t.Errorf("head args wrong: %s", head)
	}
	worker := strings.Join(r.DistArgs(1, "10.0.0.1", 29501), " ")
	if !strings.Contains(worker, "--headless") || !strings.Contains(worker, "--node-rank 1") {
		t.Errorf("worker args wrong: %s", worker)
	}
}

func TestRunArgsRequiresADigest(t *testing.T) {
	r := deepseek(t)
	s := ContainerSpec{Name: "n", Image: "eugr/spark-vllm-b12x:latest", HomeDir: "/home/u"}
	if _, err := s.RunArgs(); err == nil {
		t.Fatal("a moving tag must be refused; it is how a verified backend name becomes invalid")
	}
	s.Image = r.Image
	args, err := s.RunArgs()
	if err != nil {
		t.Fatalf("RunArgs: %v", err)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--network host", "--ipc host", "--privileged", "--gpus all", "--entrypoint vllm"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing %q in %s", want, joined)
		}
	}
	if !strings.HasSuffix(joined, r.Image) {
		t.Errorf("image must come last so serve args are the container command: %s", joined)
	}
	// A node that dies is diagnosed from its logs, and --rm deletes them.
	if strings.Contains(joined, "--rm") {
		t.Errorf("--rm discards the logs needed to explain a dead node: %s", joined)
	}
}

// The compilation-config value is JSON; losing its quoting corrupts the launch.
func TestShellCommandKeepsJSONIntact(t *testing.T) {
	r := deepseek(t)
	env, err := r.NodeEnv(map[string]string{
		PlaceholderIface: "eth9", PlaceholderIBIf: "a,b", PlaceholderNodeIP: "10.0.0.7",
	})
	if err != nil {
		t.Fatalf("NodeEnv: %v", err)
	}
	s := ContainerSpec{
		Name: "n", Image: r.Image, HomeDir: "/home/u", Env: env,
		Args: append([]string{"serve", r.Model}, r.ServeArgs()...),
	}
	line, err := s.ShellCommand()
	if err != nil {
		t.Fatalf("ShellCommand: %v", err)
	}
	if !strings.Contains(line, `'{"cudagraph_mode":"FULL_AND_PIECEWISE","custom_ops":["all"]}'`) {
		t.Errorf("compilation-config lost its quoting: %s", line)
	}
	if !strings.Contains(line, "--host 0.0.0.0") || !strings.Contains(line, "--port 8001") {
		t.Errorf("host/port not supplied by the launcher: %s", line)
	}
}

func TestShellQuote(t *testing.T) {
	cases := map[string]string{
		"plain":       "plain",
		"a=b":         "a=b",
		"rocea,roceb": "rocea,roceb",
		"":            "''",
		`{"a":1}`:     `'{"a":1}'`,
		"has space":   "'has space'",
		"it's":        `'it'"'"'s'`,
	}
	for in, want := range cases {
		if got := ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}
