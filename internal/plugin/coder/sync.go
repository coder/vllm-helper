package coder

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/plugin"
	"github.com/coder/vllm-helper/internal/profile"
)

// Configuration keys. All three are required: a URL without a token, or
// either without the provider to hang the models off, can't sync anything.
const (
	keyURL        = "CODER_URL"
	keyToken      = "CODER_API_TOKEN"
	keyProviderID = "CODER_AI_PROVIDER_ID"
)

const notConfigured = "Coder sync: not configured (" +
	keyURL + "/" + keyToken + "/" + keyProviderID + "), skipping."

const syncUsage = "usage: vllm-helper coder-sync <activate|register|remove> <profile>  |  coder-sync register-all"

func init() { plugin.Register(Plugin{}) }

// Plugin mirrors local profiles into a Coder deployment's model picker.
type Plugin struct{}

func (Plugin) Name() string { return "coder" }

func (Plugin) Configured(cfg *config.Config) bool {
	return cfg != nil && cfg.HasAll(keyURL, keyToken, keyProviderID)
}

// Events: Coder needs to know which model is selectable, so it reacts to a
// switch and to a profile being deleted. It deliberately does not use
// EventReady -- see the comment on activate.
func (Plugin) Events() []plugin.Event {
	return []plugin.Event{plugin.EventActivating, plugin.EventRemoved}
}

func (p Plugin) Handle(_ context.Context, e plugin.Event, s plugin.State) error {
	switch e {
	case plugin.EventActivating:
		activate(s.Config, s.Profile)
	case plugin.EventRemoved:
		remove(s.Config, s.Name)
	default:
		// Only reachable if Events above grows an entry this switch
		// doesn't, which would otherwise be a silent no-op.
		return fmt.Errorf("no handler for event %q", e)
	}
	// Every failure inside is already reported where it happens, with the
	// context needed to act on it; returning it too would print it twice.
	return nil
}

func (p Plugin) Check(_ context.Context, cfg *config.Config) error {
	client := newClient(cfg)
	if client == nil {
		return nil
	}
	return client.Check()
}

func (p Plugin) Commands() []*cobra.Command {
	return []*cobra.Command{{
		Use:   "coder-sync <activate|register|remove> <profile>",
		Short: "Manually sync a profile's Coder model entry",
		// GroupID is left to the CLI, which owns how help is organised.
		Example: "  vllm-helper coder-sync register qwen\n" +
			"  vllm-helper coder-sync register-all",
		Args: cobra.MaximumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error { return run(args) },
	}}
}

