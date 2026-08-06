package config

import (
	"strings"
	"testing"
)

// FuzzQuoteRoundTrip: anything Quote writes, parseEnv must read back
// unchanged. Newlines are excluded; one KEY=VALUE line can't hold one.
func FuzzQuoteRoundTrip(f *testing.F) {
	for _, seed := range []string{
		"",
		"plain",
		"two words",
		`--default-chat-template-kwargs {"thinking":true}`,
		`back\slash`,
		`"leading quote`,
		`trailing quote"`,
		`'single'`,
		`mixed "double" and 'single'`,
		`\\`,
		`\"`,
		"\ttab",
		"  padded  ",
		"=equals=",
		"#hash",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		if strings.ContainsAny(value, "\n\r") {
			t.Skip()
		}
		got := parseEnv("K=" + Quote(value) + "\n").Get("K")
		if got != value {
			t.Fatalf("round-trip corrupted the value\n  in:    %q\n  quoted: %q\n  out:   %q",
				value, Quote(value), got)
		}
	})
}

// FuzzParseEnvNeverPanics: profiles are hand-edited, so a malformed one must
// not crash the CLI.
func FuzzParseEnvNeverPanics(f *testing.F) {
	f.Add("MODEL=x\n")
	f.Add("# comment only\n")
	f.Add("=novalue\n")
	f.Add("no equals sign\n")
	f.Add("\x00\x01\x02")
	f.Add(strings.Repeat("A=1\n", 100))

	f.Fuzz(func(t *testing.T, content string) {
		env := parseEnv(content)
		for _, k := range env.Keys() {
			if !env.Has(k) {
				t.Fatalf("Keys() reported %q but Has() denies it", k)
			}
		}
	})
}
