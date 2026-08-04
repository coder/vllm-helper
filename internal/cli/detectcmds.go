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
	"github.com/coder/vllm-helper/internal/hf"
	"github.com/coder/vllm-helper/internal/profile"
)

// doctorReport is everything doctor learns, in one machine-readable shape.
type doctorReport struct {
	Hardware    detect.Hardware    `json:"hardware"`
	Config      *doctorConfig      `json:"config"`
	ConfigError string             `json:"config_error,omitempty"`
	Servers     []detect.Server    `json:"servers"`
	Containers  []detect.Container `json:"containers"`
	Endpoints   []detect.Endpoint  `json:"endpoints"`
	Toolchain   []toolchainCheck   `json:"toolchain"`
	Coder       coderStatus        `json:"coder_sync"`
	HFToken     hfTokenStatus      `json:"hf_token"`
	Warnings    []string           `json:"warnings"`
}

type doctorConfig struct {
	Path            string `json:"path"`
	Port            string `json:"port"`
	VLLMBin         string `json:"vllm_bin"`
	VLLMBinOK       bool   `json:"vllm_bin_ok"`
	Service         string `json:"service"`
	Scope           string `json:"scope"`
	ServiceState    string `json:"service_state"`
	CoderConfigured bool   `json:"coder_sync_configured"`
}

type toolchainCheck struct {
	Name       string `json:"name"`
	OK         bool   `json:"ok"`
	AptPackage string `json:"apt_package,omitempty"`
}

type coderStatus struct {
	Configured bool   `json:"configured"`
	URL        string `json:"url,omitempty"`
	Reachable  bool   `json:"reachable"`
	Error      string `json:"error,omitempty"`
}

type hfTokenStatus struct {
	Present bool   `json:"present"`
	Source  string `json:"source,omitempty"`
	Account string `json:"account,omitempty"`
	Error   string `json:"error,omitempty"`
}

// cmdDoctor prints everything we can detect about this machine and flags
// drift between config and reality.
func cmdDoctor(args []string) error {
	fs := newFlagSet("doctor")
	jsonOut := fs.Bool("json", false, "print the full report as JSON (no prompts)")
	yes := fs.Bool("yes", false, "install missing toolchain packages without asking")
	if _, err := parseArgs(fs, args); err != nil {
		return err
	}

	r := gatherDoctor()
	if *jsonOut {
		return printJSON(r)
	}
	renderDoctor(r, *yes)
	return nil
}

func gatherDoctor() doctorReport {
	r := doctorReport{Hardware: detect.Probe()}

	cfg, err := loadConfig()
	if err != nil {
		r.ConfigError = err.Error()
		cfg = nil
	} else {
		bin := cfg.VLLMBin()
		binOK := false
		if bin != "" {
			if info, err := os.Stat(bin); err == nil && info.Mode()&0o111 != 0 {
				binOK = true
			}
		}
		r.Config = &doctorConfig{
			Path:            cfg.Dirs.ConfigFile(),
			Port:            cfg.Port(),
			VLLMBin:         bin,
			VLLMBinOK:       binOK,
			Service:         cfg.ServiceName(),
			Scope:           cfg.ServiceScope(),
			ServiceState:    service(cfg).IsActive(),
			CoderConfigured: cfg.CoderConfigured(),
		}
	}

	r.Servers = detect.ScanProcesses()
	r.Containers = detect.ScanContainers()

	apiKey, extraPort := "", ""
	if cfg != nil {
		apiKey, extraPort = cfg.APIKey(), cfg.Port()
	}
	r.Endpoints = detect.ProbePorts(detect.DefaultPorts(extraPort), apiKey)

	r.Toolchain = gatherToolchain()
	r.Coder = gatherCoderStatus(cfg)
	r.HFToken = gatherHFToken(cfg)

	// Drift checks: the part a human wouldn't spot at a glance.
	if cfg != nil {
		activeModel := ""
		if active, err := config.ParseEnvFile(cfg.Dirs.ActiveEnvFile()); err == nil {
			activeModel = active.Get("MODEL")
		}
		for _, s := range r.Servers {
			if s.InContainer {
				continue
			}
			if s.Port == cfg.Port() && activeModel != "" && s.Model != "" && s.Model != activeModel {
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"config says model %s but port %s is serving %s -- was the server started outside vllm-helper? Run 'vllm-helper adopt'.",
					activeModel, s.Port, s.Model))
			}
			if s.SystemdUnit != "" && s.SystemdUnit != cfg.ServiceName() {
				r.Warnings = append(r.Warnings, fmt.Sprintf(
					"running server belongs to systemd unit %s but config manages %s -- run 'vllm-helper adopt' to take it over.",
					s.SystemdUnit, cfg.ServiceName()))
			}
		}
	}
	return r
}

// gatherCoderStatus goes beyond "the variables are set": it calls the API
// so an expired token or wrong URL shows up here instead of on the next
// switch.
func gatherCoderStatus(cfg *config.Config) coderStatus {
	st := coderStatus{}
	if cfg == nil || !cfg.CoderConfigured() {
		return st
	}
	st.Configured = true
	st.URL = cfg.CoderURL()
	client := coderClient(cfg)
	if err := client.Check(); err != nil {
		st.Error = err.Error()
	} else {
		st.Reachable = true
	}
	return st
}

