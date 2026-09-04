package vllm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// cacheSchema is bumped whenever Caps gains a field. The version in the
// filename only invalidates the cache when vLLM changes, so without this an
// existing cache would keep answering for a field it was written before --
// and a probe that never reruns reads as a capability the machine lacks.
const cacheSchema = 2

// cachePath names the cache after the vLLM version, so upgrading vLLM
// invalidates it automatically -- which is the whole point, since a static
// list of parser names is exactly what goes stale under an upgrade.
func cachePath(cacheDir, version string) string {
	if cacheDir == "" || version == "" {
		return ""
	}
	// Versions are tame ("0.27.1", "0.28.0.dev0+g1234"), but they end up in
	// a filename, so keep it to characters that can't escape the directory.
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '.', r == '-', r == '_', r == '+':
			return r
		}
		return '_'
	}, version)
	return filepath.Join(cacheDir, fmt.Sprintf("vllm-caps-v%d-%s.json", cacheSchema, safe))
}

func readCache(cacheDir, version string) (Caps, bool) {
	path := cachePath(cacheDir, version)
	if path == "" {
		return Caps{}, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Caps{}, false
	}
	var caps Caps
	if json.Unmarshal(b, &caps) != nil || !caps.Known() {
		return Caps{}, false
	}
	return caps, true
}

func writeCache(cacheDir, version string, caps Caps) {
	path := cachePath(cacheDir, version)
	if path == "" {
		return
	}
	b, err := json.Marshal(caps)
	if err != nil {
		return
	}
	if os.MkdirAll(cacheDir, 0o755) != nil {
		return
	}
	// Best effort throughout: a cache miss costs seconds, a failure here
	// must not cost the command.
	tmp, err := os.CreateTemp(cacheDir, ".vllm-caps-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() != nil {
		return
	}
	_ = os.Rename(tmp.Name(), path)
}
