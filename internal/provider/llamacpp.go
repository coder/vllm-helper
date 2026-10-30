package provider

import "strings"

// llamaCPP is llama.cpp's OpenAI-compatible server, llama-server. It serves
// GGUF on Metal, CUDA or the CPU, and sizes its own context and GPU layers to
// fit the machine, so a profile needs only the model.
//
// Unlike mlx_lm.server it checks an API key, so the command binds every
// interface the way vllm serve does. It downloads a repo's GGUF itself, and
// its /health reports healthy only once the model is loaded.
type llamaCPP struct{}

func (llamaCPP) Name() string    { return LlamaCPP }
func (llamaCPP) BinKey() string  { return "LLAMA_SERVER_BIN" }
func (llamaCPP) BinName() string { return "llama-server" }

func (llamaCPP) Capabilities() Capabilities {
	return Capabilities{APIKey: true, GGUFQuant: true}
}

// Command passes MODEL as --alias too, so /v1/models reports exactly MODEL,
// as vLLM's does, whether the model comes from the Hub or a file.
func (llamaCPP) Command(s Spec) ([]string, error) {
	source := "-hf"
	if isLocalGGUF(s.Model) {
		source = "-m"
	}
	args := []string{s.Bin, source, s.Model, "--alias", s.Model, "--host", "0.0.0.0", "--port", s.Port}
	if s.APIKey != "" {
		args = append(args, "--api-key", s.APIKey)
	}
	return appendExtraArgs(args, s.ExtraArgs)
}

// isLocalGGUF reports whether model is a file on disk rather than a Hugging
// Face repo: a repo id is never an absolute path, and one ending in .gguf
// names a file.
func isLocalGGUF(model string) bool {
	return strings.HasPrefix(model, "/") || strings.HasSuffix(strings.ToLower(model), ".gguf")
}
