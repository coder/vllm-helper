package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
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

func cmdList(args []string) error {
	fs := newFlagSet("list")
	jsonOut := fs.Bool("json", false, "print profiles as JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if *jsonOut {
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

// switchFlags registers the flags shared by switch/new/restart and returns
// a resolver that turns them into watchOpts.
func switchFlags(fs *flag.FlagSet) func() watchOpts {
	noFollow := fs.Bool("no-follow", false, "restart and return immediately instead of watching the boot")
	wait := fs.Bool("wait", false, "block until the endpoint is healthy, without streaming logs")
	timeout := fs.Duration("timeout", 0, "give up waiting after this long (default: wait forever)")
	return func() watchOpts {
		opts := watchOpts{timeout: *timeout}
		switch {
		case *noFollow:
			// fire-and-forget
		case *wait:
			opts.wait = true
		default:
			// Interactive terminals get the log stream; pipes and scripts
			// get quiet blocking (--wait behavior).
			opts.follow = stdoutIsTTY()
			opts.wait = true
		}
		return opts
	}
}

func cmdSwitch(args []string) error {
	fs := newFlagSet("switch")
	opts := switchFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		printProfiles(cfg)
		fmt.Println("\nusage: vllm-helper switch <profile> [--wait|--no-follow] [--timeout 30m]")
		return errors.New("missing profile name")
	}
	p, err := profile.Load(cfg.Dirs, pos[0])
	if err != nil {
		printProfiles(cfg)
		return err
	}
	return activateProfile(cfg, p, opts())
}

func cmdNew(args []string) error {
	fs := newFlagSet("new")
	opts := switchFlags(fs)
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return errors.New("usage: vllm-helper new <org/model-repo> [--wait|--no-follow] [--timeout 30m]")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	return pullAndSwitch(cfg, pos[0], opts())
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
	fmt.Println("Checking the model's README for known vLLM serving flags...")
	detected, _ := hf.DetectFlags(repo)
	extraArgs := detected.ExtraArgs

	fmt.Println("Checking the model's chat template for known crash patterns...")
	patchedTemplate, needsPatch, err := hf.DetectChatTemplatePatch(repo)
	if err != nil {
		fmt.Println("Chat template check failed, continuing without it:", err)
	} else if needsPatch {
		tmplPath := filepath.Join(cfg.Dirs.Data, "chat-templates", name+".jinja")
		if err := os.MkdirAll(filepath.Dir(tmplPath), 0o755); err != nil {
			fmt.Println("Couldn't create chat-templates dir, continuing without the patch:", err)
		} else if err := os.WriteFile(tmplPath, []byte(patchedTemplate), 0o644); err != nil {
			fmt.Println("Couldn't write patched template, continuing without it:", err)
		} else {
			fmt.Println("This model's bundled template crashes if a system-role message isn't")
			fmt.Println("first in the conversation (common with agent harnesses like Coder Agents).")
			fmt.Println("Patched a local copy:", tmplPath)
			extraArgs = strings.TrimSpace(extraArgs + " --chat-template " + tmplPath)
		}
	}

	createOpts := profile.CreateOpts{
		Repo:        repo,
		MaxModelLen: detected.MaxModelLen,
		ExtraArgs:   extraArgs,
		Source:      "vllm-helper new",
	}
	if extraArgs != "" {
		fmt.Println("Auto-detected from the model card:", extraArgs)
		createOpts.Status = "NOT YET VERIFIED -- flags auto-detected from README"
	} else {
		fmt.Println("No vllm serve snippet found in the model's README -- check its HF card by")
		fmt.Println("hand and edit the profile if it needs specific flags.")
		createOpts.Status = "NOT YET VERIFIED -- no flags auto-detected, check the model card by hand"
	}
	fmt.Printf("Saving it as profile '%s' for next time.\n", name)
	p, err = profile.Create(cfg.Dirs, createOpts)
	if err != nil {
		return err
	}
	return activateProfile(cfg, p, opts)
}

func cmdRemove(args []string) error {
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
	coderRemove(cfg, name)
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

	fmt.Println("Switched to profile:", p.Name)
	fmt.Println()
	for i, line := range p.File.LeadingComments() {
		if i >= 8 {
			break
		}
		fmt.Println(line)
	}

	fmt.Println()
	coderActivate(cfg, p)

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
}

func gatherStatus(cfg *config.Config) statusReport {
	r := statusReport{
		Service: svc(cfg).IsActive(),
		Profile: cfg.ActiveProfile(),
		Model:   "unknown",
		Port:    cfg.Port(),
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
	return r
}

func cmdStatus(args []string) error {
	fs := newFlagSet("status")
	jsonOut := fs.Bool("json", false, "print status as JSON")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	r := gatherStatus(cfg)
	if *jsonOut {
		return printJSON(r)
	}
	fmt.Printf("systemd:  %s\n", r.Service)
	fmt.Printf("profile:  %s\n", r.Profile)
	fmt.Printf("model:    %s\n", r.Model)
	if r.Healthy {
		fmt.Printf("endpoint: healthy (http %d on port %s)\n", r.HTTPStatus, r.Port)
	} else {
		fmt.Printf("endpoint: not responding (http %03d on port %s) -- still loading, or check the service logs\n", r.HTTPStatus, r.Port)
		if hint := svc(cfg).LogHint(); hint != "" {
			fmt.Printf("logs:     %s\n", hint)
		}
	}
	return nil
}
