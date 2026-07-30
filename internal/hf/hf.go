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
	"strings"
	"time"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

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

// Search queries the Hub for text-generation models, most-downloaded first.
func Search(query string, limit int) ([]Model, error) {
	u := fmt.Sprintf(
		"https://huggingface.co/api/models?search=%s&filter=text-generation&sort=downloads&direction=-1&limit=%d",
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
	resp, err := httpClient.Get("https://huggingface.co/api/models/" + repo + "/tree/main")
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
// out of any literal "vllm serve" snippet.
//
// Deliberately ignored even if present: --tensor-parallel-size,
// --pipeline-parallel-size, --data-parallel-size, --host, --port,
// --served-model-name -- deployment-topology flags that are wrong to copy
// onto a different machine.
func DetectFlags(repo string) (DetectedFlags, error) {
	var d DetectedFlags
	resp, err := httpClient.Get("https://huggingface.co/" + repo + "/raw/main/README.md")
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

	// Match "vllm serve ..." including backslash-continued lines.
	snippetRe := regexp.MustCompile(`vllm serve(?:.*\\\n)*.*`)
	snippet := snippetRe.FindString(string(body))
	if snippet == "" {
		return d, nil
	}

	var flags []string
	addBool := func(flag string) {
		if strings.Contains(snippet, flag) {
			flags = append(flags, flag)
		}
	}
	addValued := func(flag string) {
		re := regexp.MustCompile(regexp.QuoteMeta(flag) + `\s+(\S+)`)
		if m := re.FindStringSubmatch(snippet); m != nil {
			flags = append(flags, flag+" "+m[1])
		}
	}
	addBool("--enable-auto-tool-choice")
	addValued("--tool-call-parser")
	addValued("--reasoning-parser")
	addBool("--enable-prefix-caching")
	addBool("--trust-remote-code")
	d.ExtraArgs = strings.Join(flags, " ")

	if m := regexp.MustCompile(`--max-model-len\s+(\d+)`).FindStringSubmatch(snippet); m != nil {
		d.MaxModelLen = m[1]
	}
	return d, nil
}
