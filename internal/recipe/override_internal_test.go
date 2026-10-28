package recipe

import (
	"reflect"
	"strconv"
	"testing"
)

// The verified argv came out token-for-token identical to the hand-written
// launch script, so merging must be a no-op when there is nothing to merge.
func TestServeArgsUntouchedWithoutOverrides(t *testing.T) {
	r := deepseek(t)
	want := append(append([]string{}, r.Args...), "--host", "0.0.0.0", "--port", strconv.Itoa(r.Port))
	got := r.ServeArgs()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ServeArgs() reordered or dropped tokens: got %v, want %v", got, want)
	}
}

// An override has to replace the recipe's value in place. Appending a second
// copy of the flag is what lets the profile and the server disagree.
func TestServeArgsWithOverridesReplaceInPlace(t *testing.T) {
	r := deepseek(t)
	got, err := r.ServeArgsWith([]string{"--max-model-len", "131072", "--enforce-eager"})
	if err != nil {
		t.Fatalf("ServeArgsWith: %v", err)
	}
	if n := countFlag(got, "--max-model-len"); n != 1 {
		t.Fatalf("--max-model-len appears %d times: %v", n, got)
	}
	if v, _ := FlagValue(got, "--max-model-len"); v != "131072" {
		t.Errorf("override lost: --max-model-len = %q", v)
	}
	if v, _ := FlagValue(got, "--max-cudagraph-capture-size"); v != "64" {
		t.Errorf("unrelated recipe flag changed: %q", v)
	}
	if _, ok := FlagValue(got, "--enforce-eager"); !ok {
		t.Error("a flag the recipe does not set must still be added")
	}
	// Even a backend the recipe pins may be overridden; only the known-fatal
	// combinations in forbiddenArgs fail closed.
	back, err := r.ServeArgsWith([]string{"--attention-backend", "FLASH_ATTN"})
	if err != nil {
		t.Fatalf("ServeArgsWith: %v", err)
	}
	if v, _ := FlagValue(back, "--attention-backend"); v != "FLASH_ATTN" {
		t.Errorf("--attention-backend = %q, want the override", v)
	}
}

// The fatal combinations have to be refused on the merged argv, not only on
// the shipped args: an override is exactly how one would arrive.
func TestServeArgsWithRefusesAnOverrideTheHardwareCannotRun(t *testing.T) {
	r := deepseek(t)
	if _, err := r.ServeArgsWith([]string{"--moe-backend", "deep_gemm_mega_moe"}); err == nil {
		t.Fatal("an SM100-only kernel reached a GB10 launch")
	}
	if _, err := r.ServeArgsWith([]string{"--moe-backend=deep_gemm_mega_moe"}); err == nil {
		t.Fatal("the inline spelling of an SM100-only kernel reached a GB10 launch")
	}
}

// Last-wins has to be settled before restatements are dropped. Dropping the
// final "8" because the base also says 8 would let the earlier 16 win.
func TestStripRestatementsKeepsTheEffectiveValue(t *testing.T) {
	base := []string{"--max-num-seqs", "8", "--enforce-eager"}
	got := StripRestatements(base, []string{"--max-num-seqs", "16", "--max-num-seqs", "8"})
	if len(got) != 0 {
		t.Errorf("the effective value restates the base, so nothing overrides it; got %v", got)
	}
	merged := dedupeArgs(append(append([]string{}, base...), got...))
	if v, _ := FlagValue(merged, "--max-num-seqs"); v != "8" {
		t.Errorf("--max-num-seqs = %q, want 8", v)
	}
	got = StripRestatements(base, []string{"--max-num-seqs", "8", "--max-num-seqs", "16"})
	if !reflect.DeepEqual(got, []string{"--max-num-seqs", "16"}) {
		t.Errorf("a real final override was lost: %v", got)
	}
}

func TestOverrideFlagsNamesWhatMoved(t *testing.T) {
	got := OverrideFlags([]string{"--max-model-len", "8", "--enforce-eager", "--max-model-len", "9"})
	want := []string{"--enforce-eager", "--max-model-len"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OverrideFlags() = %v, want %v", got, want)
	}
	if len(OverrideFlags(nil)) != 0 {
		t.Error("no overrides must report no flags")
	}
}

func countFlag(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}
