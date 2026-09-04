package hf

import "testing"

// Names taken from antirez/deepseek-v4-gguf, where every file is prefixed with
// the model and then names each per-tensor override it applies.
func TestQuantLabelDropsWhatTheRepoNameAlreadySays(t *testing.T) {
	const repo = "antirez/deepseek-v4-gguf"
	tests := []struct{ name, want string }{
		{"DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2.gguf",
			"Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2"},
		{"DeepSeek-V4-Pro-Q4K-Layers00-30.gguf", "Pro-Q4K-Layers00-30"},
	}
	for _, tt := range tests {
		if got := quantLabel(tt.name, repo); got != tt.want {
			t.Errorf("quantLabel(%q) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// The common case must not regress: a trailing precision marker is still the
// whole label, and is what vLLM is handed as repo:QUANT.
func TestQuantLabelStillPrefersATrailingQuant(t *testing.T) {
	for _, tt := range []struct{ name, repo, want string }{
		{"Qwen_Qwen3-32B-Q8_0.gguf", "bartowski/Qwen_Qwen3-32B-GGUF", "Q8_0"},
		{"UD-Q2_K_XL", "unsloth/DeepSeek-V3.1-GGUF", "UD-Q2_K_XL"},
		{"", "org/model", "default"},
	} {
		if got := quantLabel(tt.name, tt.repo); got != tt.want {
			t.Errorf("quantLabel(%q, %q) = %q, want %q", tt.name, tt.repo, got, tt.want)
		}
	}
}

// trimRepoWords must never consume the whole label: a variant with no name is
// not selectable and not distinguishable from its siblings.
func TestTrimRepoWordsLeavesSomethingBehind(t *testing.T) {
	if got := trimRepoWords("deepseek-v4-gguf", "deepseek-v4-gguf"); got != "gguf" && got != "" {
		t.Errorf("trimRepoWords consumed too much: %q", got)
	}
	if got := quantLabel("deepseek-v4-gguf.gguf", "antirez/deepseek-v4-gguf"); got == "" {
		t.Error("quantLabel returned an empty label")
	}
}

func TestShortLabelKeepsBothEnds(t *testing.T) {
	const long = "Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2-imatrix-0731"
	got := shortLabel(long)
	if len(got) > maxLabelWidth {
		t.Errorf("shortLabel = %q, %d chars, want at most %d", got, len(got), maxLabelWidth)
	}
	// The quant is at the front and the revision at the back; both are how
	// one of these is told from the next.
	if got[:5] != "Flash" || got[len(got)-4:] != "0731" {
		t.Errorf("shortLabel = %q, want it to keep both ends", got)
	}
	// Short labels are left alone -- most repos have short ones.
	if got := shortLabel("Q8_0"); got != "Q8_0" {
		t.Errorf("shortLabel(Q8_0) = %q, want it untouched", got)
	}
}

// Display is for reading; Label is what vLLM is given. Shortening the wrong
// one produces a selector no repo has.
func TestTheSelectorIsNeverTheShortenedForm(t *testing.T) {
	v := Variant{
		Name:    "DeepSeek-V4-Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2.gguf",
		Label:   "Flash-IQ2XXS-w2Q2K-AProjQ8-SExpQ8-OutQ8-chat-v2",
		Display: "Flash-IQ2XX...-chat-v2",
		GGUF:    true,
	}
	sel, ok := v.Selector()
	if !ok {
		t.Fatal("no selector for a GGUF variant")
	}
	if sel != ":"+v.Label {
		t.Errorf("Selector() = %q, want the full label", sel)
	}
}
