package recipe

import (
	"strings"
	"testing"
)

func TestTestedRecipesAreWellFormed(t *testing.T) {
	tt, err := LoadTested()
	if err != nil {
		t.Fatalf("LoadTested: %v", err)
	}
	if len(tt.Recipes) == 0 {
		t.Fatal("no tested recipes shipped")
	}
	seen := map[string]bool{}
	for _, r := range tt.Recipes {
		if seen[r.ID] {
			t.Errorf("duplicate id %q", r.ID)
		}
		seen[r.ID] = true

		if r.DisplayName == "" {
			t.Errorf("%s: display_name is what users see, it cannot be empty", r.ID)
		}
		if r.Model == "" || r.Port == 0 || len(r.Args) == 0 {
			t.Errorf("%s: model, port and args are all required", r.ID)
		}
		switch r.Topology {
		case "solo", "shard", "pool":
		default:
			t.Errorf("%s: unknown topology %q", r.ID, r.Topology)
		}
		if r.Topology != "solo" && r.Nodes < 2 {
			t.Errorf("%s: topology %q needs nodes >= 2, got %d", r.ID, r.Topology, r.Nodes)
		}
		// A moving tag is not a pin: eugr's :latest advanced under us mid-session.
		if !strings.Contains(r.Image, "@sha256:") {
			t.Errorf("%s: image must be digest-pinned, got %q", r.ID, r.Image)
		}
		if r.Verified.Date == "" || r.Verified.Hardware == "" {
			t.Errorf("%s: a tested recipe must record when and where it was verified", r.ID)
		}
		if r.Verified.Smoke == "" {
			t.Errorf("%s: missing smoke-test evidence", r.ID)
		}
	}
}

// The tested args are what we actually launch, so they must satisfy the same
// arch invariant the resolver enforces.
func TestTestedRecipesHoldTheArchInvariant(t *testing.T) {
	tt, err := LoadTested()
	if err != nil {
		t.Fatalf("LoadTested: %v", err)
	}
	target := Target{Hardware: "dgx_spark_gb10", Generation: "blackwell", Arch: "sm121"}
	for _, r := range tt.Recipes {
		if !strings.Contains(r.Verified.Hardware, "spark") {
			continue
		}
		if err := checkForbidden(r.Args, target); err != nil {
			t.Errorf("%s: %v", r.ID, err)
		}
	}
}

func TestDeepSeekRecipeSummary(t *testing.T) {
	tt, err := LoadTested()
	if err != nil {
		t.Fatalf("LoadTested: %v", err)
	}
	r, ok := tt.Get("deepseek-v4-flash-2spark")
	if !ok {
		t.Fatal("deepseek-v4-flash-2spark missing")
	}
	if r.DisplayName != "DeepSeek V4 Flash" {
		t.Errorf("display name = %q", r.DisplayName)
	}
	got := r.Summary()
	for _, want := range []string{"2 nodes, sharded", "64K ctx", "3.0x conc"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q missing %q", got, want)
		}
	}
	// The image-scoped backend name that vllm-project gets wrong for this image.
	joined := strings.Join(r.Args, " ")
	if !strings.Contains(joined, "--attention-backend B12X") || strings.Contains(joined, "B12X_MLA_SPARSE") {
		t.Errorf("attention backend must be B12X, got: %s", joined)
	}
}
