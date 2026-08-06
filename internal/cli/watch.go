package cli

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/coder/vllm-helper/internal/config"
)

// watchOpts controls how a restart is observed.
type watchOpts struct {
	follow  bool          // stream the boot log while waiting
	wait    bool          // block until healthy even without following
	timeout time.Duration // 0 = wait forever (first downloads are slow)
}

// probeHealth checks vLLM's /health route. Returns the HTTP status code
// (0 if nothing answered) and whether the endpoint is serving.
func probeHealth(port string) (int, bool) {
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(fmt.Sprintf("http://localhost:%s/health", port))
	if err != nil {
		return 0, false
	}
	resp.Body.Close()
	return resp.StatusCode, resp.StatusCode == http.StatusOK
}

// watchBoot waits for the restarted server to come up: optionally streams
// the journal, polls /health until it answers, and detects failed units
// and crash loops instead of letting the user stare at a stalled log.
// Returns nil once the endpoint is healthy.
func watchBoot(cfg *config.Config, label string, opts watchOpts) error {
	svc := service(cfg)
	start := time.Now()

	var tail interface{ Kill() error }
	if opts.follow {
		fmt.Println("Watching boot log -- Ctrl+C to stop watching (the service keeps running):")
		cmd, err := svc.StartTail(10)
		if err != nil {
			fmt.Fprintln(os.Stderr, "couldn't follow the journal (continuing to wait):", err)
		} else {
			tail = cmd.Process
			defer func() {
				_ = cmd.Process.Kill()
				_, _ = cmd.Process.Wait()
			}()
		}
	}

	baseline := svc.NRestarts()
	fail := func(reason string) error {
		if tail != nil {
			_ = tail.Kill()
		}
		if !opts.follow {
			if logs := svc.LastLogs(30); logs != "" {
				fmt.Fprintln(os.Stderr, logs)
			}
		}
		userFlag := ""
		if cfg.ServiceScope() == "user" {
			userFlag = "--user "
		}
		return fmt.Errorf("%s -- full log: journalctl %s-u %s -n 100", reason, userFlag, cfg.ServiceName())
	}

	for {
		if _, healthy := probeHealth(cfg.Port()); healthy {
			elapsed := time.Since(start).Round(time.Second)
			fmt.Printf("\n%s serving on port %s (took %s) -- '%s' is live.\n",
				checkmark(), cfg.Port(), elapsed, label)
			return nil
		}
		switch svc.IsActive() {
		case "failed":
			return fail("the service entered a failed state")
		case "inactive", "deactivating":
			// The unit is started before we get here, so a stopped unit
			// means the server exited on its own (or something else
			// stopped it). Waiting for a health check it will never answer
			// would hang forever with the default timeout.
			return fail("the service stopped instead of coming up")
		}
		if n := svc.NRestarts(); baseline >= 0 && n > baseline+1 {
			return fail(fmt.Sprintf("the service is crash-looping (%d restarts since the switch)", n-baseline))
		}
		if opts.timeout > 0 && time.Since(start) > opts.timeout {
			return fail(fmt.Sprintf("gave up after %s (the model may still be downloading)", opts.timeout))
		}
		time.Sleep(2 * time.Second)
	}
}

// checkmark degrades on dumb terminals.
func checkmark() string {
	if os.Getenv("TERM") == "dumb" {
		return "OK:"
	}
	return "\u2713"
}
