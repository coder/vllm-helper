package catalog

import (
	"testing"

	"github.com/coder/vllm-helper/internal/hf"
)

// A repo id becomes both a URL sent to the Hub and, through NameFromRepo, part
// of a filename. The web panel takes it from a query string, so this is the
// check standing between a URL and the filesystem.
func TestValidRepo(t *testing.T) {
	valid := []string{
		"Qwen/Qwen3-0.6B",
		"nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16",
		"unsloth/gemma-3-27b-it-GGUF",
		"bartowski/Qwen_Qwen3-32B-GGUF",
		"mistralai/Mistral-7B-Instruct-v0.3",
		"a/b",
	}
	for _, r := range valid {
		if !ValidRepo(r) {
			t.Errorf("ValidRepo(%q) = false, want true: this is a real repo id", r)
		}
	}

	invalid := []struct{ repo, why string }{
		{"", "empty"},
		{"noslash", "a repo id has an owner"},
		{"a/b/c", "two slashes is not a repo id"},
		{"../../etc/passwd", "traversal"},
		{"owner/..", "would become a profile name of '..'"},
		{"owner/.", "would become a profile name of '.'"},
		{"owner/...", "dots only"},
		{"owner/na me", "space"},
		{"owner/na\x00me", "NUL"},
		{"owner/name\n", "newline"},
		{"/name", "no owner"},
		{"owner/", "no name"},
		{"owner/sub/name", "nested"},
		{"-owner/name", "leading dash"},
		{"owner/../name", "traversal in the middle"},
		{"https://evil.test/x", "a URL"},
	}
	for _, c := range invalid {
		if ValidRepo(c.repo) {
			t.Errorf("ValidRepo(%q) = true, want false (%s)", c.repo, c.why)
		}
	}
}

// Inspect has to answer two different questions from one measurement, and
// they have opposite answers: "could anything in this repo run here" wants the
// smallest, while "which one should I run" wants the biggest that fits. Fusing
// them is what made a 32B repo look like a 2-bit-only proposition.
func TestInspectSeparatesTheFitCheckFromTheChoice(t *testing.T) {
	// Stands in for bartowski/Qwen_Qwen3-32B-GGUF against an 84 GB budget.
	w := hf.Weights{
		SizeGB:   9.0,
		Variants: 3,
		All: []hf.Variant{
			{Name: "m-Q8_0.gguf", Label: "Q8_0", SizeGB: 34.8},
			{Name: "m-Q4_K_M.gguf", Label: "Q4_K_M", SizeGB: 19.8},
			{Name: "m-IQ2_XXS.gguf", Label: "IQ2_XXS", SizeGB: 9.0},
		},
	}

	best, ok := w.BestFit(84)
	if !ok || best.Label != "Q8_0" {
		t.Fatalf("BestFit(84) = %q/%v, want Q8_0", best.Label, ok)
	}
	// The conservative figure is unchanged, because the fit check still has
	// to ask whether anything here could run at all.
	if w.SizeGB != 9.0 {
		t.Errorf("SizeGB = %v, want the smallest kept for the fit check", w.SizeGB)
	}
	// And the choice is a strictly better model than the fit check's answer.
	if best.SizeGB <= w.SizeGB {
		t.Errorf("best %.1f GB is not larger than the smallest %.1f GB", best.SizeGB, w.SizeGB)
	}

	// A budget that only the worst quant clears must not pretend otherwise.
	if got, _ := w.BestFit(10); got.Label != "IQ2_XXS" {
		t.Errorf("BestFit(10) = %q, want IQ2_XXS: nothing better fits", got.Label)
	}
}
