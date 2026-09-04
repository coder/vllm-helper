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

	bin := active.Get("VLLM_BIN")
	if bin == "" {
		return fmt.Errorf("VLLM_BIN is not set in config.env")
	}
	if info, err := os.Stat(bin); err != nil || info.Mode()&0o111 == 0 {
		return fmt.Errorf("VLLM_BIN (%s) does not exist or is not executable", bin)
	}

	args := []string{bin, "serve", active.Get("MODEL"),
		"--port", active.Get("PORT"),
		"--max-model-len", active.Get("MAX_MODEL_LEN"),
		"--gpu-memory-utilization", fitMemUtilization(active.Get("GPU_MEMORY_UTILIZATION"), active.Get("KV_CACHE_MEMORY_BYTES") != ""),
		"--api-key", active.Get("VLLM_API_KEY"),
	}
	// A deliberate cache size makes --gpu-memory-utilization what it should
	// always have been -- a ceiling on what vLLM may touch -- rather than the
	// knob that decides the cache. Note the flag is --kv-cache-memory-bytes:
	// vLLM's own log prints "--kv-cache-memory=N", which only works because
	// argparse accepts unambiguous prefixes, and is not a real option name.
	if kv := active.Get("KV_CACHE_MEMORY_BYTES"); kv != "" {
		args = append(args, "--kv-cache-memory-bytes", kv)
	}
	if extra := active.Get("EXTRA_ARGS"); extra != "" {
		words, err := config.SplitArgs(extra)
		if err != nil {
			return fmt.Errorf("EXTRA_ARGS: %w", err)
		}
		args = append(args, words...)
	}

	// Give vLLM the merged env (TIKTOKEN_ENCODINGS_BASE, HF_HOME, ...) on
	// top of the process environment, mirroring bash's `set -a; source`.
	env := os.Environ()
	for _, k := range active.Keys() {
		env = append(env, k+"="+active.Get(k))
	}
	return syscall.Exec(bin, args, env)
}
