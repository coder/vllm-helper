// Package profile manages saved model profiles: small env files in
// ~/.config/vllm-helper/models/, one per model, holding the vllm serve
// settings that work for it.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/vllm-helper/internal/config"
)

type Profile struct {
	Name string
	File *config.EnvFile
}

func (p *Profile) Model() string { return p.File.Get("MODEL") }
func (p *Profile) MaxModelLen() string {
	return p.File.Get("MAX_MODEL_LEN")
}
func (p *Profile) ExtraArgs() string { return p.File.Get("EXTRA_ARGS") }

// RecipeKey names the recipe a profile was generated from. The recipe supplies
// the base flags; whatever this file says on top of them is a local override
// that "recipes launch" honours, so a recipe profile stays editable.
const RecipeKey = "RECIPE"

// Recipe returns the recipe this profile was generated from, or "".
func (p *Profile) Recipe() string { return strings.TrimSpace(p.File.Get(RecipeKey)) }

// Generated reports whether a recipe owns this profile.
func (p *Profile) Generated() bool { return p.Recipe() != "" }

// Status returns the human "# Status: ..." line from the profile header.
func (p *Profile) Status() string {
	for _, line := range p.File.LeadingComments() {
		if rest, ok := strings.CutPrefix(line, "# Status:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// DisplayName is the Coder-facing name. An explicit DISPLAY_NAME wins, because
// deriving from the repo cannot know that "DeepSeek-V4-Flash-0731" is published
// as "DeepSeek V4 Flash"; otherwise it is derived from the model repo:
// "deepreinforce-ai/Ornith-1.0-35B-FP8" -> "Ornith 1.0 35B FP8".
func (p *Profile) DisplayName() string {
	if explicit := strings.TrimSpace(p.File.Get("DISPLAY_NAME")); explicit != "" {
		return explicit
	}
	name := p.Model()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ReplaceAll(name, "-", " ")
}

func Load(dirs config.Dirs, name string) (*Profile, error) {
	f, err := config.ParseEnvFile(dirs.ProfileFile(name))
	if err != nil {
		return nil, fmt.Errorf("unknown profile %q (looked at %s)", name, dirs.ProfileFile(name))
	}
	return &Profile{Name: name, File: f}, nil
}

func List(dirs config.Dirs) ([]*Profile, error) {
	entries, err := os.ReadDir(dirs.Profiles)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []*Profile
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		name := strings.TrimSuffix(e.Name(), ".env")
		p, err := Load(dirs, name)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// NameFromRepo derives a profile name from a HuggingFace repo id:
// "Qwen/Qwen3.5-27B" -> "qwen3.5-27b".
func NameFromRepo(repo string) string {
	name := repo
	// A quant selector ("org/model-GGUF:Q8_0") is part of the model string
	// vLLM takes, not part of a filename.
	if i := strings.IndexByte(name, ':'); i >= 0 {
		name = name[:i]
	}
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// CreateOpts are the settings for a brand-new profile.
type CreateOpts struct {
	Repo string
	// Name overrides the profile name derived from Repo. Needed when Repo
	// carries a quant selector ("org/model-GGUF:Q8_0"), both because the
	// colon has no place in a filename and because two quants of one repo
	// have to be able to coexist as separate profiles.
	Name           string
	MaxModelLen    string // default DefaultMaxModelLen
	MemUtilization string // default 0.85
	// KVCacheBytes caps the KV cache instead of letting vLLM take whatever
	// is free. 0 leaves it to vLLM's own profiling.
	KVCacheBytes int64
	ExtraArgs    string
	Status       string // human note for the header
	Source       string // e.g. "vllm-helper new", "adopted from running server"
}

// Create writes a new profile file. Fails if it already exists.
func Create(dirs config.Dirs, opts CreateOpts) (*Profile, error) {
	name := opts.Name
	if name == "" {
		name = NameFromRepo(opts.Repo)
	}
	path := dirs.ProfileFile(name)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("profile %q already exists", name)
	}
	if opts.MaxModelLen == "" {
		opts.MaxModelLen = strconv.Itoa(DefaultMaxModelLen)
	}
	if opts.MemUtilization == "" {
		opts.MemUtilization = config.DefaultMemUtilization
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	// Written even when zero, so the key is visible to anyone editing the
	// profile by hand -- an absent knob is one nobody knows to reach for.
	content := fmt.Sprintf(`# Status: %s (added via %s on %s)
MODEL=%s
MAX_MODEL_LEN=%s
GPU_MEMORY_UTILIZATION=%s
KV_CACHE_MEMORY_BYTES=%s
EXTRA_ARGS=%s
`, opts.Status, opts.Source, time.Now().Format("2006-01-02"),
		opts.Repo, opts.MaxModelLen, opts.MemUtilization,
		kvCacheValue(opts.KVCacheBytes), config.Quote(opts.ExtraArgs))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return Load(dirs, name)
}

// DefaultMaxModelLen is the context a profile gets when the model card did not
// say. Long enough to be useful, short of the 1M-token configs that would make
// the KV cache the dominant cost on any machine.
const DefaultMaxModelLen = 131072

// kvCacheValue renders the cache size, empty when we are leaving it to vLLM.
func kvCacheValue(n int64) string {
	if n <= 0 {
		return ""
	}
	return strconv.FormatInt(n, 10)
}

func Remove(dirs config.Dirs, name string) error {
	return os.Remove(dirs.ProfileFile(name))
}
