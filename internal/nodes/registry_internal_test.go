package nodes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The registry is a map, so anything read in iteration order reshuffled
// between runs: 'node ls' and nodes.json both came out in a different order
// each time. Both are sorted by name now.
func TestRegistryListsAndSavesInNameOrder(t *testing.T) {
	r := &Registry{path: filepath.Join(t.TempDir(), "nodes.json"), m: map[string]Node{}}
	for _, name := range []string{"spark3", "spark1", "spark2", "a-box"} {
		r.Add(Node{Name: name, Host: "http://" + name + ":8001"})
	}
	want := []string{"a-box", "spark1", "spark2", "spark3"}

	for range 5 {
		if got := names(r.List()); !slices.Equal(got, want) {
			t.Fatalf("List() = %v, want %v", got, want)
		}
	}

	if err := r.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(r.path)
	if err != nil {
		t.Fatal(err)
	}
	var saved []Node
	if err := json.Unmarshal(b, &saved); err != nil {
		t.Fatal(err)
	}
	if got := names(saved); !slices.Equal(got, want) {
		t.Errorf("nodes.json order = %v, want %v", got, want)
	}
}

func names(list []Node) []string {
	out := make([]string, len(list))
	for i, n := range list {
		out[i] = n.Name
	}
	return out
}
