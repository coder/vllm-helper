package catalog

import (
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
	"github.com/coder/vllm-helper/internal/vllm"
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

	best, ok := w.BestFit(84, true)
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
	if got, _ := w.BestFit(10, true); got.Label != "IQ2_XXS" {
		t.Errorf("BestFit(10) = %q, want IQ2_XXS: nothing better fits", got.Label)
	}
}

// A chosen quant has to reach vLLM's command line, and for GGUF that means
// two things at once: the selector on the model string, and a tokenizer to
// read the config from, since GGUF repos rarely ship one.
func TestPrepareProfileAppliesAQuantChoice(t *testing.T) {
	dirs := config.Dirs{Data: t.TempDir()}
	choice := &Choice{
		Variant:   hf.Variant{Name: "m-Q8_0.gguf", Label: "Q8_0", GGUF: true},
		BaseModel: "Qwen/Qwen3-32B",
	}
	p := PrepareProfile(dirs, "bartowski/Qwen_Qwen3-32B-GGUF", choice, vllm.Caps{})

	if want := "bartowski/Qwen_Qwen3-32B-GGUF:Q8_0"; p.Opts.Repo != want {
		t.Errorf("Repo = %q, want %q", p.Opts.Repo, want)
	}
	// A name of its own, or a second quant of the same repo could not exist
	// alongside the first.
	if want := "qwen_qwen3-32b-gguf-q8_0"; p.Opts.Name != want {
		t.Errorf("Name = %q, want %q", p.Opts.Name, want)
	}
	if !strings.Contains(p.Opts.ExtraArgs, "--tokenizer Qwen/Qwen3-32B") {
		t.Errorf("ExtraArgs = %q, want a --tokenizer: GGUF repos rarely ship a config", p.Opts.ExtraArgs)
	}

	// No choice means the repo's default, unchanged.
	plain := PrepareProfile(dirs, "Qwen/Qwen3-32B", nil, vllm.Caps{})
	if plain.Opts.Repo != "Qwen/Qwen3-32B" {
		t.Errorf("Repo = %q, want the bare repo when nothing was chosen", plain.Opts.Repo)
	}
	if strings.Contains(plain.Opts.ExtraArgs, "--tokenizer") {
		t.Errorf("ExtraArgs = %q, want no tokenizer for a normal repo", plain.Opts.ExtraArgs)
	}
}

// Case matters to publishers and not to people.
func TestFindVariantIsCaseInsensitive(t *testing.T) {
	all := []hf.Variant{{Label: "Q8_0"}, {Label: "UD-Q2_K_XL"}}
	for _, in := range []string{"Q8_0", "q8_0", "Q8_0"} {
		if v, ok := FindVariant(all, in); !ok || v.Label != "Q8_0" {
			t.Errorf("FindVariant(%q) missed", in)
		}
	}
	if v, ok := FindVariant(all, "ud-q2_k_xl"); !ok || v.Label != "UD-Q2_K_XL" {
		t.Errorf("FindVariant lower-cased miss: %v/%v", v.Label, ok)
	}
	if _, ok := FindVariant(all, "Q4_K_M"); ok {
		t.Error("FindVariant should not invent a quant the repo lacks")
	}
}

// What the tool adds and what the model's author prescribed are different
// claims, and merging them before reporting made both wrong: a repo whose
// README contains no serve snippet was told "Auto-detected from the model
// card: --tokenizer ...", naming a flag this tool had just injected, and the
// profile was stamped "flags auto-detected from README" on that basis. The
// user-visible result was the same tokenizer reported twice, the second time
// falsely.
func TestNotesDoNotCreditTheCardForOurOwnFlags(t *testing.T) {
	dirs := config.Dirs{Data: t.TempDir()}
	choice := &Choice{
		Variant:   hf.Variant{Name: "m-Q8_0.gguf", Label: "Q8_0", GGUF: true},
		BaseModel: "Qwen/Qwen3-Coder-30B-A3B-Instruct",
	}
	// A repo with no README snippet, which is the usual case for GGUF.
	p := PrepareProfile(dirs, "unsloth/Qwen3-Coder-30B-A3B-Instruct-GGUF", choice, vllm.Caps{})

	// The flag still has to reach vLLM -- the plugin cannot start without it.
	if !strings.Contains(p.Opts.ExtraArgs, "--tokenizer Qwen/Qwen3-Coder-30B-A3B-Instruct") {
		t.Errorf("ExtraArgs = %q, want the tokenizer we added", p.Opts.ExtraArgs)
	}

	var credited, mentions int
	for _, n := range p.Notes {
		if strings.HasPrefix(n, "Auto-detected from the model card:") {
			credited++
			if strings.Contains(n, "--tokenizer") {
				t.Errorf("note credits the card for a flag we added: %q", n)
			}
		}
		if strings.Contains(n, "--tokenizer") {
			mentions++
		}
	}
	if credited != 0 {
		t.Errorf("a card with no serve snippet produced %d auto-detected notes", credited)
	}
	if mentions != 1 {
		t.Errorf("the tokenizer is reported %d times, want exactly 1:\n  %s",
			mentions, strings.Join(p.Notes, "\n  "))
	}
	if strings.Contains(p.Opts.Status, "auto-detected from README") {
		t.Errorf("Status = %q, but the README prescribed nothing", p.Opts.Status)
	}
}
