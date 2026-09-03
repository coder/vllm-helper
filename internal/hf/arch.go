package hf

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Arch is what a model's config.json says about its shape.
//
// Only the parts that decide whether something can be served are read. A
// config carries a great deal more, and parsing what we do not use would mean
// breaking on fields that change.
type Arch struct {
	ModelType string `json:"model_type"`
	// NumExperts is set by mixture-of-experts models under one of several
	// names, since the field was never standardised.
	NumExperts       int `json:"num_experts"`
	NumLocalExperts  int `json:"num_local_experts"`
	NRoutedExperts   int `json:"n_routed_experts"`
	NumExpertsPerTok int `json:"num_experts_per_tok"`
}

// MoE reports whether this is a mixture-of-experts model.
//
// Three spellings because three model families chose differently: Qwen and
// OLMoE use num_experts, Mixtral-derived configs use num_local_experts, and
// DeepSeek uses n_routed_experts.
func (a Arch) MoE() bool {
	return a.NumExperts > 0 || a.NumLocalExperts > 0 || a.NRoutedExperts > 0
}

// Config reads a model's config.json.
//
// Returns a zero Arch and no error when the repo has none: GGUF repos
// typically ship no config at all, and that is a fact about the repo rather
// than a failure to report.
func Config(repo string) (Arch, error) {
	resp, err := httpClient.Get(fmt.Sprintf("%s/%s/raw/main/config.json", baseURL, repo))
	if err != nil {
		return Arch{}, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound, http.StatusUnauthorized, http.StatusForbidden:
		// Missing, or gated behind a licence click. Neither is something the
		// caller can act on, and neither means the model is unservable.
		return Arch{}, nil
	default:
		return Arch{}, fmt.Errorf("config.json: HTTP %d", resp.StatusCode)
	}
	var a Arch
	if err := json.NewDecoder(resp.Body).Decode(&a); err != nil {
		return Arch{}, err
	}
	return a, nil
}

// ggufFusedExpertSupport are the model types whose fused expert weights
// vLLM's GGUF plugin can map.
//
// The plugin builds its tensor-name map from a Transformers reference model
// and errors on anything it cannot match, suppressing expected misses with a
// per-architecture list. Modern Transformers fuses MoE experts into single
// mlp.experts.gate_up_proj / down_proj parameters, and only these two branches
// register that shape -- so every other MoE fails at load with "Failed to map
// GGUF parameters", one per layer, after the service has already restarted.
//
// Read out of the installed plugin at vllm-gguf-plugin 0.0.5, and unchanged on
// its main branch. Kept deliberately short: a name absent here is refused, and
// being wrong in that direction costs an explanation rather than an outage.
var ggufFusedExpertSupport = map[string]bool{
	"olmoe":      true,
	"minimax_m2": true,
}

// GGUFServable reports whether a GGUF build of this architecture can be
// loaded, and why not when it cannot.
//
// Dense models are fine -- the mapping problem is specific to experts.
func GGUFServable(a Arch) (bool, string) {
	if !a.MoE() || ggufFusedExpertSupport[strings.ToLower(a.ModelType)] {
		return true, ""
	}
	return false, "a mixture-of-experts model, which vLLM's GGUF plugin cannot map " +
		"(only OLMoE and MiniMax-M2 are supported) -- look for a safetensors quant instead"
}
