// Package hf is a minimal HuggingFace Hub client: model search, weight-size
// lookup, and auto-detection of vLLM serving flags from model cards.
package hf

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	httpClient = &http.Client{Timeout: 15 * time.Second}
	// baseURL is the Hub root; tests point it at a local server.
	baseURL = "https://huggingface.co"
)

type Model struct {
	ID          string   `json:"id"`
	Downloads   int64    `json:"downloads"`
	Likes       int64    `json:"likes"`
	PipelineTag string   `json:"pipeline_tag"`
	CreatedAt   string   `json:"createdAt"`
	Tags        []string `json:"tags"`
}

// License extracts the license tag ("license:apache-2.0" -> "apache-2.0").
func (m Model) License() string {
	for _, t := range m.Tags {
		if rest, ok := strings.CutPrefix(t, "license:"); ok {
			return rest
		}
	}
	return "unknown"
}

// QuantTags returns quantization-related tags (fp8, gptq, awq, ...).
func (m Model) QuantTags() []string {
	known := map[string]bool{
		"fp4": true, "fp8": true, "nvfp4": true, "mxfp4": true, "awq": true,
		"gptq": true, "int4": true, "int8": true, "8-bit": true, "4-bit": true,
		"gguf": true, "bf16": true,
	}
	var out []string
	for _, t := range m.Tags {
		if known[strings.ToLower(t)] {
			out = append(out, t)
		}
	}
	return out
}

// Lookup fetches one model's metadata by repo id.
func Lookup(repo string) (Model, error) {
	var m Model
	resp, err := httpClient.Get(baseURL + "/api/models/" + repo)
	if err != nil {
		return m, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return m, fmt.Errorf("no model %q on HuggingFace (private, gated, or a typo?)", repo)
	}
	if resp.StatusCode != http.StatusOK {
		return m, fmt.Errorf("hub lookup: HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return m, err
	}
	if m.ID == "" {
		m.ID = repo
	}
	return m, nil
}

// WhoAmI validates a Hub token and returns the account name it belongs to.
func WhoAmI(token string) (string, error) {
	req, _ := http.NewRequest("GET", baseURL+"/api/whoami-v2", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return "", fmt.Errorf("token rejected (HTTP 401)")
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("whoami: HTTP %d", resp.StatusCode)
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Name, nil
}

// Search queries the Hub for text-generation models, most-downloaded first.
func Search(query string, limit int) ([]Model, error) {
	u := fmt.Sprintf(
		baseURL+"/api/models?search=%s&filter=text-generation&sort=downloads&direction=-1&limit=%d",
		url.QueryEscape(query), limit)
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("hub search: HTTP %d", resp.StatusCode)
	}
	var models []Model
	if err := json.NewDecoder(resp.Body).Decode(&models); err != nil {
		return nil, err
	}
	return models, nil
}

// WeightsSizeGB sums the weight files (.safetensors/.bin/.gguf) at the root
// of a model repo. Returns 0 if the repo keeps weights in subfolders.
func WeightsSizeGB(repo string) (float64, error) {
	resp, err := httpClient.Get(baseURL + "/api/models/" + repo + "/tree/main")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("hub tree: HTTP %d", resp.StatusCode)
	}
	var files []struct {
		Path string `json:"path"`
		Size int64  `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return 0, err
	}
	var total int64
	for _, f := range files {
		if strings.HasSuffix(f.Path, ".safetensors") || strings.HasSuffix(f.Path, ".bin") || strings.HasSuffix(f.Path, ".gguf") {
			total += f.Size
		}
	}
	return float64(total) / 1e9, nil
}

// DetectedFlags is what flag auto-detection found in a model card.
type DetectedFlags struct {
	ExtraArgs   string // e.g. "--enable-auto-tool-choice --tool-call-parser qwen3_coder"
	MaxModelLen string // from --max-model-len in the snippet, "" if absent
}

// DetectFlags fetches a model's README and pulls known vLLM serving flags
// out of any literal "vllm serve" snippet. Topology flags (parallelism,
// host, port, served-model-name) are deliberately ignored: they describe
// someone else's deployment, not the model.
func DetectFlags(repo string) (DetectedFlags, error) {
	var d DetectedFlags
	resp, err := httpClient.Get(baseURL + "/" + repo + "/raw/main/README.md")
	if err != nil {
		return d, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return d, fmt.Errorf("model card: HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return d, err
	}

	// Match "vllm serve ..." including backslash-continued lines. A card
	// without a snippet is common (facebook/opt-125m has none); it just
	// means there are no flags to adopt, not that we should skip the
	// context clamp below -- those models are exactly the ones whose real
	// context is far under the generic default.
	snippetRe := regexp.MustCompile(`vllm serve(?:.*\\\n)*.*`)
	if snippet := snippetRe.FindString(string(body)); snippet != "" {
		d.ExtraArgs, d.MaxModelLen = flagsFromSnippet(snippet)
	}

	// Model cards routinely paste a snippet from a bigger sibling model, so
	// the advertised context can exceed what this model actually supports.
	// vLLM refuses to start in that case, so clamp to the real ceiling.
	if limit, err := ContextLimit(repo); err == nil {
		d.MaxModelLen = clampContext(d.MaxModelLen, limit)
	}
	return d, nil
}

// ContextLimit reads max_position_embeddings from a model's config.json --
// the ceiling vLLM validates --max-model-len against. Returns 0 when the
// field is absent.
func ContextLimit(repo string) (int, error) {
	resp, err := httpClient.Get(baseURL + "/" + repo + "/raw/main/config.json")
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("model config: HTTP %d", resp.StatusCode)
	}
	var cfg struct {
		MaxPositionEmbeddings int `json:"max_position_embeddings"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&cfg); err != nil {
		return 0, err
	}
	return cfg.MaxPositionEmbeddings, nil
}

