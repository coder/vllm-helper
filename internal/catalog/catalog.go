// Package catalog turns a HuggingFace repo into something this machine can
// serve: what a model would cost here, and the settings to serve it with.
//
// It sits under both the CLI and the web panel. Both need the same judgements
// -- whether a model fits, which flags the installed vLLM will actually accept
// -- and those judgements have a safe direction that is easy to lose track of.
// Keeping one copy is what stops the two interfaces from disagreeing about
// whether a 200 GB model fits in 84 GB.
package catalog

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
	"github.com/coder/vllm-helper/internal/profile"
	"github.com/coder/vllm-helper/internal/vllm"
)

// repoPattern is the shape of a HuggingFace repo id: one owner, one name.
//
// Worth being strict about, because the string becomes both a URL sent to the
// Hub and -- through profile.NameFromRepo -- part of a filename. The Hub
// itself allows only these characters, so nothing legitimate is turned away.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// ValidRepo reports whether repo is a well-formed HuggingFace repo id.
//
// The dot-only names are excluded separately: they satisfy the pattern but
// NameFromRepo would turn "owner/.." into a profile name of "..", and a
// filename assembled from that is not something to find out about later.
func ValidRepo(repo string) bool {
	if !repoPattern.MatchString(repo) {
		return false
	}
	name := repo[strings.IndexByte(repo, '/')+1:]
	return strings.Trim(name, ".") != ""
}

// Result is the machine-readable shape of one search hit.
type Result struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	License   string   `json:"license"`
	Downloads int64    `json:"downloads"`
	Likes     int64    `json:"likes"`
	CreatedAt string   `json:"created_at"`
	QuantTags []string `json:"quant_tags"`
}

// Detail is everything we can learn about serving one model here.
type Detail struct {
	Result
	SizeGB float64 `json:"size_gb"` // 0 = unknown
	// SizeError says why the size is missing when the Hub could not be read,
	// as opposed to a repo that simply has no servable weights.
	SizeError string `json:"size_error,omitempty"`
	// Variants is how many complete copies of the model the repo holds, so
	// 1 means SizeGB is the whole story and more means it is the smallest of
	// them. Absent when SizeGB is unknown, since then there was nothing to
	// count.
	Variants int `json:"variants,omitempty"`
	// All lists those copies, largest first, so the choice is visible rather
	// than made silently. SizeGB being the smallest is the right answer to
	// "could anything here run" and the wrong one to show as the headline,
	// since the smallest quant in a repo is also its worst.
	All []hf.Variant `json:"all_variants,omitempty"`
	// Best is the largest copy that fits the budget -- what someone would
	// choose by hand, and what the verdict should be about. Absent when
	// nothing fits or there is no budget to judge against.
	Best          *hf.Variant `json:"best_variant,omitempty"`
	BudgetGB      float64     `json:"budget_gb"`             // 0 = unknown (no config)
	FitsBudget    *bool       `json:"fits_budget,omitempty"` // absent when either side is unknown
	DetectedFlags string      `json:"detected_flags"`
	MaxModelLen   string      `json:"detected_max_model_len,omitempty"`
	// DroppedFlags explains anything the card asked for that the installed
	// vLLM won't take, so it's visible before pulling rather than as a
	// crash loop afterwards.
	DroppedFlags []string `json:"dropped_flags,omitempty"`
}

// ToResult converts a Hub model into a search hit.
func ToResult(m hf.Model) Result {
	return Result{
		ID:        m.ID,
		URL:       "https://huggingface.co/" + m.ID,
		License:   m.License(),
		Downloads: m.Downloads,
		Likes:     m.Likes,
		CreatedAt: m.CreatedAt,
		QuantTags: m.QuantTags(),
	}
}

