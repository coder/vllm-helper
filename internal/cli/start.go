package cli

import (
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
)

func newStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Run the active model in the foreground",
		// The service manager's ExecStart, not something to type: it execs
		// vLLM over this process and never returns.
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE:   func(*cobra.Command, []string) error { return runStart() },
	}
}

// runStart is what the service manager runs: source the generated active.env
// and exec vllm serve, replacing this process so signals flow straight to vLLM.
func runStart() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile())
	switch {
	case errors.Is(err, os.ErrNotExist):
		// Exit cleanly instead of failing. The unit gets installed before the
		// first switch, and a non-zero exit here would send systemd's
		// Restart=on-failure -- or launchd's KeepAlive -- into a crash loop
		// with no model to serve. Only a missing file means "not set up yet";
		// anything else is a fault worth reporting.
		fmt.Println("No active model yet -- run 'vllm-helper switch <profile>' to load one.")
		return nil
	case err != nil:
		return fmt.Errorf("reading the active model config at %s: %w", cfg.Dirs.ActiveEnvFile(), err)
	}

	spec := serveSpecFromActive(active)
	args, err := spec.args()
	if err != nil {
		return err
	}
	return syscall.Exec(spec.Bin, args, serveEnvFromActive(active))
}
