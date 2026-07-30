package cli

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/detect"
)

// cmdSetup is first-time setup for binary installs: detect hardware and any
// existing vLLM, write config + secrets, install the systemd unit, and
// offer to adopt a running server. Idempotent; never overwrites existing
// config and never restarts anything.
func cmdSetup() error {
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

# How the systemd unit is managed: "system" (sudo systemctl) or "user"
# (systemctl --user).
SERVICE_SCOPE=system
SERVICE_NAME=vllm.service

# Rough budget for model weights on disk, used by 'vllm-helper search' to
# flag models that won't fit. Auto-detected: %s.
MODEL_BUDGET_GB=%d

# Where gpt-oss-style models find their tiktoken vocab files.
TIKTOKEN_ENCODINGS_BASE=%s

# --- Optional: Coder Agents sync -------------------------------------------
# If all three are set (token in secrets.env), every switch keeps a Coder
# deployment's AI-model picker in sync with the active profile.
#CODER_URL=https://coder.example.com
#CODER_AI_PROVIDER_ID=
`, vllmBin, hw.BudgetReason, hw.BudgetGB, filepath.Join(dirs.Data, "tiktoken-encodings"))
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
	if unitInstalled(cfg) {
		fmt.Println()
		fmt.Printf("Service unit %s already installed -- setup complete.\n", cfg.ServiceName())
		return nil
	}

	fmt.Println()
	if !confirm(fmt.Sprintf("Install the %s-scope systemd unit (%s)?", cfg.ServiceScope(), cfg.ServiceName()), true) {
		fmt.Println("Skipped unit install. Re-run 'vllm-helper setup' when ready.")
		return nil
	}
	return installUnit(cfg)
}

func unitInstalled(cfg *config.Config) bool {
	if cfg.ServiceScope() == "user" {
		home, _ := os.UserHomeDir()
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		_, err := os.Stat(filepath.Join(base, "systemd", "user", cfg.ServiceName()))
		return err == nil
	}
	_, err := os.Stat(filepath.Join("/etc/systemd/system", cfg.ServiceName()))
	return err == nil
}

// installUnit renders and installs the systemd unit pointing at this
// binary's `start` command.
func installUnit(cfg *config.Config) error {
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)

	userLine, wantedBy := "User="+os.Getenv("USER"), "multi-user.target"
	if cfg.ServiceScope() == "user" {
		userLine, wantedBy = "", "default.target"
	}
	unit := fmt.Sprintf(`[Unit]
Description=vLLM OpenAI-compatible server (active model set via vllm-helper)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
%s
ExecStart=%s start
Restart=on-failure
RestartSec=5
TimeoutStartSec=600

[Install]
WantedBy=%s
`, userLine, self, wantedBy)

	if cfg.ServiceScope() == "user" {
		home, _ := os.UserHomeDir()
		base := os.Getenv("XDG_CONFIG_HOME")
		if base == "" {
			base = filepath.Join(home, ".config")
		}
		path := filepath.Join(base, "systemd", "user", cfg.ServiceName())
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
			return err
		}
		run(nil, "systemctl", "--user", "daemon-reload")
		run(nil, "systemctl", "--user", "enable", cfg.ServiceName())
		if err := exec.Command("loginctl", "enable-linger", os.Getenv("USER")).Run(); err != nil {
			fmt.Printf("note: couldn't enable lingering -- the server stops at logout (fix: sudo loginctl enable-linger %s)\n", os.Getenv("USER"))
		}
		fmt.Println("Installed user unit at", path)
	} else {
		tmp, err := os.CreateTemp("", "vllm-unit-*.service")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.WriteString(unit); err != nil {
			return err
		}
		tmp.Close()
		path := filepath.Join("/etc/systemd/system", cfg.ServiceName())
		if err := run(os.Stdout, "sudo", "cp", tmp.Name(), path); err != nil {
			return fmt.Errorf("installing unit (needs sudo): %w", err)
		}
		run(nil, "sudo", "chmod", "644", path)
		run(nil, "sudo", "systemctl", "daemon-reload")
		run(nil, "sudo", "systemctl", "enable", cfg.ServiceName())
		fmt.Println("Installed system unit at", path)
	}
	fmt.Println()
	fmt.Println("Setup complete. Next: 'vllm-helper new <org/model>' or 'vllm-helper search <query>'.")
	return nil
}

func run(out *os.File, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = os.Stdin
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	return cmd.Run()
}
