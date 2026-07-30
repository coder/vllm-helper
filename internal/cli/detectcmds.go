package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/detect"
	"github.com/coder/vllm-helper/internal/profile"
)

// cmdDoctor prints everything we can detect about this machine and flags
// drift between config and reality.
func cmdDoctor() error {
	hw := detect.Probe()
	fmt.Println("HARDWARE")
	fmt.Printf("  memory:     %d GB total\n", hw.TotalMemGB)
	if hw.GPUName != "" {
		kind := "discrete VRAM"
		if hw.Unified {
			kind = "unified memory"
		}
		fmt.Printf("  gpu:        %s (%s)\n", hw.GPUName, kind)
	} else {
		fmt.Println("  gpu:        no NVIDIA GPU visible (nvidia-smi missing or failed)")
	}
	fmt.Printf("  budget:     ~%d GB of model weights (%s)\n", hw.BudgetGB, hw.BudgetReason)
	fmt.Println()

	cfg, err := loadConfig()
	fmt.Println("CONFIG")
	if err != nil {
		fmt.Printf("  %v\n\n", err)
		cfg = nil
	} else {
		fmt.Printf("  config:     %s\n", cfg.Dirs.ConfigFile())
		fmt.Printf("  port:       %s\n", cfg.Port())
		bin := cfg.VLLMBin()
		if bin == "" {
			fmt.Println("  vllm bin:   NOT SET -- set VLLM_BIN in config.env")
		} else if info, err := os.Stat(bin); err != nil || info.Mode()&0o111 == 0 {
			fmt.Printf("  vllm bin:   %s (MISSING or not executable)\n", bin)
		} else {
			fmt.Printf("  vllm bin:   %s\n", bin)
		}
		fmt.Printf("  service:    %s (%s scope) -- %s\n", cfg.ServiceName(), cfg.ServiceScope(), service(cfg).IsActive())
		if cfg.CoderConfigured() {
			fmt.Println("  coder sync: configured")
		} else {
			fmt.Println("  coder sync: not configured (optional)")
		}
		fmt.Println()
	}

	servers := detect.ScanProcesses()
	containers := detect.ScanContainers()
	fmt.Println("RUNNING SERVERS")
	if len(servers) == 0 && len(containers) == 0 {
		fmt.Println("  no vLLM processes or containers found")
	}
	for _, s := range servers {
		fmt.Printf("  pid %d: %s on port %s", s.PID, orUnknown(s.Model), s.Port)
		switch {
		case s.InContainer:
			fmt.Printf(" (inside a %s container)", s.ContainerHint)
		case s.SystemdUnit != "":
			fmt.Printf(" (systemd unit %s)", s.SystemdUnit)
		default:
			fmt.Printf(" (bare process -- not service-managed)")
		}
		fmt.Println()
		if s.ExtraArgs != "" {
			fmt.Printf("      flags: %s\n", s.ExtraArgs)
		}
	}
	for _, c := range containers {
		fmt.Printf("  %s container %s: image %s, ports %s\n", c.Runtime, c.Name, c.Image, orUnknown(c.Ports))
	}
	fmt.Println()

	fmt.Println("ENDPOINTS (localhost probe)")
	apiKey, extraPort := "", ""
	if cfg != nil {
		apiKey, extraPort = cfg.APIKey(), cfg.Port()
	}
	eps := detect.ProbePorts(detect.DefaultPorts(extraPort), apiKey)
	if len(eps) == 0 {
		fmt.Println("  no OpenAI-compatible endpoints responding")
	}
	for _, ep := range eps {
		desc := []string{}
		if ep.Healthy {
			desc = append(desc, "healthy")
		}
		if ep.NeedsAuth {
			desc = append(desc, "requires auth (key not accepted or not configured)")
		}
		if len(ep.Models) > 0 {
			desc = append(desc, "serving "+strings.Join(ep.Models, ", "))
		}
		fmt.Printf("  :%s  %s\n", ep.Port, strings.Join(desc, "; "))
	}

	fmt.Println()
	checkToolchain()

	// Drift checks: the part a human wouldn't spot at a glance.
	if cfg != nil {
		var warnings []string
		activeModel := ""
		if active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile()); err == nil {
			activeModel = active.Get("MODEL")
		}
		for _, s := range servers {
			if s.InContainer {
				continue
			}
			if s.Port == cfg.Port() && activeModel != "" && s.Model != "" && s.Model != activeModel {
				warnings = append(warnings, fmt.Sprintf(
					"config says model %s but port %s is serving %s -- was the server started outside vllm-helper? Run 'vllm-helper adopt'.",
					activeModel, s.Port, s.Model))
			}
			if s.SystemdUnit != "" && s.SystemdUnit != cfg.ServiceName() {
				warnings = append(warnings, fmt.Sprintf(
					"running server belongs to systemd unit %s but config manages %s -- run 'vllm-helper adopt' to take it over.",
					s.SystemdUnit, cfg.ServiceName()))
			}
		}
		if len(warnings) > 0 {
			fmt.Println()
			fmt.Println("WARNINGS")
			for _, w := range warnings {
				fmt.Println("  ! " + w)
			}
		}
	}
	return nil
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// checkToolchain verifies the build packages vLLM's JIT kernel compilation
// needs (Triton, FlashInfer). Which one bites depends on the first model
// served, so checking up front beats discovering them one crash at a time.
func checkToolchain() {
	fmt.Println("BUILD TOOLCHAIN (for vLLM/FlashInfer/Triton JIT kernels)")
	var missing []string
	check := func(label, aptPkg string, ok bool) {
		state := "ok"
		if !ok {
			state = "MISSING"
			if aptPkg != "" {
				missing = append(missing, aptPkg)
			}
		}
		fmt.Printf("  %-12s %s\n", label, state)
	}
	has := func(bins ...string) bool {
		for _, b := range bins {
			if _, err := exec.LookPath(b); err != nil {
				return false
			}
		}
		return true
	}
	pyHeaders, _ := filepath.Glob("/usr/include/python3*/Python.h")
	check("ninja", "ninja-build", has("ninja"))
	check("gcc/g++", "build-essential", has("gcc", "g++"))
	check("Python.h", pythonDevPkg(), len(pyHeaders) > 0)

	if len(missing) == 0 {
		return
	}
	if !has("apt-get") {
		fmt.Printf("  install the missing packages with your distro's package manager: %s\n", strings.Join(missing, " "))
		return
	}
	fmt.Println()
	fmt.Printf("  fix: sudo apt-get install -y %s\n", strings.Join(missing, " "))
	if confirm("  Run this now?", false) {
		cmd := exec.Command("sudo", append([]string{"apt-get", "install", "-y"}, missing...)...)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "  install failed:", err)
		}
	}
}

