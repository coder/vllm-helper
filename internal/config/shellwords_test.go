package config

import (
	"reflect"
	"testing"
)

func TestSplitWords(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"   ", nil},
		{"--enable-auto-tool-choice", []string{"--enable-auto-tool-choice"}},
		{"--a --b  --c", []string{"--a", "--b", "--c"}},
		// JSON-valued flags with spaces must survive as one argument.
		{`--override-generation-config '{"temperature": 0.7}'`,
			[]string{"--override-generation-config", `{"temperature": 0.7}`}},
		{`--default-chat-template-kwargs "{\"thinking\": true}"`,
			[]string{"--default-chat-template-kwargs", `{"thinking": true}`}},
		// Quotes glued to unquoted text form a single word.
		{`--x=a" b"c`, []string{`--x=a b`+"c"}},
		{`a\ b`, []string{"a b"}},
		{"--tool-call-parser qwen3_coder", []string{"--tool-call-parser", "qwen3_coder"}},
		{"\ttabs\nand newlines ", []string{"tabs", "and", "newlines"}},
	}
	for _, c := range cases {
		got, err := SplitWords(c.in)
		if err != nil {
			t.Errorf("SplitWords(%q) error: %v", c.in, err)
			continue
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("SplitWords(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}

func TestSplitWordsErrors(t *testing.T) {
	for _, in := range []string{`"unterminated`, `'unterminated`, `trailing\`} {
		if _, err := SplitWords(in); err == nil {
			t.Errorf("SplitWords(%q) should have failed", in)
		}
	}
}

// Round-trip: anything Quote produces, SplitWords must read back as the
// single original value.
func TestQuoteSplitRoundTrip(t *testing.T) {
	values := []string{
		"plain",
		"two words",
		`{"temperature": 0.7}`,
		`nested "quotes" inside`,
		`back\slash`,
	}
	for _, v := range values {
		got, err := SplitWords(Quote(v))
		if err != nil {
			t.Errorf("round trip %q: %v", v, err)
			continue
		}
		if len(got) != 1 || got[0] != v {
			t.Errorf("round trip %q = %#v", v, got)
		}
	}
}
