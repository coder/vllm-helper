// Package profile manages saved model profiles: small env files in
// ~/.config/vllm-helper/models/, one per model, holding the vllm serve
// settings that work for it.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

// Status returns the human "# Status: ..." line from the profile header.
func (p *Profile) Status() string {
	for _, line := range p.File.LeadingComments() {
		if rest, ok := strings.CutPrefix(line, "# Status:"); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// DisplayName derives a Coder-facing name from the model repo:
// "deepreinforce-ai/Ornith-1.0-35B-FP8" -> "Ornith 1.0 35B FP8".
func (p *Profile) DisplayName() string {
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
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return strings.ToLower(name)
}

// CreateOpts are the settings for a brand-new profile.
type CreateOpts struct {
	Repo           string
	MaxModelLen    string // default 131072
	MemUtilization string // default 0.85
	ExtraArgs      string
	Status         string // human note for the header
	Source         string // e.g. "vllm-helper new", "adopted from running server"
}

// Create writes a new profile file. Fails if it already exists.
func Create(dirs config.Dirs, opts CreateOpts) (*Profile, error) {
	name := NameFromRepo(opts.Repo)
	path := dirs.ProfileFile(name)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("profile %q already exists", name)
	}
	if opts.MaxModelLen == "" {
		opts.MaxModelLen = "131072"
	}
	if opts.MemUtilization == "" {
		opts.MemUtilization = config.DefaultMemUtilization
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	content := fmt.Sprintf(`# Status: %s (added via %s on %s)
MODEL=%s
MAX_MODEL_LEN=%s
GPU_MEMORY_UTILIZATION=%s
EXTRA_ARGS=%s
`, opts.Status, opts.Source, time.Now().Format("2006-01-02"),
		opts.Repo, opts.MaxModelLen, opts.MemUtilization, config.Quote(opts.ExtraArgs))
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return nil, err
	}
	return Load(dirs, name)
}

func Remove(dirs config.Dirs, name string) error {
	return os.Remove(dirs.ProfileFile(name))
}
