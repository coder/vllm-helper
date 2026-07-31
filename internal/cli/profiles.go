package cli

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
	"github.com/coder/vllm-helper/internal/profile"
)

func cmdList() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
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

func cmdSwitch(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		printProfiles(cfg)
		fmt.Println("\nusage: vllm-helper switch <profile>")
		return errors.New("missing profile name")
	}
	p, err := profile.Load(cfg.Dirs, args[0])
	if err != nil {
		printProfiles(cfg)
		return err
	}
	return activateProfile(cfg, p)
}

func cmdNew(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) < 1 {
		return errors.New("usage: vllm-helper new <org/model-repo>")
	}
	repo := args[0]
	name := profile.NameFromRepo(repo)

	p, err := profile.Load(cfg.Dirs, name)
	if err == nil {
		fmt.Printf("Profile '%s' already exists for %s -- using its saved settings.\n", name, repo)
		fmt.Printf("(edit %s by hand if you want to change its flags)\n", cfg.Dirs.ProfileFile(name))
		return activateProfile(cfg, p)
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

	opts := profile.CreateOpts{
		Repo:        repo,
		MaxModelLen: detected.MaxModelLen,
		ExtraArgs:   extraArgs,
		Source:      "vllm-helper new",
	}
	if extraArgs != "" {
		fmt.Println("Auto-detected from the model card:", extraArgs)
		opts.Status = "NOT YET VERIFIED -- flags auto-detected from README"
	} else {
		fmt.Println("No vllm serve snippet found in the model's README -- check its HF card by")
		fmt.Println("hand and edit the profile if it needs specific flags.")
		opts.Status = "NOT YET VERIFIED -- no flags auto-detected, check the model card by hand"
	}
	fmt.Printf("Saving it as profile '%s' for next time.\n", name)
	p, err = profile.Create(cfg.Dirs, opts)
	if err != nil {
		return err
	}
	return activateProfile(cfg, p)
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
// record the active profile, sync Coder, restart the service, tail logs.
func activateProfile(cfg *config.Config, p *profile.Profile) error {
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
	if err := service(cfg).Restart(); err != nil {
		return fmt.Errorf("restarting service: %w", err)
	}

	fmt.Println()
	fmt.Println("Watching boot log -- Ctrl+C to stop watching (the service keeps running):")
	_ = service(cfg).TailLogs()
	return nil
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

func cmdStatus() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("systemd:  %s\n", service(cfg).IsActive())

	model, prof := "unknown", cfg.ActiveProfile()
	if prof == "" {
		prof = "unknown"
	}
	if active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile()); err == nil {
		if m := active.Get("MODEL"); m != "" {
			model = m
		}
	}
	fmt.Printf("profile:  %s\n", prof)
	fmt.Printf("model:    %s\n", model)

	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://localhost:%s/health", cfg.Port())
	resp, err := client.Get(url)
	code := 0
	if err == nil {
		code = resp.StatusCode
		resp.Body.Close()
	}
	if code == http.StatusOK {
		fmt.Printf("endpoint: healthy (http %d on port %s)\n", code, cfg.Port())
	} else {
		fmt.Printf("endpoint: not responding (http %03d on port %s) -- still loading, or check the service logs\n", code, cfg.Port())
	}
	return nil
}
