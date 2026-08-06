package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleProfile = `# Status: TESTED AND WORKING (verified 2026-07-30)
# Do NOT remove quotes around EXTRA_ARGS.
MODEL=poolside/Laguna-S-2.1-NVFP4
MAX_MODEL_LEN=131072
GPU_MEMORY_UTILIZATION=0.85
EXTRA_ARGS="--enable-auto-tool-choice --tool-call-parser poolside_v1"
`

func TestParseEnv(t *testing.T) {
	f := parseEnv(sampleProfile)
	if got := f.Get("MODEL"); got != "poolside/Laguna-S-2.1-NVFP4" {
		t.Errorf("MODEL = %q", got)
	}
	// Quotes must be stripped on read...
	want := "--enable-auto-tool-choice --tool-call-parser poolside_v1"
	if got := f.Get("EXTRA_ARGS"); got != want {
		t.Errorf("EXTRA_ARGS = %q, want %q", got, want)
	}
	if f.Has("GPU_MEMORY") {
		t.Error("prefix should not match as a key")
	}
	comments := f.LeadingComments()
	if len(comments) != 2 || !strings.HasPrefix(comments[0], "# Status:") {
		t.Errorf("LeadingComments = %v", comments)
	}
}

func TestSetPreservesCommentsAndQuoting(t *testing.T) {
	f := parseEnv(sampleProfile)
	f.Set("MAX_MODEL_LEN", "262144")
	f.Set("NEW_KEY", "two words") // must be re-quoted for bash

	dir := t.TempDir()
	path := filepath.Join(dir, "out.env")
	if err := f.Write(path, 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := os.ReadFile(path)
	content := string(out)

	if !strings.Contains(content, "# Status: TESTED") {
		t.Error("comments were lost on write")
	}
	if !strings.Contains(content, "MAX_MODEL_LEN=262144") {
		t.Error("Set didn't update in place")
	}
	if !strings.Contains(content, `NEW_KEY="two words"`) {
		t.Errorf("multi-word value must be quoted to stay bash-sourceable, got:\n%s", content)
	}
	// Round-trip: re-parse what we wrote.
	if got := parseEnv(content).Get("NEW_KEY"); got != "two words" {
		t.Errorf("round-trip NEW_KEY = %q", got)
	}
}

func TestParseEnvEscapedQuotes(t *testing.T) {
	// Real-world case: JSON-valued flags are escaped for bash sourcing.
	f := parseEnv(`EXTRA_ARGS="--default-chat-template-kwargs {\"thinking\":true}"` + "\n")
	want := `--default-chat-template-kwargs {"thinking":true}`
	if got := f.Get("EXTRA_ARGS"); got != want {
		t.Errorf("EXTRA_ARGS = %q, want %q", got, want)
	}
}

func TestQuoteRoundTrip(t *testing.T) {
	for _, v := range []string{
		"plain",
		"two words",
		`--default-chat-template-kwargs {"thinking":true}`,
		`back\slash and "quote"`,
	} {
		f := parseEnv("K=" + Quote(v) + "\n")
		if got := f.Get("K"); got != v {
			t.Errorf("round-trip %q -> %q", v, got)
		}
	}
}

func TestDelete(t *testing.T) {
	f := parseEnv("A=1\nB=2\n")
	f.Delete("A")
	if f.Has("A") || !f.Has("B") {
		t.Errorf("delete broke: %v", f.Keys())
	}
}
