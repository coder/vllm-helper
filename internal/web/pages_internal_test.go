package web

import (
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// pages are the documents the panel serves, and the path each is reached by.
var pages = map[string]string{
	"/":       "index.html",
	"/models": "models.html",
}

// Each page must actually be served at its own path. The panel used to be one
// document that hid half of itself, which failed silently -- a `display` rule
// in the stylesheet quietly beat the `hidden` attribute, so the hidden half
// rendered anyway and the nav only scrolled. Separate paths make that
// impossible to get wrong unnoticed, and this checks they are wired up.
func TestEachPageIsServed(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(Static())

	for path := range pages {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set(tokenHeader, s.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}
		if !strings.Contains(rec.Body.String(), "<!doctype html>") {
			t.Errorf("GET %s did not return an HTML document", path)
		}
	}

	// And they are behind the guard like everything else.
	for path := range pages {
		req := httptest.NewRequest("GET", path, nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("unauthenticated GET %s = %d, want 401", path, rec.Code)
		}
	}
}

var assetRef = regexp.MustCompile(`(?:src|href)="(/[^"]+)"`)

// Every script and stylesheet a page asks for has to exist. Splitting one
// script into three left a page pointing at a file that had been deleted,
// which a browser reports only as a blank panel and a console error nobody
// sees on a machine they are not sitting at.
func TestPagesOnlyReferenceAssetsThatExist(t *testing.T) {
	static := Static()
	s := newTestServer(t)
	h := s.Handler(static)

	for path, file := range pages {
		body, err := fs.ReadFile(static, file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		refs := assetRef.FindAllStringSubmatch(string(body), -1)
		if len(refs) == 0 {
			t.Errorf("%s references no assets at all, which cannot be right", file)
		}
		for _, m := range refs {
			ref := m[1]
			// Links to other pages are checked by the test above; this is
			// about the files a page needs in order to work.
			if !strings.HasSuffix(ref, ".js") && !strings.HasSuffix(ref, ".css") {
				continue
			}
			req := httptest.NewRequest("GET", ref, nil)
			req.Header.Set(tokenHeader, s.token)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("%s (served at %s) references %s, which is %d",
					file, path, ref, rec.Code)
			}
			if n, _ := io.Copy(io.Discard, rec.Body); n == 0 {
				t.Errorf("%s references %s, which is empty", file, ref)
			}
		}
	}
}

// The nav has to reach every page, or a page exists that nobody can get to.
func TestEveryPageIsReachableFromTheNav(t *testing.T) {
	static := Static()
	for _, file := range pages {
		body, err := fs.ReadFile(static, file)
		if err != nil {
			t.Fatal(err)
		}
		for path := range pages {
			if !strings.Contains(string(body), `href="`+path+`"`) {
				t.Errorf("%s has no nav link to %s", file, path)
			}
		}
		// The current page marks itself, so you can tell where you are.
		if !strings.Contains(string(body), `aria-current="page"`) {
			t.Errorf("%s does not mark the current page in its nav", file)
		}
	}
}
