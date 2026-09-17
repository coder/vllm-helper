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
	local, err := recipeOverrides(cfg, r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	if local.profile == nil {
		t.Fatal("the existing profile should have been returned")
	}
	if len(local.args) != 0 {
		t.Errorf("expected no overrides, got %v", local.args)
	}
}

// An edited knob has to reach argv. Coder reads MAX_MODEL_LEN from this same
// profile, so an ignored edit advertises one context and serves another.
func TestRecipeOverridesCarryEditsIntoArgv(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL="+r.Model+"\nMAX_MODEL_LEN=131072\nEXTRA_ARGS=\"--max-num-seqs 16 --enforce-eager\"\nRECIPE="+r.ID+"\n")
	local, err := recipeOverrides(cfg, r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	args := r.ServeArgsWith(local.args)
	if v, _ := recipe.FlagValue(args, "--max-model-len"); v != "131072" {
		t.Errorf("--max-model-len = %q, want the profile value", v)
	}
	if v, _ := recipe.FlagValue(args, "--max-num-seqs"); v != "16" {
		t.Errorf("--max-num-seqs = %q, want the EXTRA_ARGS value", v)
	}
	if _, ok := recipe.FlagValue(args, "--enforce-eager"); !ok {
		t.Error("a flag only EXTRA_ARGS sets must be added")
	}
	if flags := recipe.OverrideFlags(local.args); len(flags) != 3 {
		t.Errorf("expected 3 overridden flags, got %v", flags)
	}
	// The EXTRA_ARGS written back keeps the real overrides and nothing else.
	if got := strings.Join(local.extra, " "); got != "--max-num-seqs 16 --enforce-eager" {
		t.Errorf("EXTRA_ARGS to write back = %q", got)
	}
}

// The first launcher wrote the recipe's whole flag list into EXTRA_ARGS. That
// profile is byte-for-byte the recipe, so it must not be reported as drift.
func TestRecipeOverridesIgnoreAVerbatimExtraArgsCopy(t *testing.T) {
	r := testRecipe(t)
	body := "MODEL=" + r.Model + "\nMAX_MODEL_LEN=262144\nGPU_MEMORY_UTILIZATION=0.85\nEXTRA_ARGS=" + strconv.Quote(strings.Join(r.Args, " ")) + "\nRECIPE=" + r.ID + "\n"
	local, err := recipeOverrides(writeProfile(t, body), r)
	if err != nil {
		t.Fatalf("recipeOverrides: %v", err)
	}
	if local.profile == nil {
		t.Fatal("profile not loaded")
	}
	if len(local.args) != 0 {
		t.Errorf("a verbatim copy of the recipe is not an override: %v", local.args)
	}
	// It also has to be pruned on write: left in place, those tokens would
	// override the recipe's own value the day the recipe changes.
	if len(local.extra) != 0 || !local.pruned {
		t.Errorf("expected the copy to be pruned, got extra=%v pruned=%v", local.extra, local.pruned)
	}
}

// No profile yet is the first launch, not an error.
func TestRecipeOverridesToleratesNoProfile(t *testing.T) {
	r := testRecipe(t)
	local, err := recipeOverrides(writeProfile(t, ""), r)
	if err != nil || local.profile != nil || local.args != nil {
		t.Fatalf("first launch should be clean: %+v err=%v", local, err)
	}
}

// Swapping MODEL means a different model, which the recipe's verified flags do
// not describe; that belongs in its own profile.
func TestRecipeOverridesRefuseADifferentModel(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL=other/model\nRECIPE="+r.ID+"\n")
	_, err := recipeOverrides(cfg, r)
	if err == nil || !strings.Contains(err.Error(), "other/model") {
		t.Fatalf("expected a refusal naming the model, got %v", err)
	}
}

// A supervisor restarts the cluster without knowing which model is current, so
// the id has to be derivable from the active profile. Keeping it in a separate
// unit file instead is a second source of truth that silently reverts a switch.
func TestActiveRecipeIDComesFromTheActiveProfile(t *testing.T) {
	r := testRecipe(t)
	cfg := writeProfile(t, "MODEL="+r.Model+"\nRECIPE="+r.ID+"\n")
	cfg.Dirs.State = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.Dirs.State, "active-profile"), []byte(r.ID+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := activeRecipeID(cfg)
	if err != nil {
		t.Fatalf("activeRecipeID: %v", err)
	}
	if got != r.ID {
		t.Errorf("resolved %q, want %q", got, r.ID)
	}
}

// Guessing a recipe when nothing is active would launch an arbitrary model.
func TestActiveRecipeIDRefusesToGuess(t *testing.T) {
	cfg := writeProfile(t, "")
	cfg.Dirs.State = t.TempDir()
	if _, err := activeRecipeID(cfg); err == nil {
		t.Fatal("with no active profile the id must be required, not guessed")
	}
}
