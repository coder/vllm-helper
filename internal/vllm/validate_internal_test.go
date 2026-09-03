package vllm

import (
	"encoding/json"
	"strings"
	"testing"
)

// Mirrors what vLLM 0.27.1 actually registers, trimmed to the names these
// tests reason about.
var caps0271 = Caps{
	Version:          "0.27.1",
	ToolParsers:      []string{"hermes", "qwen3_coder", "mistral", "deepseek_v3"},
	ReasoningParsers: []string{"deepseek_r1", "deepseek_v3", "nemotron_v3", "qwen3", "olmo3"},
	// Note the absence of --enable-reasoning: it was a real flag once and
	// vLLM 0.27.1 exits on it.
	Flags: []string{
		"--enable-auto-tool-choice", "--enable-prefix-caching", "--enforce-eager",
		"--max-num-seqs", "--reasoning-parser", "--reasoning-parser-plugin",
		"--seed", "--tool-call-parser", "--trust-remote-code", "--chat-template",
	},
}

// Carrying a card's flags over verbatim means carrying flags that a newer
// vLLM has dropped. Qwen3-8B-FP8's card still prescribes --enable-reasoning,
// which 0.27.1 rejects outright with "unrecognized arguments" -- before it
// looks at any other flag, so one stale name takes the whole server down.
func TestCheckDropsAFlagThatNoLongerExists(t *testing.T) {
	t.Parallel()
	got, warnings := Check("--enable-reasoning --reasoning-parser qwen3", caps0271)
	if strings.Contains(got, "--enable-reasoning") {
		t.Errorf("cleaned = %q, want the removed flag gone", got)
	}
	if !strings.Contains(got, "--reasoning-parser qwen3") {
		t.Errorf("cleaned = %q, want the valid flag kept", got)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "no such flag") {
		t.Errorf("warnings = %v, want one explaining the flag doesn't exist", warnings)
	}
}

// Flag checking has to be independently skippable: reading the argument
// parser can fail while the registries load fine, and vice versa.
func TestCheckSkipsFlagCheckWhenFlagsAreUnknown(t *testing.T) {
	t.Parallel()
	parsersOnly := Caps{Version: "x", ReasoningParsers: []string{"qwen3"}}
	in := "--some-flag-we-cannot-verify --reasoning-parser qwen3"
	got, warnings := Check(in, parsersOnly)
	if got != in || len(warnings) != 0 {
		t.Errorf("Check = (%q, %v), want the input untouched when the flag list is unavailable", got, warnings)
	}
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

// vLLM logs plugin registration to stdout, so the probe's JSON is not the only
// thing on the stream. Installing the GGUF plugin turned a working probe into
// a silent no-op: an empty Caps reads as "we could not ask", so flag
// validation went quiet, nothing was cached (making every call re-import
// torch, ~10s), and the plugin that had just been installed was reported as
// missing. Measured on a DGX Spark.
func TestExtractCapsIgnoresLogNoise(t *testing.T) {
	const payload = `{"quantizations":["gguf"]}`

	noisy := "INFO 09-03 09:36:00 [__init__.py:112] Registered model loader `gguf`\n" +
		"INFO 09-03 09:36:00 [config.py:459] Registered config parser `gguf`\n" +
		capsMarker + payload + "\n"

	got, ok := extractCaps([]byte(noisy))
	if !ok {
		t.Fatal("extractCaps found nothing in output that contains the marked line")
	}
	if string(got) != payload {
		t.Errorf("extractCaps = %q, want %q", got, payload)
	}

	// Clean output still works, and so does a trailing log line -- nothing
	// says the noise has to come first.
	for name, in := range map[string]string{
		"clean":        capsMarker + payload,
		"trailing log": capsMarker + payload + "\nINFO something afterwards",
		"crlf":         capsMarker + payload + "\r\n",
	} {
		if got, ok := extractCaps([]byte(in)); !ok || string(got) != payload {
			t.Errorf("%s: extractCaps = %q/%v, want the payload", name, got, ok)
		}
	}

	// And output with no marked line at all is a failure, not an empty
	// success -- the difference between "vLLM has no parsers" and "we could
	// not ask" decides whether validation runs.
	if _, ok := extractCaps([]byte("INFO only\nnothing useful here")); ok {
		t.Error("extractCaps claimed success on output with no marked line")
	}
}

// The GGUF plugin does not register a quantization method -- it hooks vLLM's
// loader and config-parser registries instead. Looking for it in
// QUANTIZATION_METHODS reported a freshly installed plugin as missing, which
// then hid every GGUF quant behind "needs the GGUF plugin" and dropped the
// best-fit recommendation. Measured on a DGX Spark that had just installed it.
func TestSupportsGGUFReadsThePluginNotTheQuantRegistry(t *testing.T) {
	// The registry lists a dozen native methods and never "gguf", even with
	// the plugin present.
	native := Caps{Quantizations: []string{"awq", "gptq", "fp8", "compressed-tensors"}}
	if native.SupportsGGUF() {
		t.Error("SupportsGGUF() = true from the quantization registry alone")
	}

	withPlugin := Caps{Quantizations: native.Quantizations, GGUF: true}
	if !withPlugin.SupportsGGUF() {
		t.Error("SupportsGGUF() = false with the plugin installed")
	}

	// And a machine we could not ask about must not read as "no plugin":
	// KnowsQuantizations is what separates the two, and callers gate on it.
	if (Caps{}).KnowsQuantizations() {
		t.Error("an empty Caps should not claim to know the quantization registry")
	}
	if !native.KnowsQuantizations() {
		t.Error("a populated registry should be reported as known")
	}
}

// The cheap probe is what makes every call fast: it reads distribution
// metadata rather than importing vLLM, so it must survive the same log noise
// the capability script does.
func TestExtractMarkedHandlesEitherScript(t *testing.T) {
	noisy := "INFO 09-03 [__init__.py:112] Registered model loader\n" +
		envMarker + `{"version":"0.27.1","gguf":true}` + "\n"
	got, ok := extractMarked([]byte(noisy), envMarker)
	if !ok {
		t.Fatal("extractMarked found nothing")
	}
	var e env
	if err := json.Unmarshal(got, &e); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if e.Version != "0.27.1" || !e.GGUF {
		t.Errorf("env = %+v, want version 0.27.1 and gguf true", e)
	}
	// The two markers must not match each other's lines.
	if _, ok := extractMarked([]byte(capsMarker+"{}"), envMarker); ok {
		t.Error("the env marker matched a capability line")
	}
}
