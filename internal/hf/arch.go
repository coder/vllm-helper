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

	// The shape of the KV cache. HeadDim is read, never derived: Qwen3-0.6B
	// declares head_dim 128 while hidden_size/num_attention_heads is 64, and
	// halving the per-token cost is the direction that ends in an OOM.
	NumHiddenLayers   int    `json:"num_hidden_layers"`
	NumAttentionHeads int    `json:"num_attention_heads"`
	NumKeyValueHeads  int    `json:"num_key_value_heads"`
	HeadDim           int    `json:"head_dim"`
	HiddenSize        int    `json:"hidden_size"`
	MaxPositionEmbed  int    `json:"max_position_embeddings"`
	TorchDtype        string `json:"torch_dtype"`
	// LayerTypes names each layer for hybrids. Nemotron-H is 52 layers of
	// which only a handful attend; counting them all would overstate the
	// cache several times over.
	LayerTypes []string `json:"layer_types"`

	// TextConfig carries the language model's shape on multimodal repos,
	// where the top level describes the wrapper instead.
	TextConfig *Arch `json:"text_config"`
}

// text returns the sub-config describing the language model, which on a
// multimodal repo is where the attention shape actually lives.
func (a Arch) text() Arch {
	if a.TextConfig != nil && a.TextConfig.NumHiddenLayers > 0 {
		return *a.TextConfig
	}
	return a
}

// attentionLayers counts the layers that hold a KV cache.
//
// Hybrids interleave attention with Mamba or recurrent layers that hold none,
// and say so in layer_types. Using num_hidden_layers for those overstates the
// cache by the ratio of the two -- 52 against 4 on nemotron_h.
func (a Arch) attentionLayers() int {
	if n := len(a.LayerTypes); n > 0 {
		attn := 0
		for _, t := range a.LayerTypes {
			if strings.Contains(strings.ToLower(t), "attention") {
				attn++
			}
		}
		if attn > 0 {
			return attn
		}
	}
	return a.NumHiddenLayers
}

// kvDtypeBytes is the width of one cached element. Every dtype vLLM stores a
// KV cache in is 2 bytes wide unless the cache itself is quantised, which is a
// separate flag we do not set.
func kvDtypeBytes(dtype string) int {
	switch strings.ToLower(dtype) {
	case "float32", "float", "fp32":
		return 4
	default:
		return 2
	}
}

// KVBytesPerToken is what one token of context costs in KV cache.
//
//	2 (K and V) * layers * kv_heads * head_dim * dtype_bytes
//
// Validated against this project's DGX Spark to the byte: Qwen3-0.6B gives
// 2*28*8*128*2 = 114,688 B/token, and vLLM reported 952,784 tokens in 101.77
// GiB, which is the same number.
//
// Returns 0 when the config did not say enough, which callers must treat as
// "unknown" rather than "free".
func (a Arch) KVBytesPerToken() int64 {
	t := a.text()
	layers, heads, dim := t.attentionLayers(), t.NumKeyValueHeads, t.HeadDim
	if heads == 0 {
		heads = t.NumAttentionHeads // no grouped-query attention
	}
	if dim == 0 && t.NumAttentionHeads > 0 && t.HiddenSize > 0 {
		// Only as a last resort, and only when the config omitted head_dim
		// entirely -- deriving over a declared value is the OOM above.
		dim = t.HiddenSize / t.NumAttentionHeads
	}
	if layers <= 0 || heads <= 0 || dim <= 0 {
		return 0
	}
	return 2 * int64(layers) * int64(heads) * int64(dim) * int64(kvDtypeBytes(t.TorchDtype))
}

// MaxContext is the longest context this model was trained for, or 0 if the
// config did not say.
func (a Arch) MaxContext() int { return a.text().MaxPositionEmbed }

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