func pythonDevPkg() string {
	out, err := exec.Command("python3", "-c", "import sys; print(f'python{sys.version_info.major}.{sys.version_info.minor}-dev')").Output()
	if err != nil {
		return "python3-dev"
	}
	return strings.TrimSpace(string(out))
}

// cmdAdopt detects a running vLLM server and takes it over: saves its
// command line as a profile, points config at its port/binary/unit, and
// marks it active -- WITHOUT restarting anything.
func cmdAdopt() error {
	cfg, err := loadConfig()
	if err != nil {
		if errors.Is(err, config.ErrNotConfigured) {
			return errors.New("no config yet -- run 'vllm-helper setup' (it offers adoption as part of first-time setup)")
		}
		return err
	}
	return adoptInto(cfg)
}

// adoptInto runs the adoption flow against an existing config. Shared by
// `adopt` and `setup`.
func adoptInto(cfg *config.Config) error {
	servers := detect.ScanProcesses()
	containers := detect.ScanContainers()

	var host []detect.Server
	for _, s := range servers {
		if !s.InContainer {
			host = append(host, s)
		}
	}

	if len(host) == 0 {
		if len(containers) > 0 {
			fmt.Println("Found vLLM running in containers (not adoptable as a host service yet):")
			for _, c := range containers {
				fmt.Printf("  %s container %s (image %s)\n", c.Runtime, c.Name, c.Image)
				if args := detect.InspectArgs(c.Runtime, c.ID); len(args) > 0 {
					fmt.Printf("      args: %s\n", strings.Join(args, " "))
				}
			}
			fmt.Println()
			fmt.Println("Container-managed switching is on the roadmap. For now vllm-helper can")
			fmt.Println("still search/size models for you, and 'vllm-helper doctor' will keep")
			fmt.Println("showing what's running.")
			return nil
		}
		fmt.Println("No running vLLM server found to adopt.")
		fmt.Println("Use 'vllm-helper switch <profile>' to start one, or 'vllm-helper doctor' to see what was scanned.")
		return nil
	}

	s := host[0]
	if len(host) > 1 {
		fmt.Printf("Found %d running vLLM servers; adopting the one on port %s (pid %d).\n\n", len(host), s.Port, s.PID)
	}

	fmt.Println("Found a running vLLM server:")
	fmt.Printf("  model:  %s\n", orUnknown(s.Model))
	fmt.Printf("  port:   %s\n", s.Port)
	if s.MaxModelLen != "" {
		fmt.Printf("  ctx:    %s\n", s.MaxModelLen)
	}
	if s.ExtraArgs != "" {
		fmt.Printf("  flags:  %s\n", s.ExtraArgs)
	}
	if s.SystemdUnit != "" {
		fmt.Printf("  unit:   %s\n", s.SystemdUnit)
	} else {
		fmt.Println("  unit:   none (bare process)")
	}
	if s.VLLMBin != "" {
		fmt.Printf("  binary: %s\n", s.VLLMBin)
	}
	fmt.Println()
	fmt.Println("Adopting will save this as a profile and make vllm-helper manage future")
	fmt.Println("switches. The running server is NOT restarted or interrupted.")
	if !confirm("Adopt this setup?", true) {
		fmt.Println("Left everything as-is.")
		return nil
	}

	if s.Model == "" {
		return errors.New("couldn't determine the model from the process command line -- adoption needs it")
	}

	// Profile: reuse an existing one for this model or create from the
	// observed flags.
	name := profile.NameFromRepo(s.Model)
	p, err := profile.Load(cfg.Dirs, name)
	if err != nil {
		p, err = profile.Create(cfg.Dirs, profile.CreateOpts{
			Repo:        s.Model,
			MaxModelLen: s.MaxModelLen,
			ExtraArgs:   s.ExtraArgs,
			Status:      "ADOPTED from a running server -- flags taken from its live command line",
			Source:      "vllm-helper adopt",
		})
		if err != nil {
			return err
		}
		fmt.Printf("Saved profile '%s' from the running command line.\n", name)
	} else {
		fmt.Printf("Profile '%s' already exists for this model -- keeping its saved settings.\n", name)
	}

	// Config: follow the server's reality.
	changed := false
	if cfg.Port() != s.Port {
		cfg.File.Set("PORT", s.Port)
		changed = true
		fmt.Printf("Config: PORT -> %s (matching the running server)\n", s.Port)
	}
	if s.VLLMBin != "" && cfg.VLLMBin() != s.VLLMBin {
		cfg.File.Set("VLLM_BIN", s.VLLMBin)
		changed = true
		fmt.Printf("Config: VLLM_BIN -> %s\n", s.VLLMBin)
	}
	if s.SystemdUnit != "" && cfg.ServiceName() != s.SystemdUnit {
		cfg.File.Set("SERVICE_NAME", s.SystemdUnit)
		scope := "system"
		if s.UserScope {
			scope = "user"
		}
		cfg.File.Set("SERVICE_SCOPE", scope)
		changed = true
		fmt.Printf("Config: SERVICE_NAME -> %s (%s scope)\n", s.SystemdUnit, scope)
	}
	if changed {
		if err := cfg.File.Write(cfg.Dirs.ConfigFile(), 0o644); err != nil {
			return err
		}
	}

	if err := writeActiveEnv(cfg, p); err != nil {
		return err
	}
	fmt.Printf("Marked '%s' as the active profile.\n", name)

	if s.SystemdUnit == "" {
		fmt.Println()
		fmt.Println("NOTE: the running server is a bare process (no systemd unit), so")
		fmt.Println("'vllm-helper switch' can't restart it. Stop it yourself when ready and")
		fmt.Println("run 'vllm-helper setup' to install the service, then switch normally.")
	}
	if !s.APIKeySet {
		fmt.Println()
		fmt.Println("NOTE: the running server has NO --api-key. After the next switch it will")
		fmt.Printf("require the key in %s -- update your clients.\n", cfg.Dirs.SecretsFile())
	}
	fmt.Println()
	fmt.Println("Adoption complete. 'vllm-helper status' now reflects this server, and the")
	fmt.Println("next 'vllm-helper switch' will manage it.")
	return nil
}
