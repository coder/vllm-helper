package config

import (
	"fmt"
	"strings"
)

// SplitWords splits a string into words the way a POSIX shell would:
// unquoted whitespace separates words, single quotes are literal, double
// quotes allow \" and \\ escapes, and a backslash outside quotes escapes
// the next character. This is the counterpart of Quote and is what
// EXTRA_ARGS goes through before being handed to vllm serve -- unlike
// strings.Fields, it keeps quoted values with spaces intact (e.g.
// --override-generation-config '{"temperature": 0.7}').
func SplitWords(s string) ([]string, error) {
	var (
		words   []string
		cur     strings.Builder
		inWord  bool
		quote   rune // 0, '\'' or '"'
		escaped bool
	)
	flush := func() {
		if inWord {
			words = append(words, cur.String())
			cur.Reset()
			inWord = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case quote == '"':
			switch r {
			case '"':
				quote = 0
			case '\\':
				escaped = true
			default:
				cur.WriteRune(r)
			}
		case r == '\\':
			escaped = true
			inWord = true
		case r == '\'' || r == '"':
			quote = r
			inWord = true
		case r == ' ' || r == '\t' || r == '\n':
			flush()
		default:
			cur.WriteRune(r)
			inWord = true
		}
	}
	if escaped {
		return nil, fmt.Errorf("trailing backslash in %q", s)
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %c quote in %q", quote, s)
	}
	flush()
	return words, nil
}
