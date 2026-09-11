package profile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/profile"
)

func loadFrom(t *testing.T, body string) *profile.Profile {
	t.Helper()
	path := filepath.Join(t.TempDir(), "p.env")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := config.ParseEnvFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return &profile.Profile{Name: "p", File: f}
}

// Generated is what makes a profile the override layer of a recipe, and what
// sends start/switch to "recipes launch", so it must not be fooled by whitespace.
func TestGeneratedTracksTheRecipeKey(t *testing.T) {
	cases := []struct {
		name, body, wantRecipe string
		wantGenerated          bool
	}{
		{"hand written", "MODEL=org/m\n", "", false},
		{"empty key", "MODEL=org/m\nRECIPE=\n", "", false},
		{"whitespace only", "MODEL=org/m\nRECIPE=\"   \"\n", "", false},
		{"generated", "MODEL=org/m\nRECIPE=deepseek-v4-flash-2spark\n", "deepseek-v4-flash-2spark", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := loadFrom(t, c.body)
			if got := p.Generated(); got != c.wantGenerated {
				t.Errorf("Generated() = %v, want %v", got, c.wantGenerated)
			}
			if got := p.Recipe(); got != c.wantRecipe {
				t.Errorf("Recipe() = %q, want %q", got, c.wantRecipe)
			}
		})
	}
}

// A recipe publishes its own label; deriving from the repo would render
// "DeepSeek-V4-Flash-0731" as "DeepSeek V4 Flash 0731".
func TestDisplayNamePrefersTheExplicitKey(t *testing.T) {
	derived := loadFrom(t, "MODEL=deepseek-ai/DeepSeek-V4-Flash-0731\n")
	if got := derived.DisplayName(); got != "DeepSeek V4 Flash 0731" {
		t.Errorf("derived DisplayName() = %q", got)
	}
	explicit := loadFrom(t, "MODEL=deepseek-ai/DeepSeek-V4-Flash-0731\nDISPLAY_NAME=\"DeepSeek V4 Flash\"\n")
	if got := explicit.DisplayName(); got != "DeepSeek V4 Flash" {
		t.Errorf("explicit DisplayName() = %q", got)
	}
}
