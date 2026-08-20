package vllm

import (
	"strings"
	"testing"
)

// Mirrors what vLLM 0.27.1 actually registers, trimmed to the names these
// tests reason about.
var caps0271 = Caps{
	Version:          "0.27.1",
	ToolParsers:      []string{"hermes", "qwen3_coder", "mistral", "deepseek_v3"},
	ReasoningParsers: []string{"deepseek_r1", "deepseek_v3", "nemotron_v3", "qwen3", "olmo3"},
}

// The failure that started this: a card prescribes a parser supplied by a
// plugin file, we copy the name and leave the file behind, and vLLM exits
// with KeyError two seconds in -- forever, because RestartSec=5 never trips
// systemd's start limit.
func TestCheckDropsAPluginParser(t *testing.T) {
	t.Parallel()
	in := `--trust-remote-code --tool-call-parser qwen3_coder ` +
		`--reasoning-parser-plugin nano_v3_reasoning_parser.py --reasoning-parser nano_v3`

	got, warnings := Check(in, caps0271)

	if strings.Contains(got, "nano_v3") {
		t.Errorf("cleaned = %q, want no trace of the unavailable parser", got)
	}
	if !strings.Contains(got, "--trust-remote-code") || !strings.Contains(got, "--tool-call-parser qwen3_coder") {
		t.Errorf("cleaned = %q, want the valid flags kept", got)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %v, want one for the parser and one for the plugin file", warnings)
	}
	joined := strings.Join(warnings, "\n")
	// The plugin explains *why* the name is unknown; saying so beats a bare
	// "not found".
	if !strings.Contains(joined, "nano_v3_reasoning_parser.py") {
		t.Errorf("warnings should name the plugin file: %v", warnings)
	}
	// Suggest, never substitute.
	if !strings.Contains(joined, "nemotron_v3") {
		t.Errorf("warnings should point at the registered neighbour: %v", warnings)
	}
	if !strings.Contains(joined, "not applied automatically") {
		t.Errorf("warnings should say the suggestion wasn't applied: %v", warnings)
	}
}

func TestCheckKeepsValidFlags(t *testing.T) {
	t.Parallel()
	in := `--enable-auto-tool-choice --tool-call-parser hermes --reasoning-parser qwen3 --max-num-seqs 8`
	got, warnings := Check(in, caps0271)
	if got != in {
		t.Errorf("cleaned = %q, want it unchanged", got)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %v, want none", warnings)
	}
}

// Not being able to ask must never look like "nothing is valid" -- that would
// strip every flag on a machine where introspection failed.
func TestCheckWithoutCapsChangesNothing(t *testing.T) {
	t.Parallel()
	in := `--reasoning-parser something_exotic --tool-call-parser also_unknown`
	got, warnings := Check(in, Caps{})
	if got != in || len(warnings) != 0 {
		t.Errorf("Check with unknown caps = (%q, %v), want the input untouched", got, warnings)
	}
}

func TestNearest(t *testing.T) {
	t.Parallel()
	tests := []struct{ unknown, want string }{
		// Shares the version-ish tail, which is the useful signal.
		{"nano_v3", "nemotron_v3"},
		// No shared tail: better to say nothing than to point somewhere wrong.
		{"totally_unrelated", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := nearest(tt.unknown, caps0271.ReasoningParsers); got != tt.want {
			t.Errorf("nearest(%q) = %q, want %q", tt.unknown, got, tt.want)
		}
	}
	// The one that matters most: names one character apart are different
	// parsers, so a suggestion must never read as an equivalence.
	if got := nearest("deepseek_r2", caps0271.ReasoningParsers); got == "deepseek_r1" {
		t.Error("deepseek_r2 must not resolve to deepseek_r1: they are not interchangeable")
	}
}

func TestCapsKnown(t *testing.T) {
	t.Parallel()
	if (Caps{}).Known() {
		t.Error("an empty Caps must not claim to know the registry")
	}
	if !caps0271.Known() {
		t.Error("a populated Caps should be Known")
	}
}
