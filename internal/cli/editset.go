package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/profile"
)

// standardProfileKeys are the keys every profile is expected to carry.
// Anything else is passed through into the server environment as-is.
var standardProfileKeys = map[string]bool{
	"MODEL":                  true,
	"MAX_MODEL_LEN":          true,
	"GPU_MEMORY_UTILIZATION": true,
	"EXTRA_ARGS":             true,
}

func newEditCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "edit <profile>",
		Short:   "Open a profile in $EDITOR and validate the result",
		GroupID: groupModels,
		Args:    cobra.MaximumNArgs(1),
		RunE:    func(_ *cobra.Command, args []string) error { return runEdit(args) },
	}
}

// runEdit opens a profile in $VISUAL/$EDITOR and validates the result.
func runEdit(args []string) error {
	if len(args) < 1 {
		return errors.New("usage: vllm-helper edit <profile>")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	if _, err := profile.Load(cfg.Dirs, name); err != nil {
		printProfiles(cfg)
		return err
	}
	editor := pickEditor()
	if editor == "" {
		return fmt.Errorf("no editor found -- set $EDITOR, or edit %s directly", cfg.Dirs.ProfileFile(name))
	}
	// $EDITOR may carry arguments ("code --wait", "emacs -nw").
	words, err := config.SplitWords(editor)
	if err != nil || len(words) == 0 {
		return fmt.Errorf("can't parse editor command %q", editor)
	}

	cmd := exec.Command(words[0], append(words[1:], cfg.Dirs.ProfileFile(name))...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("editor exited with an error: %w", err)
	}
	return afterProfileChange(cfg, name)
}

func pickEditor() string {
	for _, e := range []string{os.Getenv("VISUAL"), os.Getenv("EDITOR")} {
		if e != "" {
			return e
		}
	}
	for _, e := range []string{"nano", "vi"} {
		if _, err := exec.LookPath(e); err == nil {
			return e
		}
	}
	return ""
}

func newSetCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "set <profile> KEY=VALUE [KEY=VALUE ...]",
		Short:   "Change profile settings from the command line",
		GroupID: groupModels,
		Example: "  vllm-helper set qwen MAX_MODEL_LEN=40960\n" +
			"  vllm-helper set qwen -- EXTRA_ARGS=--enable-prefix-caching",
		RunE: func(_ *cobra.Command, args []string) error { return runSet(args) },
	}
}

// runSet updates profile keys in place: vllm-helper set <profile> KEY=VALUE...
func runSet(args []string) error {
	if len(args) < 2 {
		return errors.New("usage: vllm-helper set <profile> KEY=VALUE [KEY=VALUE ...]")
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	name := args[0]
	p, err := profile.Load(cfg.Dirs, name)
	if err != nil {
		printProfiles(cfg)
		return err
	}
	// Validate everything against an in-memory copy first: a bad pair
	// halfway through the list must not leave a half-applied profile on
	// disk, and a broken EXTRA_ARGS must not be written at all.
	type assignment struct{ key, val string }
	pairs := make([]assignment, 0, len(args)-1)
	for _, pair := range args[1:] {
		key, val, ok := strings.Cut(pair, "=")
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("%q is not KEY=VALUE (nothing was written)", pair)
		}
		pairs = append(pairs, assignment{key, val})
	}
	for _, a := range pairs {
		p.File.Set(a.key, a.val)
	}
	if err := validateProfile(p, name); err != nil {
		return fmt.Errorf("%w (nothing was written)", err)
	}

	if err := p.File.Write(cfg.Dirs.ProfileFile(name), 0o644); err != nil {
		return err
	}
	for _, a := range pairs {
		fmt.Printf("%s: %s=%s\n", name, a.key, a.val)
		if !standardProfileKeys[a.key] {
			fmt.Printf("  (note: %s isn't a standard profile key -- it will be exported into the\n", a.key)
			fmt.Println("  server environment as-is)")
		}
	}
	profileChangeHint(cfg, name)
	return nil
}

// afterProfileChange re-validates a profile that was written by something
// outside our control (an editor) and tells the user how the change takes
// effect.
func afterProfileChange(cfg *config.Config, name string) error {
	p, err := profile.Load(cfg.Dirs, name)
	if err != nil {
		return fmt.Errorf("the profile no longer parses: %w", err)
	}
	if err := validateProfile(p, name); err != nil {
		return err
	}
	profileChangeHint(cfg, name)
	return nil
}

// validateProfile reports whether a profile is servable as written.
func validateProfile(p *profile.Profile, name string) error {
	if p.Model() == "" {
		return fmt.Errorf("profile '%s' has no MODEL= line -- it can't be served like this", name)
	}
	if extra := p.ExtraArgs(); extra != "" {
		if _, err := config.SplitArgs(extra); err != nil {
			return fmt.Errorf("profile '%s' EXTRA_ARGS won't parse: %w", name, err)
		}
	}
	return nil
}

func profileChangeHint(cfg *config.Config, name string) {
	if name == cfg.ActiveProfile() {
		fmt.Printf("'%s' is the active profile -- run 'vllm-helper restart' to apply the change.\n", name)
	} else {
		fmt.Printf("Saved. The change applies the next time you switch to '%s'.\n", name)
	}
}
