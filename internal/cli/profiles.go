package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/catalog"
	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/plugin"
	"github.com/coder/vllm-helper/internal/profile"
)

// profileInfo is the machine-readable shape of one saved profile.
type profileInfo struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	MaxModelLen string `json:"max_model_len"`
	GPUMemUtil  string `json:"gpu_memory_utilization"`
	ExtraArgs   string `json:"extra_args"`
	Status      string `json:"status"`
	Active      bool   `json:"active"`
}

func newListCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:     "list",
		Short:   "List saved model profiles",
		GroupID: groupModels,
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return runList(jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print profiles as JSON")
	return c
}

func runList(jsonOut bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if jsonOut {
		profiles, _ := profile.List(cfg.Dirs)
		active := cfg.ActiveProfile()
		out := make([]profileInfo, 0, len(profiles))
		for _, p := range profiles {
			out = append(out, profileInfo{
				Name:        p.Name,
				Model:       p.Model(),
				MaxModelLen: p.MaxModelLen(),
				GPUMemUtil:  p.File.Get("GPU_MEMORY_UTILIZATION"),
				ExtraArgs:   p.ExtraArgs(),
				Status:      p.Status(),
				Active:      p.Name == active,
			})
		}
		return printJSON(out)
	}
	printProfiles(cfg)
	return nil
}

func printProfiles(cfg *config.Config) {
	profiles, _ := profile.List(cfg.Dirs)
	fmt.Printf("Available profiles (%s/*.env):\n", cfg.Dirs.Profiles)
	if len(profiles) == 0 {
		fmt.Println("  (none yet -- add one with 'vllm-helper new <org/model>')")
		return
	}
	active := cfg.ActiveProfile()
	for _, p := range profiles {
		marker := ""
		if p.Name == active {
			marker = " [active]"
		}
		fmt.Printf("  %-24s %s%s\n", p.Name, p.Status(), marker)
	}
}

// waitFlags are shared by switch/new/restart: how long to stay attached
// while the model loads.
type waitFlags struct {
	noFollow bool
	wait     bool
	timeout  time.Duration
}

func (w *waitFlags) register(c *cobra.Command) {
	c.Flags().BoolVar(&w.noFollow, "no-follow", false, "restart and return immediately instead of watching the boot")
	c.Flags().BoolVar(&w.wait, "wait", false, "block until the endpoint is healthy, without streaming logs")
	c.Flags().DurationVar(&w.timeout, "timeout", 0, "give up waiting after this long (default: wait forever)")
}

func (w *waitFlags) opts() watchOpts {
	opts := watchOpts{timeout: w.timeout}
	switch {
	case w.noFollow:
		// fire-and-forget
	case w.wait:
		opts.wait = true
	default:
		// Interactive terminals get the log stream; pipes and scripts
		// get quiet blocking (--wait behavior).
		opts.follow = stdoutIsTTY()
		opts.wait = true
	}
	return opts
}

func newSwitchCmd() *cobra.Command {
	var w waitFlags
	c := &cobra.Command{
		Use:     "switch <profile>",
		Short:   "Switch to a saved profile, restarting the server",
		GroupID: groupModels,
		// Not ExactArgs: with no profile named, runSwitch lists what's
		// available, which beats cobra's count of missing arguments.
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return runSwitch(args, w.opts())
		},
	}
	w.register(c)
	return c
}

func runSwitch(args []string, opts watchOpts) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		printProfiles(cfg)
		fmt.Println("\nusage: vllm-helper switch <profile> [--wait|--no-follow] [--timeout 30m]")
		return errors.New("missing profile name")
	}
	p, err := profile.Load(cfg.Dirs, args[0])
	if err != nil {
		printProfiles(cfg)
		return err
	}
	return activateProfile(cfg, p, opts)
}

func newNewCmd() *cobra.Command {
	var w waitFlags
	c := &cobra.Command{
		Use:     "new <org/model-repo>",
		Short:   "Pull a new HuggingFace model, save it as a profile, and switch to it",
		GroupID: groupModels,
		Args:    cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			if len(args) < 1 {
				return errors.New("usage: vllm-helper new <org/model-repo> [--wait|--no-follow] [--timeout 30m]")
			}
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			return pullAndSwitch(cfg, args[0], w.opts())
		},
	}
	w.register(c)
	return c
}

