package recipe

import (
	"fmt"
	"sort"
	"strings"
)

// Target is the machine a recipe is resolved for. The hardware profile supplies
// these; the catalog never infers them.
type Target struct {
	Hardware   string // exact upstream id, e.g. "dgx_spark_gb10"
	Generation string // e.g. "blackwell"
	Arch       string // e.g. "sm121", checked against forbiddenArgs
}

// Resolved is a launch configuration ready to be turned into a command.
type Resolved struct {
	ModelID       string
	Image         string
	Args          []string
	Env           map[string]string
	MinVLLM       string
	Strategy      string
	Variant       string
	VRAMMinimumGB float64
	Verified      bool
	PipPath       bool
	Notes         []string
}

// forbiddenArgs are flags that abort at startup on a given arch. Resolution
// fails closed if the merge ever emits one: both entries are SM100-only kernels
// that the recipe's generic Blackwell overrides would otherwise leak onto GB10.
var forbiddenArgs = map[string][]struct{ Flag, Value string }{
	"sm121": {
		{"--moe-backend", "deep_gemm_mega_moe"},
		{"--attention_config.use_fp4_indexer_cache", "True"},
	},
}

// Resolve merges a recipe into a launch configuration.
//
// Layering, lowest precedence first: base args, opt-in features and their
// selected modes, hardware overrides, then strategy args. Hardware overrides do
// NOT accumulate across levels: for each hardware key the most specific level
// that defines it wins outright (strategy > variant > recipe), because upstream
// relies on a strategy-level override replacing the generation-level one rather
// than layering on top of it. Remaining duplicate flags are last-wins.
func Resolve(r Recipe, variant string, strategy string, features []string, t Target) (Resolved, error) {
	v, ok := r.Variants[variant]
	if !ok {
		return Resolved{}, fmt.Errorf("recipe %s has no variant %q", r.Slug, variant)
	}
	if len(v.SupportedHardware) > 0 && !contains(v.SupportedHardware, t.Hardware) {
		return Resolved{}, fmt.Errorf("variant %q of %s does not support %s", variant, r.Slug, t.Hardware)
	}
	if !r.StrategySupported(strategy, t) {
		return Resolved{}, fmt.Errorf("%s does not support strategy %q on %s", r.Slug, strategy, t.Hardware)
	}

	out := Resolved{
		ModelID:       firstNonEmpty(v.ModelID, r.ModelID),
		Image:         firstNonEmpty(v.DockerImage, r.DockerImage),
		MinVLLM:       firstNonEmpty(v.MinVLLM, r.MinVLLM),
		Strategy:      strategy,
		Variant:       variant,
		VRAMMinimumGB: v.VRAMMinimumGB,
		Verified:      r.Hardware[t.Hardware] == "verified",
		PipPath:       true,
		Env:           map[string]string{},
	}
	if note, ok := r.HardwareNotes[t.Hardware]; ok {
		out.Notes = append(out.Notes, note)
	}

	args := append([]string(nil), r.BaseArgs...)
	args = append(args, featureArgs(r, v, features, t, &out)...)

	// Hardware overrides, generation before exact so the exact id refines it.
	for _, key := range []string{t.Generation, t.Hardware} {
		if key == "" {
			continue
		}
		if ov, ok := mostSpecificOverride(r, v, strategy, key); ok {
			args = append(args, applyOverride(ov, &out)...)
		}
	}

	so := r.StrategyOverrides[strategy]
	args = append(args, so.ExtraArgs...)

	out.Args = dedupeArgs(args)
	if err := checkForbidden(out.Args, t); err != nil {
		return Resolved{}, err
	}
	return out, nil
}

// StrategySupported reports whether a strategy may be used on this hardware.
// strategy_hardware can both grant a strategy that is absent from
// compatible_strategies and revoke one that is present.
func (r Recipe) StrategySupported(strategy string, t Target) bool {
	hw := r.StrategyHardware[strategy]
	for _, key := range []string{t.Generation, t.Hardware} {
		switch hw[key] {
		case "unsupported":
			return false
		case "supported":
			return true
		}
	}
	return contains(r.CompatibleStrategies, strategy)
}

