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
	got := r.ServeArgsWith([]string{"--max-model-len", "131072", "--enforce-eager"})
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
	back := r.ServeArgsWith([]string{"--attention-backend", "FLASH_ATTN"})
	if v, _ := FlagValue(back, "--attention-backend"); v != "FLASH_ATTN" {
		t.Errorf("--attention-backend = %q, want the override", v)
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
