// Command gendocs writes the CLI reference pages into the docs tree.
//
//	go run ./scripts/gendocs              # from the repo root
//	go generate ./...                     # same, via the hook in internal/cli
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/coder/vllm-helper/internal/cli"
)

func main() {
	dir := flag.String("dir", "docs", "docs root to write reference pages into")
	flag.Parse()

	pages, err := cli.GenerateCLIReference()
	if err != nil {
		fmt.Fprintln(os.Stderr, "gendocs:", err)
		os.Exit(1)
	}
	paths := make([]string, 0, len(pages))
	for p := range pages {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, rel := range paths {
		path := filepath.Join(*dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, "gendocs:", err)
			os.Exit(1)
		}
		if err := os.WriteFile(path, []byte(pages[rel]), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, "gendocs:", err)
			os.Exit(1)
		}
	}
	fmt.Printf("gendocs: wrote %d pages to %s\n", len(pages), *dir)
}
