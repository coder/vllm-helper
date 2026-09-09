// Package recipe resolves an upstream vLLM recipe into a launch configuration
// for one model, hardware target and parallelism strategy.
package recipe

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"sort"
)

//go:embed catalog.json
var catalogJSON []byte

// Index is the vendored catalog, pinned to an upstream commit so a launch is
// reproducible offline and an upstream edit cannot silently change a profile.
type Index struct {
	Source  string   `json:"source"`
	Commit  string   `json:"commit"`
	Recipes []Recipe `json:"recipes"`
}

type Recipe struct {
	Slug                 string                       `json:"slug"`
	ModelID              string                       `json:"model_id"`
	MinVLLM              string                       `json:"min_vllm_version,omitempty"`
	DockerImage          string                       `json:"docker_image,omitempty"`
	BaseArgs             []string                     `json:"base_args,omitempty"`
	Hardware             map[string]string            `json:"hardware,omitempty"`
	Features             map[string]Feature           `json:"features,omitempty"`
	OptInFeatures        []string                     `json:"opt_in_features,omitempty"`
	Variants             map[string]Variant           `json:"variants,omitempty"`
	CompatibleStrategies []string                     `json:"compatible_strategies,omitempty"`
	DefaultStrategy      string                       `json:"default_strategy,omitempty"`
	StrategyHardware     map[string]map[string]string `json:"strategy_hardware,omitempty"`
	HardwareOverrides    map[string]Override          `json:"hardware_overrides,omitempty"`
	StrategyOverrides    map[string]StrategyOverride  `json:"strategy_overrides,omitempty"`
	HardwareNotes        map[string]string            `json:"hardware_notes,omitempty"`
}

type Feature struct {
	Args        []string        `json:"args,omitempty"`
	DefaultMode string          `json:"default_mode,omitempty"`
	Modes       map[string]Mode `json:"modes,omitempty"`
}

type Mode struct {
	Args              []string            `json:"args,omitempty"`
	Variants          []string            `json:"variants,omitempty"`
	Hardware          map[string]string   `json:"hardware,omitempty"`
	HardwareOverrides map[string]Override `json:"hardware_overrides,omitempty"`
}

type Variant struct {
	ModelID           string              `json:"model_id,omitempty"`
	Label             string              `json:"label,omitempty"`
	Precision         string              `json:"precision,omitempty"`
	VRAMMinimumGB     float64             `json:"vram_minimum_gb,omitempty"`
	MinVLLM           string              `json:"min_vllm_version,omitempty"`
	DockerImage       string              `json:"docker_image,omitempty"`
	SupportedHardware []string            `json:"supported_hardware,omitempty"`
	DefaultModes      map[string]string   `json:"default_modes,omitempty"`
	HardwareOverrides map[string]Override `json:"hardware_overrides,omitempty"`
}

type Override struct {
	DockerImage string            `json:"docker_image,omitempty"`
	ExtraArgs   []string          `json:"extra_args,omitempty"`
	ExtraEnv    map[string]string `json:"extra_env,omitempty"`
	NoPip       bool              `json:"no_pip,omitempty"`
}

type StrategyOverride struct {
	TP                int                 `json:"tp,omitempty"`
	ExtraArgs         []string            `json:"extra_args,omitempty"`
	HardwareOverrides map[string]Override `json:"hardware_overrides,omitempty"`
}

// Load returns the vendored catalog.
func Load() (Index, error) {
	var ix Index
	if err := json.Unmarshal(catalogJSON, &ix); err != nil {
		return Index{}, fmt.Errorf("parse catalog: %w", err)
	}
	return ix, nil
}

// Lookup finds the recipe and variant serving a HuggingFace repo. Variants carry
// their own model_id, so DeepSeek-V4-Flash-0731 resolves to the V4-Flash recipe
// under variant "default" rather than to a file of its own.
func (ix Index) Lookup(repo string) (Recipe, string, bool) {
	for _, r := range ix.Recipes {
		for _, name := range sortedKeys(r.Variants) {
			if r.Variants[name].ModelID == repo {
				return r, name, true
			}
		}
	}
	for _, r := range ix.Recipes {
		if r.ModelID != repo {
			continue
		}
		if _, ok := r.Variants["default"]; ok {
			return r, "default", true
		}
		if names := sortedKeys(r.Variants); len(names) > 0 {
			return r, names[0], true
		}
		return r, "", true
	}
	return Recipe{}, "", false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
