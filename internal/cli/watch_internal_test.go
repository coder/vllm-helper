package cli

import (
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/nodes"
	"github.com/coder/vllm-helper/internal/service"
)

// The boot wait has to ask the machine that serves. A cluster's head can be
// another node on the recipe's own port; probing localhost on the configured
// port never answered there, so switch and restart waited forever on a model
// that was already up.
func TestBootTargetFollowsTheServer(t *testing.T) {
	r := testRecipe(t)
	useRegistry(t,
		nodes.Node{Name: "here", Host: "http://127.0.0.1:8001"},
		nodes.Node{Name: "there", Host: "http://10.99.0.7:8001", SSH: "serve@10.99.0.7"})
	cfg := &config.Config{File: &config.EnvFile{}}

	cluster := &clusterRunner{recipe: r, opts: recipeLaunchOptions{nodeNames: []string{"there", "here"}}}
	got, err := bootTargetFor(cfg, cluster)
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://10.99.0.7:" + strconv.Itoa(r.Port); got.base != want {
		t.Errorf("cluster probe = %s, want %s", got.base, want)
	}

	got, err = bootTargetFor(cfg, systemdService(cfg))
	if err != nil {
		t.Fatal(err)
	}
	if want := "http://localhost:" + cfg.Port(); got.base != want {
		t.Errorf("native probe = %s, want %s", got.base, want)
	}
}

type loadingRunner struct{}

func (loadingRunner) IsActive() string                 { return "active" }
func (loadingRunner) NRestarts() int                   { return 0 }
func (loadingRunner) Health() service.Health           { return service.Health{State: "active"} }
func (loadingRunner) Restart() error                   { return nil }
func (loadingRunner) Stop() error                      { return nil }
func (loadingRunner) StartTail(int) (*exec.Cmd, error) { return nil, nil }
func (loadingRunner) PrintLogs(int) error              { return nil }
func (loadingRunner) LastLogs(int) string              { return "" }
func (loadingRunner) LogHint() string                  { return "" }
func (loadingRunner) Label() string                    { return "test" }

func TestWatchTargetReturnsOnceTheTargetIsHealthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" {
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	done := make(chan error, 1)
	go func() {
		done <- watchTarget(bootTarget{runner: loadingRunner{}, base: srv.URL, where: srv.URL}, "x", watchOpts{wait: true, timeout: time.Minute})
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("watch did not see the healthy target")
	}
}
