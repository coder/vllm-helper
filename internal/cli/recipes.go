package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/coder/vllm-helper/internal/recipe"
)

func newRecipesCmd() *cobra.Command {
	c := &cobra.Command{
		Use:     "recipes",
		Short:   "Tested launch configurations for known-good models",
		GroupID: groupModels,
	}
	c.AddCommand(newRecipesListCmd(), newRecipesShowCmd())
	return c
}

func newRecipesListCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List tested recipes",
		Args:  cobra.NoArgs,
		RunE:  func(*cobra.Command, []string) error { return runRecipesList(jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print recipes as JSON")
	return c
}

func newRecipesShowCmd() *cobra.Command {
	var jsonOut bool
	c := &cobra.Command{
		Use:   "show <id>",
		Short: "Show everything a tested recipe pins, including how it was verified",
		Args:  cobra.ExactArgs(1),
		RunE:  func(_ *cobra.Command, a []string) error { return runRecipesShow(a[0], jsonOut) },
	}
	c.Flags().BoolVar(&jsonOut, "json", false, "print the recipe as JSON")
	return c
}

func runRecipesList(jsonOut bool) error {
	t, err := recipe.LoadTested()
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(t.Recipes)
	}
	if len(t.Recipes) == 0 {
		fmt.Println("No tested recipes shipped in this build.")
		return nil
	}
	width := 0
	for _, r := range t.Recipes {
		if len(r.ID) > width {
			width = len(r.ID)
		}
	}
	fmt.Println("TESTED -- run on this hardware, pinned by image digest")
	for _, r := range t.Recipes {
		fmt.Printf("  %-*s  %-22s  %s", width, r.ID, r.DisplayName, r.Summary())
		fmt.Println()
	}
	fmt.Println()
	fmt.Println("Run 'vllm-helper recipes show <id>' for the pinned image, flags and evidence.")
	return nil
}

func runRecipesShow(id string, jsonOut bool) error {
	t, err := recipe.LoadTested()
	if err != nil {
		return err
	}
	r, ok := t.Get(id)
	if !ok {
		ids := make([]string, 0, len(t.Recipes))
		for _, x := range t.Recipes {
			ids = append(ids, x.ID)
		}
		return fmt.Errorf("no tested recipe %q (have: %s)", id, strings.Join(ids, ", "))
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(r)
	}

	line := func(k, v string) {
		if v != "" {
			fmt.Printf("  %-14s %s", k, v)
			fmt.Println()
		}
	}
	fmt.Println(r.DisplayName + "  (" + r.ID + ")")
	fmt.Println()
	line("model", r.Model)
	line("layout", r.Summary())
	line("image", r.Image)
	line("port", fmt.Sprintf("%d", r.Port))
	fmt.Println()
	fmt.Println("Verified")
	v := r.Verified
	line("date", v.Date)
	line("hardware", v.Hardware)
	line("driver", v.Driver)
	line("vllm", v.VLLMVersion)
	if v.KVTokens > 0 {
		line("kv cache", fmt.Sprintf("%d tokens", v.KVTokens))
	}
	line("smoke test", v.Smoke)
	if len(v.Notes) > 0 {
		fmt.Println()
		fmt.Println("Notes")
		for _, n := range v.Notes {
			fmt.Println("  - " + n)
		}
	}
	fmt.Println()
	fmt.Println("Flags")
	fmt.Println("  " + strings.Join(r.Args, " "))
	if len(r.Env) > 0 {
		fmt.Println()
		fmt.Printf("Environment (%d vars; IFACE_NAME, IB_IF and NODE_IP are filled in per node)", len(r.Env))
		fmt.Println()
	}
	return nil
}
