package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/coder/vllm-helper/internal/coder"
	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/profile"
)

const coderSyncUsage = "usage: vllm-helper coder-sync <activate|register|remove> <profile>  |  coder-sync register-all"

func cmdCoderSync(args []string) error {
	if len(args) < 1 {
		return errors.New(coderSyncUsage)
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	op := args[0]

	if op == "register-all" {
		return coderRegisterAll(cfg)
	}
	if len(args) < 2 {
		return errors.New(coderSyncUsage)
	}
	name := args[1]
	p, err := profile.Load(cfg.Dirs, name)
	if err != nil && op != "remove" {
		return err
	}
	switch op {
	case "activate":
		coderActivate(cfg, p)
	case "register":
		coderRegister(cfg, p)
	case "remove":
		coderRemove(cfg, name)
	default:
		return errors.New(coderSyncUsage)
	}
	return nil
}

// coderRegisterAll backfills a (disabled) Coder model-config entry for
// every saved profile, so the deployment's model picker shows the whole
// library, not just models that have been active at least once.
func coderRegisterAll(cfg *config.Config) error {
	if !cfg.CoderConfigured() {
		return errors.New("Coder sync is not configured (CODER_URL/CODER_API_TOKEN/CODER_AI_PROVIDER_ID)")
	}
	profiles, err := profile.List(cfg.Dirs)
	if err != nil {
		return err
	}
	if len(profiles) == 0 {
		fmt.Println("No profiles to register.")
		return nil
	}
	for _, p := range profiles {
		coderRegister(cfg, p)
	}
	fmt.Printf("Done -- %d profile(s) checked against Coder.\n", len(profiles))
	return nil
}

// coderIDs loads the profile -> Coder model-config-id state file.
func coderIDs(cfg *config.Config) *config.EnvFile {
	f, err := config.ParseEnvFile(cfg.Dirs.CoderIDsFile())
	if err != nil {
		return &config.EnvFile{}
	}
	return f
}

func saveCoderIDs(cfg *config.Config, ids *config.EnvFile) {
	_ = os.MkdirAll(cfg.Dirs.State, 0o755)
	if err := ids.Write(cfg.Dirs.CoderIDsFile(), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: couldn't save state:", err)
	}
}

func coderClient(cfg *config.Config) *coder.Client {
	if !cfg.CoderConfigured() {
		return nil
	}
	return coder.NewClient(cfg.CoderURL(), cfg.CoderToken(), cfg.CoderProviderID())
}

func modelConfig(p *profile.Profile, enabled bool) coder.ModelConfig {
	limit, _ := strconv.ParseInt(p.MaxModelLen(), 10, 64)
	return coder.ModelConfig{
		Model:        p.Model(),
		DisplayName:  p.DisplayName(),
		ContextLimit: limit,
		Enabled:      enabled,
		IsDefault:    enabled,
	}
}

// coderActivate makes this profile the single enabled/default model in
// Coder and disables every other known profile's entry. Failures warn and
// continue -- Coder sync never blocks a local switch.
func coderActivate(cfg *config.Config, p *profile.Profile) {
	client := coderClient(cfg)
	if client == nil {
		fmt.Println("Coder sync: not configured (CODER_URL/CODER_API_TOKEN/CODER_AI_PROVIDER_ID), skipping.")
		return
	}
	ids := coderIDs(cfg)

	if id := ids.Get(p.Name); id == "" {
		fmt.Printf("Coder sync: creating model-config for '%s'...\n", p.Name)
		newID, err := client.Create(modelConfig(p, true))
		if err != nil {
			fmt.Fprintln(os.Stderr, "Coder sync: create failed, continuing without it:", err)
			return
		}
		ids.Set(p.Name, newID)
		saveCoderIDs(cfg, ids)
	} else {
		fmt.Printf("Coder sync: updating model-config for '%s'...\n", p.Name)
		if err := client.Update(id, modelConfig(p, true)); err != nil {
			fmt.Fprintln(os.Stderr, "Coder sync: update failed, continuing without it:", err)
			return
		}
	}

	profiles, _ := profile.List(cfg.Dirs)
	for _, other := range profiles {
		if other.Name == p.Name {
			continue
		}
		id := ids.Get(other.Name)
		if id == "" {
			continue
		}
		if err := client.Disable(id); err != nil {
			fmt.Fprintf(os.Stderr, "Coder sync: failed to disable '%s', continuing: %v\n", other.Name, err)
		}
	}
	fmt.Printf("Coder sync: '%s' is now the only enabled/default model in Coder Agents.\n", p.Name)
}

// coderRegister creates a disabled entry for a profile that isn't active
// (backfill), without touching anything else's state.
func coderRegister(cfg *config.Config, p *profile.Profile) {
	client := coderClient(cfg)
	if client == nil {
		fmt.Println("Coder sync: not configured (CODER_URL/CODER_API_TOKEN/CODER_AI_PROVIDER_ID), skipping.")
		return
	}
	ids := coderIDs(cfg)
	if ids.Get(p.Name) != "" {
		fmt.Printf("Coder sync: '%s' already has a model-config, leaving as-is.\n", p.Name)
		return
	}
	fmt.Printf("Coder sync: registering (disabled) model-config for '%s'...\n", p.Name)
	newID, err := client.Create(modelConfig(p, false))
	if err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: register failed, continuing without it:", err)
		return
	}
	ids.Set(p.Name, newID)
	saveCoderIDs(cfg, ids)
}

// coderRemove deletes a profile's Coder entry (used by `remove`).
func coderRemove(cfg *config.Config, name string) {
	client := coderClient(cfg)
	if client == nil {
		fmt.Println("Coder sync: not configured (CODER_URL/CODER_API_TOKEN/CODER_AI_PROVIDER_ID), skipping.")
		return
	}
	ids := coderIDs(cfg)
	id := ids.Get(name)
	if id == "" {
		return
	}
	fmt.Printf("Coder sync: deleting model-config for '%s'...\n", name)
	if err := client.Delete(id); err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: delete failed (may already be gone), continuing:", err)
	}
	ids.Delete(name)
	saveCoderIDs(cfg, ids)
}
