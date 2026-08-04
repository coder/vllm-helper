package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
)

// searchResult is the machine-readable shape of one search hit.
type searchResult struct {
	ID        string   `json:"id"`
	URL       string   `json:"url"`
	License   string   `json:"license"`
	Downloads int64    `json:"downloads"`
	Likes     int64    `json:"likes"`
	CreatedAt string   `json:"created_at"`
	QuantTags []string `json:"quant_tags"`
}

// modelDetail is the machine-readable shape of `inspect`: everything we
// can learn about serving one model on this machine.
type modelDetail struct {
	searchResult
	SizeGB        float64 `json:"size_gb"`               // 0 = unknown
	BudgetGB      float64 `json:"budget_gb"`             // 0 = unknown (no config)
	FitsBudget    *bool   `json:"fits_budget,omitempty"` // absent when either side is unknown
	DetectedFlags string  `json:"detected_flags"`
	MaxModelLen   string  `json:"detected_max_model_len,omitempty"`
}

// cmdSearch searches HuggingFace and walks the user through picking a
// model: a numbered result list, then a detail view with a live size check
// against this machine's budget, then pull-and-switch. With --json it
// prints the results and exits.
func cmdSearch(args []string) error {
	fs := newFlagSet("search")
	jsonOut := fs.Bool("json", false, "print results as JSON and exit (no prompts)")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) == 0 {
		return errors.New("usage: vllm-helper search <search terms> [--json]")
	}
	query := strings.Join(pos, " ")

	if !*jsonOut {
		fmt.Printf("Searching HuggingFace for %q...\n", query)
	}
	models, err := hf.Search(query, 25)
	if err != nil {
		return fmt.Errorf("search request failed (network or rate limit): %w", err)
	}
	if *jsonOut {
		out := make([]searchResult, 0, len(models))
		for _, m := range models {
			out = append(out, toSearchResult(m))
		}
		return printJSON(out)
	}
	if len(models) == 0 {
		fmt.Println("No text-generation models found. Try different terms.")
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	fmt.Println()
	for i, m := range models {
		quant := strings.Join(m.QuantTags(), ",")
		if quant != "" {
			quant = "  [" + quant + "]"
		}
		fmt.Printf("  %2d) %-52s %9d dl  %5d likes%s\n", i+1, m.ID, m.Downloads, m.Likes, quant)
	}
	fmt.Println()

	for {
		pick := ask(fmt.Sprintf("Pick a model to inspect (1-%d, Enter to cancel)", len(models)), "")
		if pick == "" {
			fmt.Println("Cancelled.")
			return nil
		}
		n, err := strconv.Atoi(pick)
		if err != nil || n < 1 || n > len(models) {
			fmt.Println("Not a valid number, try again.")
			continue
		}
		m := models[n-1]
		fmt.Println()
		printModelDetail(gatherModelDetail(m, cfg))
		fmt.Println()

		if confirm(fmt.Sprintf("Pull %s and switch to it?", m.ID), false) {
			return pullAndSwitch(cfg, m.ID, watchOpts{follow: stdoutIsTTY(), wait: true})
		}
		fmt.Println()
	}
}

// cmdInspect shows the detail view for one repo without the search loop:
// vllm-helper inspect <org/model> [--json]
func cmdInspect(args []string) error {
	fs := newFlagSet("inspect")
	jsonOut := fs.Bool("json", false, "print the details as JSON")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}
	if len(pos) < 1 {
		return errors.New("usage: vllm-helper inspect <org/model-repo> [--json]")
	}
	m, err := hf.Lookup(pos[0])
	if err != nil {
		return err
	}
	// Config is optional here: without it the size check just has no budget.
	cfg, err := loadConfig()
	if err != nil && !errors.Is(err, config.ErrNotConfigured) {
		return err
	}
	d := gatherModelDetail(m, cfg)
	if *jsonOut {
		return printJSON(d)
	}
	printModelDetail(d)
	return nil
}

func toSearchResult(m hf.Model) searchResult {
	return searchResult{
		ID:        m.ID,
		URL:       "https://huggingface.co/" + m.ID,
		License:   m.License(),
		Downloads: m.Downloads,
		Likes:     m.Likes,
		CreatedAt: m.CreatedAt,
		QuantTags: m.QuantTags(),
	}
}

// gatherModelDetail runs the live checks (weight size vs budget, flag
// detection) for one model. cfg may be nil.
func gatherModelDetail(m hf.Model, cfg *config.Config) modelDetail {
	d := modelDetail{searchResult: toSearchResult(m)}
	if cfg != nil {
		d.BudgetGB, _ = strconv.ParseFloat(cfg.ModelBudgetGB(), 64)
	}
	if size, err := hf.WeightsSizeGB(m.ID); err == nil && size > 0 {
		d.SizeGB = size
		if d.BudgetGB > 0 {
			fits := size <= d.BudgetGB
			d.FitsBudget = &fits
		}
	}
	if flags, err := hf.DetectFlags(m.ID); err == nil {
		d.DetectedFlags = flags.ExtraArgs
		d.MaxModelLen = flags.MaxModelLen
	}
	return d
}

func printModelDetail(d modelDetail) {
	fmt.Println(d.ID)
	fmt.Println(d.URL)
	fmt.Printf("  license:    %s\n", d.License)
	fmt.Printf("  downloads:  %d   likes: %d   created: %.10s\n", d.Downloads, d.Likes, d.CreatedAt)
	if len(d.QuantTags) > 0 {
		fmt.Printf("  quant:      %s\n", strings.Join(d.QuantTags, ", "))
	} else {
		fmt.Println("  quant:      none detected in tags -- check the model card")
	}
	switch {
	case d.SizeGB == 0:
		fmt.Println("  size:       unknown (no weight files at repo root -- check the model card)")
	case d.FitsBudget == nil:
		fmt.Printf("  size:       %.1f GB  (no budget configured -- run 'vllm-helper setup')\n", d.SizeGB)
	case *d.FitsBudget:
		fmt.Printf("  size:       %.1f GB  (fits under the ~%.0fGB budget)\n", d.SizeGB, d.BudgetGB)
	default:
		fmt.Printf("  size:       %.1f GB  (TOO BIG for this machine -- look for a smaller quant)\n", d.SizeGB)
	}
	if d.DetectedFlags != "" {
		fmt.Printf("  vllm flags: %s (auto-detected, will be pre-filled)\n", d.DetectedFlags)
	} else {
		fmt.Println("  vllm flags: no vllm serve snippet in README -- set flags by hand after pulling")
	}
}
