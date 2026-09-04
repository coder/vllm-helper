package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/catalog"
	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/hf"
)

// cmdSearch searches HuggingFace and walks the user through picking a
// model: a numbered result list, then a detail view with a live size check
// against this machine's budget, then pull-and-switch. With --json it
// prints the results and exits.
func newSearchCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:     "search <search terms>",
		Short:   "Search HuggingFace and pick a model to pull and switch to",
		GroupID: groupModels,
		RunE:    func(_ *cobra.Command, args []string) error { return runSearch(args, jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print results as JSON and exit (no prompts)")
	return c
}

func runSearch(args []string, jsonOut bool) error {
	if len(args) == 0 {
		return errors.New("usage: vllm-helper search <search terms> [--json]")
	}
	query := strings.Join(args, " ")

	if !jsonOut {
		fmt.Printf("Searching HuggingFace for %q...\n", query)
	}
	models, err := hf.Search(query, 25)
	if err != nil {
		return fmt.Errorf("search request failed (network or rate limit): %w", err)
	}
	if jsonOut {
		out := make([]catalog.Result, 0, len(models))
		for _, m := range models {
			out = append(out, catalog.ToResult(m))
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

	// The picker needs a human on both ends; piped sessions get the list
	// (or --json) without prompts that would block forever.
	if !stdoutIsTTY() || !stdinIsTTY() {
		fmt.Println("(non-interactive session -- use --json for machine-readable results, or")
		fmt.Println("'vllm-helper new <org/model>' to pull one of these directly)")
		return nil
	}

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
		printModelDetail(inspectModel(m, cfg))
		fmt.Println()

		if confirm(fmt.Sprintf("Pull %s and switch to it?", m.ID), false) {
			return pullAndSwitch(cfg, m.ID, watchOpts{follow: stdoutIsTTY(), wait: true})
		}
		fmt.Println()
	}
}

// cmdInspect shows the detail view for one repo without the search loop:
// vllm-helper inspect <org/model> [--json]
func newInspectCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:     "inspect <org/model-repo>",
		Short:   "Show a model's license, weight size against this machine, and detected flags",
		GroupID: groupModels,
		Args:    cobra.MaximumNArgs(1),
		RunE:    func(_ *cobra.Command, args []string) error { return runInspect(args, jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print the details as JSON")
	return c
}

func runInspect(args []string, jsonOut bool) error {
	if len(args) < 1 {
		return errors.New("usage: vllm-helper inspect <org/model-repo> [--json]")
	}
	m, err := hf.Lookup(args[0])
	if err != nil {
		return err
	}
	// Config is optional here: without it the size check just has no budget.
	cfg, err := loadConfig()
	if err != nil && !errors.Is(err, config.ErrNotConfigured) {
		return err
	}
	d := inspectModel(m, cfg)
	if jsonOut {
		return printJSON(d)
	}
	printModelDetail(d)
	return nil
}

// inspectModel measures a model against this machine. cfg may be nil, which
// just means there is no budget to measure against.
func inspectModel(m hf.Model, cfg *config.Config) catalog.Detail {
	var budget float64
	if cfg != nil {
		budget, _ = strconv.ParseFloat(cfg.ModelBudgetGB(), 64)
	}
	return catalog.Inspect(m, budget, vllmCaps(cfg))
}

func printModelDetail(d catalog.Detail) {
	fmt.Println(d.ID)
	fmt.Println(d.URL)
	fmt.Printf("  license:    %s\n", d.License)
	fmt.Printf("  downloads:  %d   likes: %d   created: %.10s\n", d.Downloads, d.Likes, d.CreatedAt)
	if len(d.QuantTags) > 0 {
		fmt.Printf("  quant:      %s\n", strings.Join(d.QuantTags, ", "))
	} else {
		fmt.Println("  quant:      none detected in tags -- check the model card")
	}
	// Repos that ship several quants side by side get measured at their
	// smallest, so say so rather than letting the number look definitive.
	var of string
	if d.Variants > 1 {
		of = fmt.Sprintf(", smallest of %d quants in this repo", d.Variants)
	}
	switch {
	case d.SizeError != "":
		fmt.Printf("  size:       could not be measured: %s\n", d.SizeError)
	case d.SizeGB == 0:
		// Distinct from "we could not measure it": adapters and vision
		// projectors are weight files, they are just not a model you can
		// serve, and saying none were found would be the opposite of true.
		fmt.Println("  size:       unknown (no servable weights found -- an adapter or projector-only repo, or check the model card)")
	case d.FitsBudget == nil:
		fmt.Printf("  size:       %.1f GB  (no budget configured -- run 'vllm-helper setup'%s)\n", d.SizeGB, of)
	case *d.FitsBudget && d.Best != nil:
		// Lead with the one worth running rather than the one that merely
		// proves something here fits, which is the smallest and so also the
		// worst quant in the repo.
		fmt.Printf("  size:       %.1f GB at %s  (biggest of %d quants that fits the ~%.0fGB budget; smallest is %.1f GB)\n",
			d.Best.SizeGB, d.Best.Label, d.Variants, d.BudgetGB, d.SizeGB)
	case *d.FitsBudget:
		fmt.Printf("  size:       %.1f GB  (fits under the ~%.0fGB budget%s)\n", d.SizeGB, d.BudgetGB, of)
	case d.Variants > 1:
		// Every quant was measured and the smallest still doesn't fit, so
		// there is no smaller one to go and look for.
		fmt.Printf("  size:       %.1f GB  (TOO BIG for this machine -- and it is the smallest of %d quants here, so try a different repo)\n", d.SizeGB, d.Variants)
	default:
		fmt.Printf("  size:       %.1f GB  (TOO BIG for this machine -- look for a smaller quant%s)\n", d.SizeGB, of)
	}
	if d.DetectedFlags != "" {
		fmt.Printf("  vllm flags: %s (auto-detected, will be pre-filled)\n", d.DetectedFlags)
	} else {
		fmt.Println("  vllm flags: no vllm serve snippet in README -- set flags by hand after pulling")
	}
	for _, w := range d.DroppedFlags {
		fmt.Printf("  dropped:    %s\n", w)
	}
	printVariants(d)
}

// printVariants lists the other copies of a model in a repo.
//
// Worth the space because the size above is the smallest, which is the safe
// answer to "could anything here run" and simultaneously the worst thing to
// actually serve: bartowski/Qwen_Qwen3-32B-GGUF measures 9.0 GB at IQ2_XXS
// while its Q8_0 is 34.8 GB and fits an 84 GB budget nearly twice over. A
// reader who only sees the smallest will write the repo off, or run a 2-bit
// quant without ever knowing there was a choice.
func printVariants(d catalog.Detail) {
	if len(d.All) < 2 {
		return
	}
	fmt.Println()
	fmt.Printf("  %d quants in this repo:\n", len(d.All))
	for _, v := range d.All {
		mark := "   "
		switch {
		case d.Best != nil && v.Name == d.Best.Name:
			mark = "  >"
		case d.BudgetGB > 0 && v.SizeGB > d.BudgetGB:
			mark = "  x"
		}
		fmt.Printf("%s %-14s %6.1f GB\n", mark, v.Label, v.SizeGB)
	}
	if d.Best != nil {
		fmt.Printf("    > = biggest that fits your ~%.0fGB budget", d.BudgetGB)
		if d.BudgetGB > 0 {
			fmt.Print(";  x = too big")
		}
		fmt.Println()
	}
	// Saying which one gets served matters more than the list itself: a
	// profile records the repo, so what runs is whatever vLLM resolves --
	// not necessarily the one measured above.
	fmt.Println("    Pulling this repo does not pick one of these -- see issue #25.")
}