func featureArgs(r Recipe, v Variant, enabled []string, t Target, out *Resolved) []string {
	var args []string
	names := append([]string(nil), enabled...)
	sort.Strings(names)
	for _, name := range names {
		f, ok := r.Features[name]
		if !ok {
			continue
		}
		args = append(args, f.Args...)
		if len(f.Modes) == 0 {
			continue
		}
		mode := firstNonEmpty(v.DefaultModes[name], f.DefaultMode)
		m, ok := f.Modes[mode]
		if !ok {
			continue
		}
		if unsupportedFor(m.Hardware, t) {
			continue
		}
		if len(m.Variants) > 0 && !contains(m.Variants, out.Variant) {
			continue
		}
		args = append(args, m.Args...)
		for _, key := range []string{t.Generation, t.Hardware} {
			if ov, ok := m.HardwareOverrides[key]; ok {
				args = append(args, applyOverride(ov, out)...)
			}
		}
	}
	return args
}

func unsupportedFor(m map[string]string, t Target) bool {
	return m[t.Hardware] == "unsupported" || m[t.Generation] == "unsupported"
}

// mostSpecificOverride returns the override for one hardware key from the most
// specific level that defines it. Levels below are discarded, not merged.
func mostSpecificOverride(r Recipe, v Variant, strategy, key string) (Override, bool) {
	if ov, ok := r.StrategyOverrides[strategy].HardwareOverrides[key]; ok {
		return ov, true
	}
	if ov, ok := v.HardwareOverrides[key]; ok {
		return ov, true
	}
	ov, ok := r.HardwareOverrides[key]
	return ov, ok
}

func applyOverride(ov Override, out *Resolved) []string {
	if ov.DockerImage != "" {
		out.Image = ov.DockerImage
	}
	if ov.NoPip {
		out.PipPath = false
	}
	for k, val := range ov.ExtraEnv {
		out.Env[k] = val
	}
	return ov.ExtraArgs
}

type argPair struct {
	flag, value string
	hasValue    bool
	inline      bool // written as --flag=value, which must round-trip
}

// splitArgs groups a flat arg list into flags and their values. Values are
// either inline (--flag=v) or the following token.
func splitArgs(in []string) []argPair {
	var out []argPair
	for i := 0; i < len(in); i++ {
		tok := in[i]
		if !strings.HasPrefix(tok, "-") {
			out = append(out, argPair{flag: tok})
			continue
		}
		if k, val, ok := strings.Cut(tok, "="); ok {
			out = append(out, argPair{flag: k, value: val, hasValue: true, inline: true})
			continue
		}
		if i+1 < len(in) && !strings.HasPrefix(in[i+1], "--") {
			out = append(out, argPair{flag: tok, value: in[i+1], hasValue: true})
			i++
			continue
		}
		out = append(out, argPair{flag: tok})
	}
	return out
}

// dedupeArgs keeps the last occurrence of each flag, at its last position.
func dedupeArgs(in []string) []string {
	pairs := splitArgs(in)
	last := make(map[string]int, len(pairs))
	for i, p := range pairs {
		last[p.flag] = i
	}
	var out []string
	for i, p := range pairs {
		if last[p.flag] != i {
			continue
		}
		switch {
		case p.inline:
			out = append(out, p.flag+"="+p.value)
		case p.hasValue:
			out = append(out, p.flag, p.value)
		default:
			out = append(out, p.flag)
		}
	}
	return out
}

func checkForbidden(args []string, t Target) error {
	bad := forbiddenArgs[t.Arch]
	if len(bad) == 0 {
		return nil
	}
	for _, p := range splitArgs(args) {
		for _, f := range bad {
			if p.flag == f.Flag && p.value == f.Value {
				return fmt.Errorf("resolved args contain %s %s, which is unsupported on %s", f.Flag, f.Value, t.Arch)
			}
		}
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
