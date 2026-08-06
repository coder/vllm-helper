package detect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// sleeperEnv marks a re-exec of this test binary that stands in for a vLLM
// server: it ignores its arguments and stays alive until the test kills it.
const sleeperEnv = "VLLM_HELPER_TEST_SLEEPER"

func TestMain(m *testing.M) {
	if os.Getenv(sleeperEnv) != "" {
		time.Sleep(2 * time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startFakeServer launches a process whose argv looks exactly like a tuned
// vLLM server's, so the scanner has something real to find on both /proc
// (linux) and ps + KERN_PROCARGS2 (darwin).
func startFakeServer(t *testing.T, port string) string {
	t.Helper()

	self, err := os.Executable()
	if err != nil {
		t.Skipf("can't locate the test binary to re-exec: %v", err)
	}
	// argv[0] has to end in /vllm: that is what the scanner matches on and
	// what it resolves VLLMBin from.
	bin := filepath.Join(t.TempDir(), "vllm")
	if err := os.Symlink(self, bin); err != nil {
		t.Skipf("can't stage a vllm-named executable: %v", err)
	}

	cmd := exec.Command(bin, "serve", "tester/fake-model",
		"--port", port,
		"--max-model-len", "4096",
		"--gpu-memory-utilization", "0.92",
		"--tensor-parallel-size", "2",
		"--api-key", "sk-not-a-real-key",
	)
	cmd.Env = append(os.Environ(), sleeperEnv+"=1")
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the stand-in server: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_, _ = cmd.Process.Wait()
	})
	return bin
}

func TestScanProcessesFindsARealServer(t *testing.T) {
	const port = "18123"
	bin := startFakeServer(t, port)

	var found *Server
	deadline := time.Now().Add(20 * time.Second)
	for found == nil && time.Now().Before(deadline) {
		for _, s := range ScanProcesses() {
			if s.Port == port {
				found = &s
				break
			}
		}
		if found == nil {
			time.Sleep(100 * time.Millisecond)
		}
	}
	if found == nil {
		t.Fatalf("ScanProcesses never saw the server listed on port %s", port)
	}

	tests := []struct {
		name string
		got  string
		want string
	}{
		{"model", found.Model, "tester/fake-model"},
		{"max model len", found.MaxModelLen, "4096"},
		{"mem utilization", found.MemUtilization, "0.92"},
		{"extra args", found.ExtraArgs, "--tensor-parallel-size 2"},
		{"vllm bin", found.VLLMBin, bin},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if !found.APIKeySet {
		t.Error("APIKeySet should be true for a server started with --api-key")
	}
	if strings.Contains(found.ExtraArgs, "sk-not-a-real-key") {
		t.Errorf("the api key reached ExtraArgs: %q", found.ExtraArgs)
	}
	if found.PID <= 0 {
		t.Errorf("PID = %d, want the real process id", found.PID)
	}
}

// Worker subprocesses share the parent's command line, so a scan has to
// report one server per port rather than one per process.
func TestScanProcessesDedupesByPort(t *testing.T) {
	const port = "18124"
	startFakeServer(t, port)
	startFakeServer(t, port)

	deadline := time.Now().Add(20 * time.Second)
	for {
		var onPort []Server
		for _, s := range ScanProcesses() {
			if s.Port == port {
				onPort = append(onPort, s)
			}
		}
		switch {
		case len(onPort) == 1:
			return
		case len(onPort) > 1:
			t.Fatalf("got %d servers on port %s, want them deduped to 1", len(onPort), port)
		case time.Now().After(deadline):
			t.Fatalf("ScanProcesses never saw a server on port %s", port)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
