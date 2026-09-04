package cli

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/detect"
)

func newSetupCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "setup",
		Short:   "First-time setup: detect hardware and existing vLLM, write config",
		GroupID: groupMachine,
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return runSetup() },
	}
}

// runSetup is first-time setup for binary installs: detect hardware and any
// existing vLLM, write config + secrets, install the service unit, and
// offer to adopt a running server. Idempotent; never overwrites existing
// config and never restarts anything.
func runSetup() error {
	dirs := config.ResolveDirs()
	fmt.Println("== vllm-helper setup ==")
	fmt.Println()

	hw := detect.Probe()
	fmt.Printf("Hardware:  %d GB memory", hw.TotalMemGB)
	if hw.GPUName != "" {
		kind := "discrete VRAM"
		if hw.Unified {
			kind = "unified memory"
		}
		fmt.Printf(", %s (%s)", hw.GPUName, kind)
	}
	fmt.Printf("\nBudget:    ~%d GB of model weights (%s)\n\n", hw.BudgetGB, hw.BudgetReason)

	for _, d := range []string{dirs.Config, dirs.Profiles, dirs.State, filepath.Join(dirs.Data, "tiktoken-encodings")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	if err := os.Chmod(dirs.Config, 0o700); err != nil {
		return err
	}

	if _, err := os.Stat(dirs.ConfigFile()); err == nil {
		fmt.Println("Config already exists at", dirs.ConfigFile(), "-- keeping it.")
	} else {
		vllmBin := ""
		if p, err := exec.LookPath("vllm"); err == nil {
			if resolved, err := filepath.EvalSymlinks(p); err == nil {
				vllmBin = resolved
			} else {
				vllmBin = p
			}
		}
		content := fmt.Sprintf(`# vllm-helper global config. Sourced (exported) into the server's
# environment on every switch -- values here apply to every profile.

# Port the OpenAI-compatible endpoint listens on.
PORT=8001

# Full path to the vllm executable (usually inside a venv). REQUIRED.
VLLM_BIN=%s

# How the %s unit is managed: "system" (needs sudo) or "user".
SERVICE_SCOPE=%s
SERVICE_NAME=%s

# Budget for model weights, used by 'vllm-helper search' to flag models that
# won't fit. Measured from this machine on every run -- currently ~%d GB, being
# %s. Uncomment only to override that.
#MODEL_BUDGET_GB=%d

# Where gpt-oss-style models find their tiktoken vocab files.
TIKTOKEN_ENCODINGS_BASE=%s

# How many full-length requests each profile's KV cache is sized to hold.
# Defaults to %d. A property of the workload rather than the machine: lower it
# on a single-user box, raise it for a shared endpoint.
#KV_CONTEXT_TARGET=%d

# --- Optional: Coder Agents sync -------------------------------------------
# If all three are set (token in secrets.env), every switch keeps a Coder
# deployment's AI-model picker in sync with the active profile.
#CODER_URL=https://coder.example.com
#CODER_AI_PROVIDER_ID=
# Only needed if the deployment has more than one organization.
#CODER_ORGANIZATION=default
`, vllmBin, config.ServiceManager, config.DefaultServiceScope, config.DefaultServiceName,
			hw.BudgetGB, hw.BudgetReason, hw.BudgetGB, filepath.Join(dirs.Data, "tiktoken-encodings"),
			detect.DefaultContextTarget, detect.DefaultContextTarget)
		if err := os.WriteFile(dirs.ConfigFile(), []byte(content), 0o644); err != nil {
			return err
		}
		fmt.Println("Wrote", dirs.ConfigFile())
		if vllmBin == "" {
			fmt.Println()
			fmt.Println("WARNING: no 'vllm' executable found on PATH. Install vLLM (e.g. in a")
			fmt.Println("venv: 'uv venv ~/venvs/vllm && uv pip install vllm'), then set VLLM_BIN")
			fmt.Println("in config.env to its full path.")
			fmt.Println()
		} else {
			fmt.Println("Detected vllm at", vllmBin)
		}
	}

	if _, err := os.Stat(dirs.SecretsFile()); err == nil {
		fmt.Println("Secrets already exist at", dirs.SecretsFile(), "-- keeping them.")
	} else {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		key := "sk-" + base64.RawURLEncoding.EncodeToString(buf)
		content := fmt.Sprintf(`# Secrets for vllm-helper. Never commit this file anywhere.

# API key clients must send to the vLLM endpoint (generated at setup).
VLLM_API_KEY=%s

# Only needed if Coder sync is configured in config.env:
#CODER_API_TOKEN=
`, key)
		if err := os.WriteFile(dirs.SecretsFile(), []byte(content), 0o600); err != nil {
			return err
		}
		fmt.Println("Generated a new VLLM_API_KEY into", dirs.SecretsFile())
	}

	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	// Offer adoption before installing a unit -- if a server already runs
	// under its own unit, adoption records it and we skip installing ours.
	fmt.Println()
	if err := adoptInto(cfg); err != nil && !errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(os.Stderr, "adoption scan failed (continuing):", err)
	}

	// Reload config -- adoption may have changed SERVICE_NAME/SCOPE.
	cfg, err = loadConfig()
	if err != nil {
		return err
	}
	if svc(cfg).Installed() {
		fmt.Println()
		fmt.Printf("Service unit %s already installed -- setup complete.\n", cfg.ServiceName())
		return nil
	}

	fmt.Println()
	if !confirm(fmt.Sprintf("Install the %s-scope %s unit (%s)?", cfg.ServiceScope(), config.ServiceManager, cfg.ServiceName()), true) {
		fmt.Println("Skipped unit install. Re-run 'vllm-helper setup' when ready.")
		return nil
	}
	return installUnit(cfg)
}

// installUnit installs the host's service unit pointing at this binary's
// `start` command.
func installUnit(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)

	path, err := svc(cfg).Install(self)
	if err != nil {
		return err
	}
	fmt.Printf("Installed %s unit at %s\n", cfg.ServiceScope(), path)
	fmt.Println()
	fmt.Println("Setup complete. Next: 'vllm-helper new <org/model>' or 'vllm-helper search <query>'.")
	return nil
}