// pullAndSwitch is `new` minus the flag parsing, so `search` can funnel
// into it with its own options.
func pullAndSwitch(cfg *config.Config, repo string, opts watchOpts) error {
	name := profile.NameFromRepo(repo)

	p, err := profile.Load(cfg.Dirs, name)
	if err == nil {
		fmt.Printf("Profile '%s' already exists for %s -- using its saved settings.\n", name, repo)
		fmt.Printf("(change it with 'vllm-helper edit %s' if it needs different flags)\n", name)
		return activateProfile(cfg, p, opts)
	}

	fmt.Println("Pulling new model:", repo)
	fmt.Println("Checking the model's README for known vLLM serving flags, and its")
	fmt.Println("chat template for known crash patterns...")
	plan := catalog.PrepareProfile(cfg.Dirs, repo, vllmCaps(cfg))
	for _, n := range plan.Notes {
		fmt.Println(n)
	}
	createOpts := plan.Opts
	fmt.Printf("Saving it as profile '%s' for next time.\n", name)
	p, err = profile.Create(cfg.Dirs, createOpts)
	if err != nil {
		return err
	}
	return activateProfile(cfg, p, opts)
}

func newRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "remove <profile>",
		Short:   "Delete a profile, and its Coder model entry when sync is configured",
		GroupID: groupModels,
		Args:    cobra.MaximumNArgs(1),
		RunE:    func(_ *cobra.Command, args []string) error { return runRemove(args) },
	}
}

func runRemove(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		printProfiles(cfg)
		return errors.New("usage: vllm-helper remove <profile>")
	}
	name := args[0]
	if _, err := profile.Load(cfg.Dirs, name); err != nil {
		return err
	}
	if name == cfg.ActiveProfile() {
		return fmt.Errorf("'%s' is the currently active model -- switch to something else first", name)
	}
	plugin.Dispatch(context.Background(), os.Stderr, plugin.EventRemoved, plugin.State{
		Config:  cfg,
		Name:    name,
		BaseURL: localBaseURL(cfg),
	})
	if err := profile.Remove(cfg.Dirs, name); err != nil {
		return err
	}
	fmt.Println("Removed profile:", name)
	return nil
}

// activateProfile is the core of switch/new: assemble the runtime env,
// record the active profile, sync Coder, restart the service, and watch
// the boot according to opts.
func activateProfile(cfg *config.Config, p *profile.Profile, opts watchOpts) error {
	if err := writeActiveEnv(cfg, p); err != nil {
		return err
	}
	// Warn, never refuse: profiles get hand-edited and vLLM gets upgraded
	// underneath one that used to work, but the user asked for this model
	// and it is not this command's place to overrule that.
	warnExtraArgs(p, vllmCaps(cfg))

	fmt.Println("Switched to profile:", p.Name)
	fmt.Println()
	for i, line := range p.File.LeadingComments() {
		if i >= 8 {
			break
		}
		fmt.Println(line)
	}

	fmt.Println()
	plugin.Dispatch(context.Background(), os.Stderr, plugin.EventActivating, plugin.State{
		Config:  cfg,
		Name:    p.Name,
		Profile: p,
		BaseURL: localBaseURL(cfg),
	})

	fmt.Println()
	fmt.Printf("Restarting %s (this reloads the model -- can take several minutes,\n", cfg.ServiceName())
	fmt.Println("longer if the model isn't cached on disk yet)...")
	if err := svc(cfg).Restart(); err != nil {
		return fmt.Errorf("restarting service: %w", err)
	}

	if !opts.follow && !opts.wait {
		fmt.Println()
		fmt.Println("Not waiting for the model to load. Check progress with 'vllm-helper status'")
		fmt.Println("or 'vllm-helper logs'.")
		return nil
	}
	fmt.Println()
	return watchBoot(cfg, p.Name, opts)
}

