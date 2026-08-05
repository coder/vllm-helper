package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// SplitArgs splits an EXTRA_ARGS value into arguments for vllm serve.
//
// It is SplitWords plus one backward-compatibility rule. Profiles written
// before values were quoted store bare JSON, e.g.
//
//	EXTRA_ARGS="--default-chat-template-kwargs {\"thinking\":true}"
//
// which the env file already unquotes to {"thinking":true}. A second,
// POSIX-correct pass would then remove those inner quotes and hand vLLM
// {thinking:true}, which is not JSON -- so where shell parsing would turn a
// valid JSON argument into an invalid one, keep the literal text instead.
// Splitting is unambiguous in that case because such values contain no
// spaces; anything the shell regrouped is left to SplitWords.
func SplitArgs(s string) ([]string, error) {
	words, err := SplitWords(s)
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(s)
	if len(fields) != len(words) {
		return words, nil
	}
	for i := range words {
		if words[i] != fields[i] && isJSON(fields[i]) && !isJSON(words[i]) {
			words[i] = fields[i]
		}
	}
	return words, nil
}

// isJSON reports whether s is a JSON object or array. Bare scalars don't
// count: quote removal can't corrupt them.
func isJSON(s string) bool {
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return false
	}
	return json.Valid([]byte(s))
}

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
