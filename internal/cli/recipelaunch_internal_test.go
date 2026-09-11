package cli

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/recipe"
)

func testRecipe(t *testing.T) recipe.TestedRecipe {
	t.Helper()
	tt, err := recipe.LoadTested()
	if err != nil {
		t.Fatalf("LoadTested: %v", err)
	}
	r, ok := tt.Get("deepseek-v4-flash-2spark")
	if !ok {
		t.Fatal("deepseek-v4-flash-2spark missing")
	}
	return r
}

func writeProfile(t *testing.T, body string) *config.Config {
	t.Helper()
	dir := t.TempDir()
	profiles := filepath.Join(dir, "models")
	if err := os.MkdirAll(profiles, 0o755); err != nil {
		t.Fatal(err)
	}
	if body != "" {
		path := filepath.Join(profiles, "deepseek-v4-flash-2spark.env")
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Config{Dirs: config.Dirs{Config: dir, Profiles: profiles}}
}

// A profile that only restates the recipe is not an override: reporting one
// would downgrade the verified claim for every launch.
func TestRecipeOverridesIgnoreRestatements(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL="+r.Model+"\nMAX_MODEL_LEN=262144\nGPU_MEMORY_UTILIZATION=0.85\nKV_CACHE_MEMORY_BYTES=\nEXTRA_ARGS=\"\"\nRECIPE="+r.ID+"\n")
	over, p, err := recipeOverrides(cfg, r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	if p == nil {
		t.Fatal("the existing profile should have been returned")
	}
	if len(over) != 0 {
		t.Errorf("expected no overrides, got %v", over)
	}
}

// An edited knob has to reach argv. Coder reads MAX_MODEL_LEN from this same
// profile, so an ignored edit advertises one context and serves another.
func TestRecipeOverridesCarryEditsIntoArgv(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL="+r.Model+"\nMAX_MODEL_LEN=131072\nEXTRA_ARGS=\"--max-num-seqs 16 --enforce-eager\"\nRECIPE="+r.ID+"\n")
	over, _, err := recipeOverrides(cfg, r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	args := r.ServeArgsWith(over)
	if v, _ := recipe.FlagValue(args, "--max-model-len"); v != "131072" {
		t.Errorf("--max-model-len = %q, want the profile value", v)
	}
	if v, _ := recipe.FlagValue(args, "--max-num-seqs"); v != "16" {
		t.Errorf("--max-num-seqs = %q, want the EXTRA_ARGS value", v)
	}
	if _, ok := recipe.FlagValue(args, "--enforce-eager"); !ok {
		t.Error("a flag only EXTRA_ARGS sets must be added")
	}
	if flags := recipe.OverrideFlags(over); len(flags) != 3 {
		t.Errorf("expected 3 overridden flags, got %v", flags)
	}
}

// The first launcher wrote the recipe's whole flag list into EXTRA_ARGS. That
// profile is byte-for-byte the recipe, so it must not be reported as drift.
func TestRecipeOverridesIgnoreAVerbatimExtraArgsCopy(t *testing.T) {
	r := testRecipe(t)
	body := "MODEL=" + r.Model + "\nMAX_MODEL_LEN=262144\nGPU_MEMORY_UTILIZATION=0.85\nEXTRA_ARGS=" + strconv.Quote(strings.Join(r.Args, " ")) + "\nRECIPE=" + r.ID + "\n"
	over, p, err := recipeOverrides(writeProfile(t, body), r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	if p == nil {
		t.Fatal("profile not loaded")
	}
	if len(over) != 0 {
		t.Errorf("a verbatim copy of the recipe is not an override: %v", over)
	}
}

// No profile yet is the first launch, not an error.
func TestRecipeOverridesToleratesNoProfile(t *testing.T) {
	r := testRecipe(t)
	over, p, err := recipeOverrides(writeProfile(t, ""), r)
	if err != nil || p != nil || over != nil {
		t.Fatalf("first launch should be clean: over=%v p=%v err=%v", over, p, err)
	}
}

// Swapping MODEL means a different model, which the recipe's verified flags do
// not describe; that belongs in its own profile.
func TestRecipeOverridesRefuseADifferentModel(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL=other/model\nRECIPE="+r.ID+"\n")
	_, _, err := recipeOverrides(cfg, r)
	if err == nil || !strings.Contains(err.Error(), "other/model") {
		t.Fatalf("expected a refusal naming the model, got %v", err)
	}
}
