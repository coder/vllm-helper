package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
)

// GGUF left vLLM core in 0.27.1 and lives in an out-of-tree package. A stock
// install therefore cannot load a GGUF file at all -- the failure is not a bad
// flag or a missing tokenizer, it is that "gguf" is not a registered
// quantization method -- so it is worth naming precisely and offering to fix.
const ggufPlugin = "vllm-gguf-plugin"

// ggufMissingMessage explains the gap and how to close it, naming the
// environment rather than a generic pip so the package cannot land in the
// wrong interpreter.
func ggufMissingMessage(cfg *config.Config) string {
	return fmt.Sprintf(
		"this vLLM can't load GGUF: support moved out of vLLM core in 0.27.1 and needs %s.\n"+
			"Install it with 'vllm-helper install-gguf', or by hand:\n  %s -m pip install %s",
		ggufPlugin, pipFor(cfg), ggufPlugin)
}

// pipFor names the interpreter that owns the configured vLLM. Installing into
// whatever python is on PATH is the classic way to end up with the package
// present and still unavailable to the server.
func pipFor(cfg *config.Config) string {
	if cfg != nil {
		if py := pythonBesideVLLM(cfg.VLLMBin()); py != "" {
			return py
		}
	}
	return "<your-vllm-venv>/bin/python"
}

func pythonBesideVLLM(binPath string) string {
	if binPath == "" {
		return ""
	}
	dir := filepath.Dir(binPath)
	for _, name := range []string{"python3", "python"} {
		p := filepath.Join(dir, name)
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p
		}
	}
	return ""
}

func newInstallGGUFCmd() *cobra.Command {
	var yes bool
	c := &cobra.Command{
		Use:     "install-gguf",
		Short:   "Install the plugin that lets vLLM load GGUF models",
		GroupID: groupModels,
		Args:    cobra.NoArgs,
		RunE:    func(*cobra.Command, []string) error { return runInstallGGUF(yes) },
	}
	c.Flags().BoolVarP(&yes, "yes", "y", false, "install without asking")
	return c
}

func runInstallGGUF(yes bool) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	python := pythonBesideVLLM(cfg.VLLMBin())
	if python == "" {
		return fmt.Errorf("couldn't find the interpreter next to %s -- install by hand:\n  <your-venv>/bin/python -m pip install %s",
			cfg.VLLMBin(), ggufPlugin)
	}
	if caps := vllmCaps(cfg); caps.SupportsGGUF() {
		fmt.Println("This vLLM already loads GGUF. Nothing to do.")
		return nil
	}

	fmt.Println("GGUF support left vLLM core in 0.27.1 and now lives in a separate package.")
	fmt.Println()
	fmt.Printf("  %s -m pip install %s\n", python, ggufPlugin)
	fmt.Println()
	// Said before the prompt, not after: these are the things that would
	// otherwise be discovered as a failed model load half an hour later.
	fmt.Println("Worth knowing first:")
	fmt.Println("  - vLLM's own docs call GGUF support experimental and under-optimized.")
	fmt.Println("  - The plugin builds from source and needs a compiler and CUDA headers.")
	fmt.Println("  - Split-file and per-quant-subfolder GGUFs still won't load; only")
	fmt.Println("    single files at a repo's root work on released versions.")
	fmt.Println("  - A native quant (AWQ/GPTQ/FP8/NVFP4) is usually faster where one exists.")
	fmt.Println()
	if !yes && !confirm("Install it?", false) {
		fmt.Println("Left alone.")
		return nil
	}

	cmd := exec.Command(python, "-m", "pip", "install", ggufPlugin)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pip install failed: %w", err)
	}

	// The capability cache is keyed by vLLM's version, which the plugin does
	// not change, so it would keep reporting the old answer.
	forgetCaps(cfg)
	fmt.Println()
	if caps := vllmCaps(cfg); caps.SupportsGGUF() {
		fmt.Println("Done -- this vLLM can now load GGUF models.")
		return nil
	}
	return fmt.Errorf("installed, but vLLM still doesn't register 'gguf'.\n" +
		"The plugin may need a matching vLLM version, or the build may have failed above")
}

// forgetCaps drops the cached capability probe so the next read re-runs it.
func forgetCaps(cfg *config.Config) {
	matches, _ := filepath.Glob(filepath.Join(cfg.Dirs.State, "vllm-caps-*.json"))
	for _, m := range matches {
		_ = os.Remove(m)
	}
}
