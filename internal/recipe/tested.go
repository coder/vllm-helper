package recipe

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed tested.json
var testedJSON []byte

// Tested holds the recipes we have actually run on our own hardware. It is kept
// separate from catalog.json because the two have different provenance and
// update paths: the catalog is generated from upstream, this is hand-authored
// from a real deployment and re-stamped by "recipe verify".
type Tested struct {
	Recipes []TestedRecipe `json:"recipes"`
}

// TestedRecipe is one launch configuration proven on real hardware. Image is
// pinned by digest, never a moving tag: eugr's :latest advanced under us mid-
// session, and a moved image is how a valid backend name becomes invalid.
type TestedRecipe struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"display_name"`
	Model       string            `json:"model"`
	Topology    string            `json:"topology"` // solo, shard or pool
	Nodes       int               `json:"nodes"`
	Image       string            `json:"image"`
	Port        int               `json:"port"`
	Source      Source            `json:"source"`
	Args        []string          `json:"args"`
	Env         map[string]string `json:"env,omitempty"`
	Verified    Verification      `json:"verified"`
}

// Source is the upstream recipe an entry was derived from, pinned to a commit.
// It is structured rather than a comment because JSON has no comments and, more
// importantly, "recipediff" needs it to know what to compare an entry against.
// It also carries the attribution MIT and Apache-2.0 upstreams require.
type Source struct {
	Repo    string `json:"repo"`
	Path    string `json:"path,omitempty"`
	Commit  string `json:"commit"`
	License string `json:"license,omitempty"`
}

// Describe renders the provenance for "recipes show".
func (s Source) Describe() string {
	if s.Repo == "" {
		return ""
	}
	out := s.Repo
	if s.Path != "" {
		out += " " + s.Path
	}
	if len(s.Commit) >= 8 {
		out += " @" + s.Commit[:8]
	}
	if s.License != "" {
		out += " (" + s.License + ")"
	}
	return out
}

// Verification is what separates our entries from an upstream claim: upstream
// says a model works somewhere, this says it ran here, on this date, at this
// image digest, with these numbers.
type Verification struct {
	Date        string   `json:"date"`
	Hardware    string   `json:"hardware"`
	Driver      string   `json:"driver,omitempty"`
	VLLMVersion string   `json:"vllm_version,omitempty"`
	LoadSeconds int      `json:"load_seconds,omitempty"`
	Context     int      `json:"context,omitempty"`
	KVTokens    int64    `json:"kv_tokens,omitempty"`
	Concurrency float64  `json:"concurrency,omitempty"`
	Smoke       string   `json:"smoke,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

// LoadTested returns the shipped tested recipes.
func LoadTested() (Tested, error) {
	var t Tested
	if err := json.Unmarshal(testedJSON, &t); err != nil {
		return Tested{}, fmt.Errorf("parse tested recipes: %w", err)
	}
	sort.Slice(t.Recipes, func(i, j int) bool { return t.Recipes[i].ID < t.Recipes[j].ID })
	return t, nil
}

// Get looks a recipe up by id.
func (t Tested) Get(id string) (TestedRecipe, bool) {
	for _, r := range t.Recipes {
		if r.ID == id {
			return r, true
		}
	}
	return TestedRecipe{}, false
}

// Summary is the one-line description shown next to the display name.
func (r TestedRecipe) Summary() string {
	out := r.nodeLabel()
	if r.Verified.Context > 0 {
		out += " - " + humanTokens(int64(r.Verified.Context)) + " ctx"
	}
	if r.Verified.Concurrency > 0 {
		out += fmt.Sprintf(" - %.1fx conc", r.Verified.Concurrency)
	}
	if r.Verified.LoadSeconds > 0 {
		out += fmt.Sprintf(" - ~%.1f min load", float64(r.Verified.LoadSeconds)/60)
	}
	return out
}

func (r TestedRecipe) nodeLabel() string {
	switch {
	case r.Nodes > 1 && r.Topology == "pool":
		return fmt.Sprintf("%d nodes, replicas", r.Nodes)
	case r.Nodes > 1:
		return fmt.Sprintf("%d nodes, sharded", r.Nodes)
	default:
		return "1 node"
	}
}

func humanTokens(n int64) string {
	switch {
	case n >= 1<<20 && n%(1<<20) == 0:
		return fmt.Sprintf("%dM", n/(1<<20))
	case n >= 1024 && n%1024 == 0:
		return fmt.Sprintf("%dK", n/1024)
	default:
		return fmt.Sprintf("%d", n)
	}
}
