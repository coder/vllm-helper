package cli

import (
	"fmt"
	"os"
	"syscall"

	"github.com/coder/vllm-helper/internal/config"
)

// cmdStart is what systemd runs: source the generated active.env and exec
// vllm serve, replacing this process so signals flow straight to vLLM.
func cmdStart() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile())
	if err != nil {
		return fmt.Errorf("no active model config at %s -- run 'vllm-helper switch <profile>' first", cfg.Dirs.ActiveEnvFile())
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
		"--gpu-memory-utilization", active.Get("GPU_MEMORY_UTILIZATION"),
		"--api-key", active.Get("VLLM_API_KEY"),
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
