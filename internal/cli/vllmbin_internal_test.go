package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// /opt/homebrew/bin/vllm resolves into a versioned Cellar directory that the
// next 'brew upgrade' deletes. The opt link tracks the installed version.
func TestStableVLLMPathKeepsHomebrewsOptLink(t *testing.T) {
	t.Parallel()

	prefix := t.TempDir()
	keg := filepath.Join(prefix, "Cellar", "vllm-metal", "0.32.0")
	envBin := filepath.Join(keg, "libexec", "bin")
	if err := os.MkdirAll(envBin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(envBin, "vllm"), nil, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{filepath.Join(prefix, "bin"), filepath.Join(prefix, "opt")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "Cellar", "vllm-metal", "0.32.0"), filepath.Join(prefix, "opt", "vllm-metal")); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(prefix, "bin", "vllm")
	if err := os.Symlink(filepath.Join("..", "Cellar", "vllm-metal", "0.32.0", "libexec", "bin", "vllm"), link); err != nil {
		t.Fatal(err)
	}

	got := stableVLLMPath(link)
	realPrefix, err := filepath.EvalSymlinks(prefix)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(realPrefix, "opt", "vllm-metal", "libexec", "bin", "vllm")
	if got != want {
		t.Errorf("stableVLLMPath(%s) = %s, want %s", link, got, want)
	}
}

// Anything outside a Homebrew keg resolves to its real path, as setup always
// did: a venv's bin/vllm is usually reached through a symlink on PATH.
func TestStableVLLMPathResolvesOtherSymlinks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	target := filepath.Join(dir, "venv", "bin", "vllm")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "vllm")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := stableVLLMPath(link); got != want {
		t.Errorf("stableVLLMPath(%s) = %s, want %s", link, got, want)
	}
}
