package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFiles embed.FS

// Static is the panel's assets, rooted so "/" serves index.html. They are
// compiled in: this tool ships as one binary people scp onto a machine, and
// a UI that needed files alongside it would break that.
func Static() fs.FS {
	sub, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // Impossible: the directory is embedded above.
	}
	return sub
}