// Inspect runs the live checks for one model: weight size against the given
// budget, and whether the card's flags survive this vLLM. A budgetGB of 0
// means no budget is configured.
//
// A model whose size could not be read keeps FitsBudget unset rather than
// guessing, so nothing downstream claims it fits.
func Inspect(m hf.Model, budgetGB float64, caps vllm.Caps) Detail {
	d := Detail{Result: ToResult(m), BudgetGB: budgetGB}
	// A failed measurement is not the same as a repo with nothing to measure:
	// the pagination guards report an error precisely so a short listing is
	// not mistaken for a small model, and swallowing it here would undo that.
	if w, err := hf.WeightsSize(m.ID); err != nil {
		d.SizeError = err.Error()
	} else if w.SizeGB > 0 {
		d.SizeGB = w.SizeGB
		d.Variants = w.Variants
		d.All = w.All
		if d.BudgetGB > 0 {
			fits := w.SizeGB <= d.BudgetGB
			d.FitsBudget = &fits
			// Which one to actually run is a separate question from
			// whether any of them can, and only worth answering when
			// there is more than one to choose between.
			if best, ok := w.BestFit(d.BudgetGB); ok && len(w.All) > 1 {
				d.Best = &best
			}
		}
	}
	if flags, err := hf.DetectFlags(m.ID); err == nil {
		// Validate here too: seeing that a card's parser won't work on this
		// machine is most useful before pulling tens of gigabytes.
		d.DetectedFlags, d.DroppedFlags = vllm.Check(flags.ExtraArgs, caps)
		d.MaxModelLen = flags.MaxModelLen
	}
	return d
}

// Plan is the settings a new profile should be created with, plus what was
// noticed while working them out. Notes are returned rather than printed so
// the CLI can put them on a terminal and the panel can put them in a browser.
type Plan struct {
	Opts  profile.CreateOpts
	Notes []string
}

// PrepareProfile works out how to serve a model this machine has never run:
// which flags its card prescribes, which of those the installed vLLM will
// take, and whether its chat template needs the local patch.
//
// Nothing here is fatal. Every check that fails degrades to a note, because
// the alternative -- refusing to save a profile because the Hub was slow --
// helps nobody.
func PrepareProfile(dirs config.Dirs, repo string, caps vllm.Caps) Plan {
	var p Plan
	name := profile.NameFromRepo(repo)

	detected, _ := hf.DetectFlags(repo)
	extraArgs, dropped := vllm.Check(detected.ExtraArgs, caps)
	p.Notes = append(p.Notes, dropped...)

	if patched, needsPatch, err := hf.DetectChatTemplatePatch(repo); err != nil {
		p.Notes = append(p.Notes, "Chat template check failed, continuing without it: "+err.Error())
	} else if needsPatch {
		tmplPath := filepath.Join(dirs.Data, "chat-templates", name+".jinja")
		switch {
		case os.MkdirAll(filepath.Dir(tmplPath), 0o755) != nil:
			p.Notes = append(p.Notes, "Couldn't create the chat-templates directory, continuing without the patch.")
		case os.WriteFile(tmplPath, []byte(patched), 0o644) != nil:
			p.Notes = append(p.Notes, "Couldn't write the patched template, continuing without it.")
		default:
			p.Notes = append(p.Notes,
				"This model's bundled template crashes if a system-role message isn't first in "+
					"the conversation (common with agent harnesses like Coder Agents). Patched a "+
					"local copy: "+tmplPath)
			extraArgs = strings.TrimSpace(extraArgs + " --chat-template " + tmplPath)
		}
	}

	p.Opts = profile.CreateOpts{
		Repo:        repo,
		MaxModelLen: detected.MaxModelLen,
		ExtraArgs:   extraArgs,
		Source:      "vllm-helper new",
	}
	if extraArgs != "" {
		p.Notes = append(p.Notes, "Auto-detected from the model card: "+extraArgs)
		p.Opts.Status = "NOT YET VERIFIED -- flags auto-detected from README"
	} else {
		p.Notes = append(p.Notes,
			"No vllm serve snippet found in the model's README -- check its HF card by hand "+
				"and edit the profile if it needs specific flags.")
		p.Opts.Status = "NOT YET VERIFIED -- no flags auto-detected, check the model card by hand"
	}
	return p
}
