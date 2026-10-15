package main

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// eugrRecipe is the subset of eugr's schema worth comparing against. The rest
// (build_args, container) describes their build pipeline; we consume the
// published image by digest, so for us those fields are advisory.
type eugrRecipe struct {
	Model    string            `yaml:"model"`
	Mods     []string          `yaml:"mods"`
	Defaults map[string]any    `yaml:"defaults"`
	Env      map[string]string `yaml:"env"`
	Command  string            `yaml:"command"`
}

func parseEugr(b []byte) (eugrRecipe, error) {
	var r eugrRecipe
	if err := yaml.Unmarshal(b, &r); err != nil {
		return eugrRecipe{}, fmt.Errorf("parse eugr recipe: %w", err)
	}
	if r.Model == "" {
		return eugrRecipe{}, fmt.Errorf("recipe has no model, which is the join key")
	}
	return r, nil
}

// expand substitutes {placeholder} from defaults and unescapes {{ }}, which is
// how the template carries literal JSON. An unknown placeholder is left as it
// was: this is a comparison, and reporting "{foo}" is more honest than
// inventing a value.
func (r eugrRecipe) expand(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); {
		switch {
		case strings.HasPrefix(s[i:], "{{"):
			out.WriteByte('{')
			i += 2
		case strings.HasPrefix(s[i:], "}}"):
			out.WriteByte('}')
			i += 2
		case s[i] == '{':
			end := strings.IndexByte(s[i:], '}')
			if end < 0 {
				out.WriteByte(s[i])
				i++
				continue
			}
			name := s[i+1 : i+end]
			if v, ok := r.Defaults[name]; ok {
				out.WriteString(scalar(v))
			} else {
				out.WriteString("{" + name + "}")
			}
			i += end + 1
		default:
			out.WriteByte(s[i])
			i++
		}
	}
	return out.String()
}

// scalar renders a YAML value the way it would appear on a command line.
func scalar(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	default:
		return fmt.Sprintf("%v", t)
	}
}

// Flags renders the command template into flag/value pairs. Values keep their
// quoting stripped so they compare against an argv entry.
func (r eugrRecipe) Flags() map[string]string {
	line := strings.ReplaceAll(r.expand(r.Command), "\\\n", " ")
	flags := map[string]string{}
	toks := shellSplit(line)
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		if !strings.HasPrefix(t, "--") {
			continue
		}
		if name, value, ok := strings.Cut(t, "="); ok {
			flags[name] = value
			continue
		}
		if i+1 < len(toks) && !strings.HasPrefix(toks[i+1], "--") {
			flags[t] = toks[i+1]
			i++
			continue
		}
		flags[t] = ""
	}
	return flags
}

// shellSplit splits on whitespace but keeps quoted runs together, which is all
// eugr's template needs: single quotes wrapping JSON.
func shellSplit(s string) []string {
	var toks []string
	var cur strings.Builder
	var quote byte
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				cur.WriteByte(c)
			}
		case c == '\'' || c == '"':
			quote = c
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return toks
}