// gatherHFToken reports whether a HuggingFace token is available (gated
// models can't be pulled without one) and whether the Hub accepts it.
func gatherHFToken(cfg *config.Config) hfTokenStatus {
	st := hfTokenStatus{}
	token := ""
	if cfg != nil && cfg.HFToken() != "" {
		token, st.Source = cfg.HFToken(), "config"
	} else {
		for _, k := range []string{"HF_TOKEN", "HUGGING_FACE_HUB_TOKEN", "HUGGINGFACE_TOKEN"} {
			if v := os.Getenv(k); v != "" {
				token, st.Source = v, "environment ("+k+")"
				break
			}
		}
	}
	if token == "" {
		return st
	}
	st.Present = true
	account, err := hf.WhoAmI(token)
	if err != nil {
		st.Error = err.Error()
	} else {
		st.Account = account
	}
	return st
}

func renderDoctor(r doctorReport, yes bool) {
	hw := r.Hardware
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

	fmt.Println("CONFIG")
	if r.Config == nil {
		fmt.Printf("  %s\n\n", r.ConfigError)
	} else {
		c := r.Config
		fmt.Printf("  config:     %s\n", c.Path)
		fmt.Printf("  port:       %s\n", c.Port)
		switch {
		case c.VLLMBin == "":
			fmt.Println("  vllm bin:   NOT SET -- set VLLM_BIN in config.env")
		case !c.VLLMBinOK:
			fmt.Printf("  vllm bin:   %s (MISSING or not executable)\n", c.VLLMBin)
		default:
			fmt.Printf("  vllm bin:   %s\n", c.VLLMBin)
		}
		fmt.Printf("  service:    %s (%s scope) -- %s\n", c.Service, c.Scope, c.ServiceState)
		fmt.Println()
	}

	fmt.Println("RUNNING SERVERS")
	if len(r.Servers) == 0 && len(r.Containers) == 0 {
		fmt.Println("  no vLLM processes or containers found")
	}
	for _, s := range r.Servers {
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
	for _, c := range r.Containers {
		fmt.Printf("  %s container %s: image %s, ports %s\n", c.Runtime, c.Name, c.Image, orUnknown(c.Ports))
	}
	fmt.Println()

	fmt.Println("ENDPOINTS (localhost probe)")
	if len(r.Endpoints) == 0 {
		fmt.Println("  no OpenAI-compatible endpoints responding")
	}
	for _, ep := range r.Endpoints {
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

	fmt.Println("INTEGRATIONS")
	switch {
	case !r.Coder.Configured:
		fmt.Println("  coder sync: not configured (optional)")
	case r.Coder.Reachable:
		fmt.Printf("  coder sync: OK -- %s answered with this token\n", r.Coder.URL)
	default:
		fmt.Printf("  coder sync: CONFIGURED BUT FAILING -- %s\n", r.Coder.Error)
		fmt.Println("              (check CODER_URL and that CODER_API_TOKEN hasn't expired)")
	}
	switch {
	case !r.HFToken.Present:
		fmt.Println("  hf token:   not set (fine for public models; gated models like Llama")
		fmt.Println("              need HF_TOKEN in config.env or the environment)")
	case r.HFToken.Error != "":
		fmt.Printf("  hf token:   set (%s) but the Hub rejected it: %s\n", r.HFToken.Source, r.HFToken.Error)
	default:
		fmt.Printf("  hf token:   OK -- authenticated as %q (%s)\n", r.HFToken.Account, r.HFToken.Source)
	}
	fmt.Println()

	renderToolchain(r.Toolchain, yes)

	if len(r.Warnings) > 0 {
		fmt.Println()
		fmt.Println("WARNINGS")
		for _, w := range r.Warnings {
			fmt.Println("  ! " + w)
		}
	}
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// gatherToolchain verifies the build packages vLLM's JIT kernel compilation
// needs (Triton, FlashInfer). Which one bites depends on the first model
// served, so checking up front beats discovering them one crash at a time.
func gatherToolchain() []toolchainCheck {
	has := func(bins ...string) bool {
		for _, b := range bins {
			if _, err := exec.LookPath(b); err != nil {
				return false
			}
		}
		return true
	}
	pyHeaders, _ := filepath.Glob("/usr/include/python3*/Python.h")
	return []toolchainCheck{
		{Name: "ninja", AptPackage: "ninja-build", OK: has("ninja")},
		{Name: "gcc/g++", AptPackage: "build-essential", OK: has("gcc", "g++")},
		{Name: "Python.h", AptPackage: pythonDevPkg(), OK: len(pyHeaders) > 0},
	}
}

func renderToolchain(checks []toolchainCheck, yes bool) {
	fmt.Println("BUILD TOOLCHAIN (for vLLM/FlashInfer/Triton JIT kernels)")
	var missing []string
	for _, c := range checks {
		state := "ok"
		if !c.OK {
			state = "MISSING"
			if c.AptPackage != "" {
				missing = append(missing, c.AptPackage)
			}
		}
		fmt.Printf("  %-12s %s\n", c.Name, state)
	}
	if len(missing) == 0 {
		return
	}
	if _, err := exec.LookPath("apt-get"); err != nil {
		fmt.Printf("  install the missing packages with your distro's package manager: %s\n", strings.Join(missing, " "))
		return
	}
	fmt.Println()
	fmt.Printf("  fix: sudo apt-get install -y %s\n", strings.Join(missing, " "))
	if yes || confirm("  Run this now?", false) {
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