// clampContext caps a model card's advertised context length at the model's
// real limit. An unparseable or absent value falls back to the limit itself,
// which is a better default than vLLM's.
func clampContext(value string, limit int) string {
	if limit <= 0 {
		return value
	}
	n, err := strconv.Atoi(value)
	if err != nil || n > limit {
		return strconv.Itoa(limit)
	}
	return value
}

var systemMessagePositionRe = regexp.MustCompile(
	`(\{%-\s*if\s+not\s+loop\.first\s*%\}\s*)` +
		`\{\{-\s*raise_exception\('System message must be at the beginning\.'\)\s*\}\}` +
		`(\s*\{%-\s*endif\s*%\})`,
)

// DetectChatTemplatePatch fetches a model's bundled chat template and checks
// whether it hard-crashes on a system-role message that isn't first in the
// conversation (found for real in some Qwen3.5-derived templates -- Ornith
// and Qwen3.5-122B both have it, byte-for-byte identical check). Agent
// harnesses -- Coder Agents included -- routinely inject periodic system-
// role "reminder" messages mid-conversation, which trips this. No vLLM flag
// controls it; the only lever is --chat-template <path> to override the
// whole file, so this returns a patched copy that drops a misplaced system
// message instead of crashing.
//
// ok is false (with empty patched and nil err) if the model's template
// doesn't have this issue -- the caller should just use the model's own
// bundled template as normal in that case, not add --chat-template at all.
func DetectChatTemplatePatch(repo string) (patched string, ok bool, err error) {
	resp, err := httpClient.Get(baseURL + "/" + repo + "/raw/main/chat_template.jinja")
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false, nil // no separate template file -- nothing to patch
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", false, err
	}
	return patchSystemMessageCheck(string(body))
}

// patchSystemMessageCheck is the pure (no I/O) half of
// DetectChatTemplatePatch, split out so the regex logic is unit-testable
// without mocking HTTP.
func patchSystemMessageCheck(text string) (patched string, ok bool, err error) {
	if !strings.Contains(text, "System message must be at the beginning") {
		return "", false, nil
	}
	result := systemMessagePositionRe.ReplaceAllString(text, "${1}{# vllm-helper: dropped instead of raising #}${2}")
	if result == text {
		// The crash string is present but our regex didn't match its exact
		// surrounding shape -- don't silently claim a fix that didn't apply.
		return "", false, fmt.Errorf("found the crash string but this template's exact shape didn't match the patch regex -- patch by hand")
	}
	return result, true, nil
}
