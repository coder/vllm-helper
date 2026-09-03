package web

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// scriptsFor lists the scripts each page loads, in order. Kept beside the
// pages themselves so adding a page means adding it here too.
var scriptsFor = map[string][]string{
	"index.html":  {"common.js", "machine.js"},
	"models.html": {"common.js", "models.js"},
}

var (
	// A bare assignment statement: `busy = true;` but not `el.busy = 1`,
	// `a == b`, `x => y`, or `n >= 2`.
	bareAssign = regexp.MustCompile(`(?m)^\s*([A-Za-z_$][\w$]*)\s*=[^=>]`)
	// A SCREAMING_SNAKE reference, which by convention is a module-level
	// constant. Reads are checked as well as writes because a constant is
	// only ever read -- and losing one to a bad edit is exactly as fatal as
	// losing a variable, just invisible to an assignment-only check.
	constRef = regexp.MustCompile(`\b([A-Z][A-Z0-9_]{2,})\b`)
	// Anything that introduces a name into scope.
	declaration = regexp.MustCompile(`(?m)\b(?:let|const|var|function|class)\s+([A-Za-z_$][\w$]*)`)
	// Parameters and catch bindings, which are also declarations.
	params = regexp.MustCompile(`(?:function\s*[\w$]*\s*\(([^)]*)\)|catch\s*\(([^)]*)\)|\(([^)]*)\)\s*=>|([A-Za-z_$][\w$]*)\s*=>)`)
)

// Splitting one script into three moved a `let busy = false` out of the file
// that still assigned to it, and under "use strict" that is a ReferenceError
// the moment someone clicks Switch -- reported from the machine as "busy is
// not defined". Neither `node --check` nor any Go test caught it, because the
// syntax was fine and the bug only existed once the files were combined by a
// browser.
//
// This checks the combination the browser actually sees: for each page, every
// name assigned to must be declared somewhere in that page's own scripts. It
// is a heuristic and deliberately narrow -- bare assignment statements only --
// but that is exactly the shape of the bug it exists to prevent.
func TestNoPageAssignsToAnUndeclaredName(t *testing.T) {
	static := Static()

	// Globals a browser provides, which no script declares.
	provided := map[string]bool{
		"location": true, "document": true, "window": true, "history": true,
		"sessionStorage": true, "localStorage": true, "console": true,
		"fetch": true, "setTimeout": true, "clearTimeout": true,
		"setInterval": true, "clearInterval": true, "confirm": true, "alert": true,
		// SCREAMING_SNAKE tokens that are not constants of ours.
		"POST": true, "GET": true, "PUT": true, "DELETE": true, "UTF": true,
	}

	for page, scripts := range scriptsFor {
		declared := map[string]bool{}
		assigned := map[string]string{} // name -> script it was assigned in

		for _, name := range scripts {
			body, err := fs.ReadFile(static, name)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			src := string(body)

			for _, m := range declaration.FindAllStringSubmatch(src, -1) {
				declared[m[1]] = true
			}
			for _, m := range params.FindAllStringSubmatch(src, -1) {
				for _, group := range m[1:] {
					for _, p := range strings.Split(group, ",") {
						p = strings.TrimSpace(p)
						p = strings.TrimPrefix(p, "...")
						if i := strings.IndexAny(p, " =:{}[])"); i >= 0 {
							p = p[:i]
						}
						if p != "" {
							declared[p] = true
						}
					}
				}
			}
			for _, m := range bareAssign.FindAllStringSubmatch(src, -1) {
				if _, seen := assigned[m[1]]; !seen {
					assigned[m[1]] = name
				}
			}
			// Read from code only: a constant named in a comment is
			// documentation, and one that appears inside a string -- as
			// "X-VLLM-Helper-Token" does -- is not a reference to anything.
			for _, m := range constRef.FindAllStringSubmatch(codeOnly(src), -1) {
				if _, seen := assigned[m[1]]; !seen {
					assigned[m[1]] = name
				}
			}
		}

		for name, script := range assigned {
			if declared[name] || provided[name] {
				continue
			}
			t.Errorf("%s loads %s, which assigns to %q, but no script on that "+
				"page declares it -- under \"use strict\" that is a ReferenceError",
				page, script, name)
		}
	}
}

// codeOnly blanks out comments and string literals, so a name that merely
// appears in prose or inside a string is not counted as a use of it.
//
// Both matter here. A comment naming a constant is documentation, and
// "X-VLLM-Helper-Token" contains something that looks exactly like a
// SCREAMING_SNAKE constant without being a reference to one.
func codeOnly(src string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(src); i++ {
		c := src[i]
		switch {
		case quote != 0:
			// Inside a string: skip to its end, keeping newlines so line
			// structure survives for anything reading it afterwards.
			if c == '\\' {
				i++ // escaped character, whatever it is
				continue
			}
			if c == quote {
				quote = 0
			}
			if c == '\n' {
				b.WriteByte('\n')
			}
		case c == '"' || c == '\'' || c == '`':
			quote = c
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			for i+1 < len(src) && !(src[i] == '*' && src[i+1] == '/') {
				if src[i] == '\n' {
					b.WriteByte('\n')
				}
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}
