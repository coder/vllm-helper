// Package config resolves vllm-helper's directories (XDG layout, matching
// the bash implementation exactly) and loads the layered configuration:
// config.env + secrets.env + the active profile.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dirs holds every path the tool reads or writes. Nothing is ever written
// into the checkout/install directory.
type Dirs struct {
	Config   string // ~/.config/vllm-helper
	State    string // ~/.local/state/vllm-helper
	Data     string // ~/.local/share/vllm-helper
	Profiles string // <Config>/models
}

func ResolveDirs() Dirs {
	home, _ := os.UserHomeDir()
	xdg := func(envOverride, xdgVar, fallback string) string {
		if v := os.Getenv(envOverride); v != "" {
			return v
		}
		base := os.Getenv(xdgVar)
		if base == "" {
			base = filepath.Join(home, fallback)
		}
		return filepath.Join(base, "vllm-helper")
	}
	d := Dirs{
		Config: xdg("VLLM_HELPER_CONFIG_DIR", "XDG_CONFIG_HOME", ".config"),
		State:  xdg("VLLM_HELPER_STATE_DIR", "XDG_STATE_HOME", filepath.Join(".local", "state")),
		Data:   xdg("VLLM_HELPER_DATA_DIR", "XDG_DATA_HOME", filepath.Join(".local", "share")),
	}
	d.Profiles = filepath.Join(d.Config, "models")
	return d
}

func (d Dirs) ConfigFile() string        { return filepath.Join(d.Config, "config.env") }
func (d Dirs) SecretsFile() string       { return filepath.Join(d.Config, "secrets.env") }
func (d Dirs) ActiveEnvFile() string     { return filepath.Join(d.State, "active.env") }
func (d Dirs) ActiveProfileFile() string { return filepath.Join(d.State, "active-profile") }
func (d Dirs) CoderIDsFile() string      { return filepath.Join(d.State, "coder-ids.env") }
func (d Dirs) ProfileFile(name string) string {
	return filepath.Join(d.Profiles, name+".env")
}

// Config is the loaded global configuration (config.env + secrets.env).
type Config struct {
	Dirs    Dirs
	File    *EnvFile // config.env
	Secrets *EnvFile // secrets.env (may be nil)
}

var ErrNotConfigured = errors.New("not configured")

// Load reads config.env (+ secrets.env if present). Returns
// ErrNotConfigured if config.env doesn't exist yet.
func Load() (*Config, error) {
	dirs := ResolveDirs()
	file, err := ParseEnvFile(dirs.ConfigFile())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("%w: no config at %s -- run 'vllm-helper setup' first", ErrNotConfigured, dirs.ConfigFile())
	}
	if err != nil {
		return nil, err
	}
	cfg := &Config{Dirs: dirs, File: file}
	if secrets, err := ParseEnvFile(dirs.SecretsFile()); err == nil {
		cfg.Secrets = secrets
	}
	return cfg, nil
}

// get returns a value from secrets.env or config.env (secrets win), with a
// fallback default.
func (c *Config) get(key, def string) string {
	if c.Secrets != nil {
		if v := c.Secrets.Get(key); v != "" {
			return v
		}
	}
	if v := c.File.Get(key); v != "" {
		return v
	}
	return def
}

func (c *Config) Port() string          { return c.get("PORT", "8001") }
func (c *Config) VLLMBin() string       { return c.get("VLLM_BIN", "") }
func (c *Config) ServiceName() string   { return c.get("SERVICE_NAME", DefaultServiceName) }
func (c *Config) ServiceScope() string  { return c.get("SERVICE_SCOPE", DefaultServiceScope) }
func (c *Config) ModelBudgetGB() string { return c.get("MODEL_BUDGET_GB", "90") }
func (c *Config) APIKey() string        { return c.get("VLLM_API_KEY", "") }

// HFToken returns a HuggingFace token from config/secrets ("" if unset).
// Needed only for gated or private models.
func (c *Config) HFToken() string { return c.get("HF_TOKEN", "") }

func (c *Config) CoderURL() string        { return c.get("CODER_URL", "") }
func (c *Config) CoderToken() string      { return c.get("CODER_API_TOKEN", "") }
func (c *Config) CoderProviderID() string { return c.get("CODER_AI_PROVIDER_ID", "") }
func (c *Config) CoderConfigured() bool {
	return c.CoderURL() != "" && c.CoderToken() != "" && c.CoderProviderID() != ""
}

// ActiveProfile returns the name of the currently active profile ("" if none).
func (c *Config) ActiveProfile() string {
	b, err := os.ReadFile(c.Dirs.ActiveProfileFile())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
