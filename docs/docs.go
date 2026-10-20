// Package docs holds the documentation the web panel serves under /docs.
//
// The content is Markdown under this directory, wired to routes by
// manifest.json -- the same schema coder/coder uses for coder.com/docs -- and
// embedded into the binary. It has to be embedded: the binary is scp'd onto
// machines, and a docs directory that had to ride along would break that.
package docs

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"strings"
)

//go:embed manifest.json *.md reference
var files embed.FS

// FS returns the embedded documentation: manifest.json, every page it routes
// to, and the generated CLI reference under reference/cli/.
func FS() fs.FS { return files }

// Route is one entry of manifest.json, mirroring coder/coder's schema. Path
// is relative to this directory and written with a leading "./"; children
// nest the same shape.
type Route struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Path        string  `json:"path"`
	IconPath    string  `json:"icon_path,omitempty"`
	State       string  `json:"state,omitempty"`
	Children    []Route `json:"children,omitempty"`
}

// Manifest is the parsed manifest.json.
type Manifest struct {
	Versions []string `json:"versions"`
	Routes   []Route  `json:"routes"`
}

// ReadManifest parses manifest.json out of fsys.
func ReadManifest(fsys fs.FS) (*Manifest, error) {
	b, err := fs.ReadFile(fsys, "manifest.json")
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("parsing manifest.json: %w", err)
	}
	if len(m.Routes) == 0 {
		return nil, fmt.Errorf("manifest.json routes nothing")
	}
	for i := range m.Routes {
		if err := checkRoute(&m.Routes[i]); err != nil {
			return nil, err
		}
	}
	return &m, nil
}

func checkRoute(r *Route) error {
	if r.Title == "" {
		return fmt.Errorf("manifest route %q has no title", r.Path)
	}
	if r.Path == "" {
		return fmt.Errorf("manifest route %q has no path", r.Title)
	}
	for i := range r.Children {
		if err := checkRoute(&r.Children[i]); err != nil {
			return err
		}
	}
	return nil
}

// RouteFor maps a page path to the panel route that serves it. Paths named
// README.md or index.md take their directory's route, so the introduction
// lives at /docs and a section landing page at /docs/<section>.
//
// It is also the link rule for page prose: any relative ".md" link resolves
// against the current page's directory, then maps through here, which keeps
// links in the Markdown valid on GitHub and in the panel alike.
func RouteFor(mdPath string) string {
	p := path.Clean(strings.TrimPrefix(mdPath, "./"))
	p = strings.TrimSuffix(p, ".md")
	if base := path.Base(p); base == "README" || base == "index" {
		p = path.Dir(p)
	}
	p = strings.Trim(p, "/")
	if p == "." || p == "" {
		return "/docs"
	}
	return "/docs/" + p
}