func run(args []string) error {
	if len(args) < 1 {
		return errors.New(syncUsage)
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	op := args[0]

	if op == "register-all" {
		return registerAll(cfg)
	}
	if len(args) < 2 {
		return errors.New(syncUsage)
	}
	name := args[1]
	p, err := profile.Load(cfg.Dirs, name)
	// A profile that won't load is fatal except for remove, so a Coder
	// entry can still be cleaned up after its profile file is gone.
	if err != nil && op != "remove" {
		return err
	}
	switch op {
	case "activate":
		activate(cfg, p)
	case "register":
		register(cfg, p)
	case "remove":
		remove(cfg, name)
	default:
		return errors.New(syncUsage)
	}
	return nil
}

// registerAll backfills a (disabled) Coder model-config entry for every
// saved profile, so the deployment's model picker shows the whole library,
// not just models that have been active at least once.
func registerAll(cfg *config.Config) error {
	if !(Plugin{}).Configured(cfg) {
		return errors.New("Coder sync is not configured (" +
			keyURL + "/" + keyToken + "/" + keyProviderID + ")")
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
		register(cfg, p)
	}
	fmt.Printf("Done -- %d profile(s) checked against Coder.\n", len(profiles))
	return nil
}

// ids loads the profile -> Coder model-config-id state file.
func ids(cfg *config.Config) *config.EnvFile {
	f, err := config.ParseEnvFile(cfg.Dirs.PluginStateFile("coder"))
	if err != nil {
		return &config.EnvFile{}
	}
	return f
}

func saveIDs(cfg *config.Config, f *config.EnvFile) {
	_ = os.MkdirAll(cfg.Dirs.State, 0o755)
	if err := f.Write(cfg.Dirs.PluginStateFile("coder"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: couldn't save state:", err)
	}
}

func newClient(cfg *config.Config) *Client {
	if !(Plugin{}).Configured(cfg) {
		return nil
	}
	return NewClient(cfg.Lookup(keyURL, ""), cfg.Lookup(keyToken, ""),
		cfg.Lookup(keyProviderID, ""), cfg.CoderOrganization())
}

// modelConfig translates a profile into the entry Coder stores.
//
// Coder rejects a context_limit of 0, so an unparseable MAX_MODEL_LEN has to
// be reported here: sending it produced an HTTP 400 whose message said
// nothing about which local setting was at fault.
func modelConfig(p *profile.Profile, enabled bool) (ModelConfig, error) {
	limit, err := strconv.ParseInt(p.MaxModelLen(), 10, 64)
	if err != nil || limit <= 0 {
		return ModelConfig{}, fmt.Errorf(
			"MAX_MODEL_LEN is %q, which is not a context length Coder will accept -- fix it with 'vllm-helper set %s MAX_MODEL_LEN=<number>'",
			p.MaxModelLen(), p.Name)
	}
	return ModelConfig{
		Model:        p.Model(),
		DisplayName:  p.DisplayName(),
		ContextLimit: limit,
		Enabled:      enabled,
		IsDefault:    enabled,
	}, nil
}

// activate makes this profile the single enabled/default model in Coder and
// disables every other known profile's entry. Failures warn and continue --
// Coder sync never blocks a local switch.
//
// This runs before the service restarts, so Coder briefly advertises a model
// that is still loading. Deferring it until the endpoint is healthy would
// need a hook that nothing raises under --no-follow, and Coder retries an
// unreachable provider for long enough to cover a normal load, so the entry
// goes in early and stays put.
//
// Disabling the others is not about is_default (Coder enforces a single
// default itself); it is because vLLM serves one model at a time, so any
// other entry names something this machine cannot answer for right now.
func activate(cfg *config.Config, p *profile.Profile) {
	client := newClient(cfg)
	if client == nil {
		fmt.Println(notConfigured)
		return
	}
	mc, err := modelConfig(p, true)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync:", err)
		return
	}
	state := ids(cfg)

	// A saved ID can name an entry that no longer exists -- deleted in
	// Coder's UI, or left behind by a different deployment. Recreating is
	// what was wanted either way, and refusing would leave the picker
	// permanently stale with only an HTTP 404 to explain it.
	id := state.Get(p.Name)
	if id != "" {
		fmt.Printf("Coder sync: updating model-config for '%s'...\n", p.Name)
		err := client.Update(id, mc)
		if err == nil {
			state = disableOthers(cfg, client, state, p.Name)
			fmt.Printf("Coder sync: '%s' is now the only enabled/default model in Coder Agents.\n", p.Name)
			return
		}
		if !errors.Is(err, ErrNotFound) {
			fmt.Fprintln(os.Stderr, "Coder sync: update failed, continuing without it:", err)
			return
		}
		fmt.Printf("Coder sync: '%s' had no entry on this deployment, creating one...\n", p.Name)
	} else {
		fmt.Printf("Coder sync: creating model-config for '%s'...\n", p.Name)
	}
	newID, err := client.Create(mc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: create failed, continuing without it:", err)
		return
	}
	state.Set(p.Name, newID)
	saveIDs(cfg, state)

	state = disableOthers(cfg, client, state, p.Name)
	fmt.Printf("Coder sync: '%s' is now the only enabled/default model in Coder Agents.\n", p.Name)
}

// disableOthers turns off every other profile's entry, because vLLM serves one
// model at a time and any other entry names something this machine cannot
// answer for right now. (Not about is_default -- Coder enforces a single
// default itself.)
//
// An entry that has already gone is forgotten rather than reported: the goal
// is that it not be selectable, and a deleted entry meets that.
func disableOthers(cfg *config.Config, client *Client, state *config.EnvFile, keep string) *config.EnvFile {
	profiles, _ := profile.List(cfg.Dirs)
	dropped := false
	for _, other := range profiles {
		if other.Name == keep {
			continue
		}
		id := state.Get(other.Name)
		if id == "" {
			continue
		}
		switch err := client.Disable(id); {
		case err == nil:
		case errors.Is(err, ErrNotFound):
			state.Delete(other.Name)
			dropped = true
		default:
			fmt.Fprintf(os.Stderr, "Coder sync: failed to disable '%s', continuing: %v\n", other.Name, err)
		}
	}
	if dropped {
		saveIDs(cfg, state)
	}
	return state
}

// register creates a disabled entry for a profile that isn't active
// (backfill), without touching anything else's state.
func register(cfg *config.Config, p *profile.Profile) {
	client := newClient(cfg)
	if client == nil {
		fmt.Println(notConfigured)
		return
	}
	state := ids(cfg)
	if state.Get(p.Name) != "" {
		fmt.Printf("Coder sync: '%s' already has a model-config, leaving as-is.\n", p.Name)
		return
	}
	mc, err := modelConfig(p, false)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync:", err)
		return
	}
	fmt.Printf("Coder sync: registering (disabled) model-config for '%s'...\n", p.Name)
	newID, err := client.Create(mc)
	if err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: register failed, continuing without it:", err)
		return
	}
	state.Set(p.Name, newID)
	saveIDs(cfg, state)
}

// remove deletes a profile's Coder entry (used by `remove`).
func remove(cfg *config.Config, name string) {
	client := newClient(cfg)
	if client == nil {
		fmt.Println(notConfigured)
		return
	}
	state := ids(cfg)
	id := state.Get(name)
	if id == "" {
		return
	}
	fmt.Printf("Coder sync: deleting model-config for '%s'...\n", name)
	if err := client.Delete(id); err != nil {
		fmt.Fprintln(os.Stderr, "Coder sync: delete failed (may already be gone), continuing:", err)
	}
	state.Delete(name)
	saveIDs(cfg, state)
}
