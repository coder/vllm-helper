package web

import (
	"regexp"
	"strings"
	"testing"
)

var (
	cssRule       = regexp.MustCompile(`(?s)\.([a-z-]+)\s*\{([^}]*)\}`)
	elementWithID = regexp.MustCompile(`(?s)<(\w+)([^>]*class="([^"]*)"[^>]*)>(.*?)</\w+>`)
)

// white-space is inherited, so a rule written for an element holding one text
// node also applies to a later variant of it that holds markup -- and there
// the source's own indentation renders as blank lines. That happened to
// .notice-note, whose four child elements came out separated by five blank
// lines, making a two-line message fill half the screen.
//
// The same shape as the [hidden] trap: a stylesheet rule quietly doing
// something to content that arrived after it was written.
func TestPreWrapIsNotInheritedByMarkup(t *testing.T) {
	css := readAsset(t, "static/app.css")

	// Classes that set pre-wrap, and classes that put it back.
	preWrap, resets := map[string]bool{}, map[string]bool{}
	for _, m := range cssRule.FindAllStringSubmatch(css, -1) {
		class, body := m[1], m[2]
		if strings.Contains(body, "white-space: pre-wrap") {
			preWrap[class] = true
		}
		if strings.Contains(body, "white-space: normal") {
			resets[class] = true
		}
	}
	if len(preWrap) == 0 {
		t.Skip("no pre-wrap rules to check")
	}

	for _, page := range pages {
		html := readAsset(t, "static/"+page)
		for _, m := range elementWithID.FindAllStringSubmatch(html, -1) {
			classes := strings.Fields(m[3])
			inner := m[4]
			if !strings.Contains(inner, "<") {
				continue // a text-only element is what pre-wrap is for
			}
			inherits, reset := false, false
			for _, c := range classes {
				inherits = inherits || preWrap[c]
				reset = reset || resets[c]
			}
			if inherits && !reset {
				t.Errorf("%s: <%s class=%q> holds markup and inherits white-space: pre-wrap, "+
					"so the source indentation between its children will render as blank lines",
					page, m[1], m[3])
			}
		}
	}
}

func readAsset(t *testing.T, name string) string {
	t.Helper()
	b, err := staticFiles.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
