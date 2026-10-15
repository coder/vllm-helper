package recipe

import (
	"fmt"
	"strings"
)

// Target is the machine a recipe is resolved for. The hardware profile supplies
// these; the catalog never infers them.
type Target struct {
	Hardware   string // exact upstream id, e.g. "dgx_spark_gb10"
	Generation string // e.g. "blackwell"
	Arch       string // e.g. "sm121", checked against forbiddenArgs
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

// render turns a pair back into argv tokens, keeping --flag=value inline.
func (p argPair) render() []string {
	switch {
	case p.inline:
		return []string{p.flag + "=" + p.value}
	case p.hasValue:
		return []string{p.flag, p.value}
	default:
		return []string{p.flag}
	}
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
		out = append(out, p.render()...)
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
