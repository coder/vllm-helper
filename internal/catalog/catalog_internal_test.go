package catalog

import "testing"

// A repo id becomes both a URL sent to the Hub and, through NameFromRepo, part
// of a filename. The web panel takes it from a query string, so this is the
// check standing between a URL and the filesystem.
func TestValidRepo(t *testing.T) {
	valid := []string{
		"Qwen/Qwen3-0.6B",
		"nvidia/NVIDIA-Nemotron-3-Nano-30B-A3B-BF16",
		"unsloth/gemma-3-27b-it-GGUF",
		"bartowski/Qwen_Qwen3-32B-GGUF",
		"mistralai/Mistral-7B-Instruct-v0.3",
		"a/b",
	}
	for _, r := range valid {
		if !ValidRepo(r) {
			t.Errorf("ValidRepo(%q) = false, want true: this is a real repo id", r)
		}
	}

	invalid := []struct{ repo, why string }{
		{"", "empty"},
		{"noslash", "a repo id has an owner"},
		{"a/b/c", "two slashes is not a repo id"},
		{"../../etc/passwd", "traversal"},
		{"owner/..", "would become a profile name of '..'"},
		{"owner/.", "would become a profile name of '.'"},
		{"owner/...", "dots only"},
		{"owner/na me", "space"},
		{"owner/na\x00me", "NUL"},
		{"owner/name\n", "newline"},
		{"/name", "no owner"},
		{"owner/", "no name"},
		{"owner/sub/name", "nested"},
		{"-owner/name", "leading dash"},
		{"owner/../name", "traversal in the middle"},
		{"https://evil.test/x", "a URL"},
	}
	for _, c := range invalid {
		if ValidRepo(c.repo) {
			t.Errorf("ValidRepo(%q) = true, want false (%s)", c.repo, c.why)
		}
	}
}
