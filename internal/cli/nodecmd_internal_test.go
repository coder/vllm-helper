package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/nodes"
)

// 'node ls' showed only the HTTP host, so a node whose SSH target was wrong,
// or silently derived from --host, looked fine until a launch failed on it.
func TestNodeListShowsTheSSHTargetUsed(t *testing.T) {
	t.Setenv("USER", "coder")
	var out bytes.Buffer
	printNodes(&out, []nodes.Node{
		{Name: "spark1", Host: "http://192.168.1.125:8001"},
		{Name: "spark2", Host: "http://192.168.1.234:8001", SSH: "coder@10.44.0.2"},
	})
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want a header and two nodes:\n%s", len(lines), out.String())
	}
	for i, want := range [][]string{
		{"NAME", "HOST", "SSH"},
		{"spark1", "http://192.168.1.125:8001", "coder@192.168.1.125", "(from --host)"},
		{"spark2", "http://192.168.1.234:8001", "coder@10.44.0.2"},
	} {
		if got := strings.Fields(lines[i]); strings.Join(got, " ") != strings.Join(want, " ") {
			t.Errorf("line %d = %q, want fields %q", i, lines[i], want)
		}
	}
}
