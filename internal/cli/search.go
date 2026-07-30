package cli

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/coder/vllm-helper/internal/hf"
)

// cmdSearch searches HuggingFace and walks the user through picking a
// model: a numbered result list, then a detail view with a live size check
// against this machine's budget, then pull-and-switch.
func cmdSearch(args []string) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: vllm-helper search <search terms>")
	}
	query := strings.Join(args, " ")

	fmt.Printf("Searching HuggingFace for %q...\n", query)
	models, err := hf.Search(query, 25)
	if err != nil {
		return fmt.Errorf("search request failed (network or rate limit): %w", err)
	}
	if len(models) == 0 {
		fmt.Println("No text-generation models found. Try different terms.")
		return nil
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
		fmt.Println(m.ID)
		fmt.Println("https://huggingface.co/" + m.ID)
		fmt.Printf("  license:    %s\n", m.License())
		fmt.Printf("  downloads:  %d   likes: %d   created: %.10s\n", m.Downloads, m.Likes, m.CreatedAt)
		if q := m.QuantTags(); len(q) > 0 {
			fmt.Printf("  quant:      %s\n", strings.Join(q, ", "))
		} else {
			fmt.Println("  quant:      none detected in tags -- check the model card")
		}

		budget, _ := strconv.ParseFloat(cfg.ModelBudgetGB(), 64)
		if size, err := hf.WeightsSizeGB(m.ID); err == nil && size > 0 {
			verdict := fmt.Sprintf("fits under the ~%.0fGB budget", budget)
			if size > budget {
				verdict = "TOO BIG for this machine -- look for a smaller quant"
			}
			fmt.Printf("  size:       %.1f GB  (%s)\n", size, verdict)
		} else {
			fmt.Println("  size:       unknown (no weight files at repo root -- check the model card)")
		}

		if flags, err := hf.DetectFlags(m.ID); err == nil && flags.ExtraArgs != "" {
			fmt.Printf("  vllm flags: %s (auto-detected, will be pre-filled)\n", flags.ExtraArgs)
		} else {
			fmt.Println("  vllm flags: no vllm serve snippet in README -- set flags by hand after pulling")
		}
		fmt.Println()

		if confirm(fmt.Sprintf("Pull %s and switch to it?", m.ID), false) {
			return cmdNew([]string{m.ID})
		}
		fmt.Println()
	}
}