// writeActiveEnv assembles state/active.env from config.env + secrets.env +
// the profile (same layering as the bash implementation, and the output
// stays bash-sourceable) and records the active profile name.
func writeActiveEnv(cfg *config.Config, p *profile.Profile) error {
	if err := os.MkdirAll(cfg.Dirs.State, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	appendFile := func(f *config.EnvFile) {
		if f == nil {
			return
		}
		for _, line := range f.Lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			b.WriteString(line + "\n")
		}
	}
	appendFile(cfg.File)
	appendFile(cfg.Secrets)
	appendFile(p.File)

	tmp := cfg.Dirs.ActiveEnvFile() + ".tmp"
	if err := os.WriteFile(tmp, []byte(b.String()), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, cfg.Dirs.ActiveEnvFile()); err != nil {
		return err
	}
	return os.WriteFile(cfg.Dirs.ActiveProfileFile(), []byte(p.Name+"\n"), 0o644)
}

// statusReport is the machine-readable shape of `status`.
type statusReport struct {
	Service    string `json:"service"`
	Profile    string `json:"profile"`
	Model      string `json:"model"`
	Port       string `json:"port"`
	Healthy    bool   `json:"endpoint_healthy"`
	HTTPStatus int    `json:"http_status"`
	Crashed    bool   `json:"crashed"`
	Restarts   int    `json:"restarts"`
	FailReason string `json:"fail_reason,omitempty"`
	LastLog    string `json:"last_log,omitempty"`
}

func gatherStatus(cfg *config.Config) statusReport {
	health := svc(cfg).Health()
	r := statusReport{
		Service:    health.State,
		Profile:    cfg.ActiveProfile(),
		Model:      "unknown",
		Port:       cfg.Port(),
		Crashed:    health.Crashed,
		Restarts:   health.NRestarts,
		FailReason: health.Result,
	}
	if r.Profile == "" {
		r.Profile = "unknown"
	}
	if active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile()); err == nil {
		if m := active.Get("MODEL"); m != "" {
			r.Model = m
		}
	}
	r.HTTPStatus, r.Healthy = probeHealth(cfg.Port())
	if r.Crashed {
		// Only worth the journalctl call when there's a failure to explain.
		r.LastLog = lastMeaningfulLine(svc(cfg).LastLogs(40))
	}
	return r
}

// lastMeaningfulLine picks the line most likely to say why the server died.
// A Python traceback ends with the exception, and vLLM's fatal startup
// errors are one-liners, so the last non-blank line is nearly always it.
func lastMeaningfulLine(logs string) string {
	lines := strings.Split(logs, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return line
		}
	}
	return ""
}

func newStatusCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:     "status",
		Short:   "Service state, active model, and whether the endpoint responds",
		GroupID: groupServer,
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return runStatus(jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print status as JSON")
	return c
}

func runStatus(jsonOut bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	r := gatherStatus(cfg)
	if jsonOut {
		return printJSON(r)
	}
	fmt.Printf("%-9s %s\n", config.ServiceManager+":", r.Service)
	fmt.Printf("profile:  %s\n", r.Profile)
	fmt.Printf("model:    %s\n", r.Model)
	if r.Healthy {
		fmt.Printf("endpoint: healthy (http %d on port %s)\n", r.HTTPStatus, r.Port)
		return nil
	}

	// An unresponsive endpoint means one of two very different things, and
	// telling someone to wait for a server that keeps dying wastes their
	// afternoon. Say which one it is.
	if r.Crashed {
		fmt.Printf("endpoint: not responding (http %03d on port %s) -- the service is failing to start, not loading\n", r.HTTPStatus, r.Port)
		detail := r.FailReason
		if r.Restarts > 0 {
			detail = fmt.Sprintf("%s, %d restarts", detail, r.Restarts)
			detail = strings.TrimPrefix(detail, ", ")
		}
		if detail != "" {
			fmt.Printf("failure:  %s\n", detail)
		}
		if r.LastLog != "" {
			fmt.Printf("last log: %s\n", r.LastLog)
		}
	} else {
		fmt.Printf("endpoint: not responding (http %03d on port %s) -- still loading, or check the service logs\n", r.HTTPStatus, r.Port)
	}
	if hint := svc(cfg).LogHint(); hint != "" {
		fmt.Printf("logs:     %s\n", hint)
	}
	return nil
}
