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

// Weights describes the weight files found in a model repo.
type Weights struct {
	SizeGB float64 // 0 when the repo has no weight files we recognise
	// Variants is how many independent weight groupings the repo holds.
	// >1 means SizeGB is the smallest of them; 1 means it is the whole story.
	Variants int
}

// maxTreePages bounds how far we follow the Hub's cursor pagination. Pages hold
// 1000 entries, so this covers any real repo while keeping a broken or hostile
// Link header from looping forever.
const maxTreePages = 20

// treeEntry is one file or directory in a repo listing.
type treeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

// ggufShard matches the "-00001-of-00003" that marks one piece of a split
// GGUF, so the pieces can be added back together.
var ggufShard = regexp.MustCompile(`(?i)-\d+-of-\d+\.gguf$`)

// quantFolder matches directory names that name a precision rather than a part
// of the model -- Q4_K_M, UD-IQ1_S, BF16, NVFP4. Sub-folders like these are
// alternatives to each other; ones like text_encoder/ or vae/ are not.
var quantFolder = regexp.MustCompile(`(?i)^(ud-)?(i?q\d|bf16|fp16|fp32|f16|f32|fp8|fp4|nvfp4|mxfp4|int4|int8|awq|gptq)`)

// companionFiles are weight files that are not a model you can serve: a vision
// projector, a LoRA adapter, or training leftovers. They matter because they
// are small, so left in they win "smallest" outright -- unsloth/gemma-3-27b-it-GGUF
// would report its 0.86 GB mmproj instead of the 6.5 GB smallest real quant.
// Matched anywhere in the name, since the conventions disagree about order:
// mmproj-F16.gguf and Qwen2-VL-7B-mmproj-f16.gguf are the same kind of file.
var companionFiles = []string{
	"mmproj", "mm_projector", "lora", "adapter",
	"training_args", "optimizer", "scheduler", "rng_state",
}

func isCompanionFile(p string) bool {
	base := strings.ToLower(p[strings.LastIndex(p, "/")+1:])
	for _, c := range companionFiles {
		if strings.Contains(base, c) {
			return true
		}
	}
	return false
}

// componentFolders hold one part of a model rather than a whole copy of it.
// Naming them is what stops a GGUF-quantized pipeline -- transformer/, vae/,
// text_encoder/ -- from looking like a set of alternatives, which would report
// the VAE as though it were the model.
var componentFolders = regexp.MustCompile(`(?i)^(text_encoder|tokenizer|scheduler|feature_extractor|safety_checker|image_encoder|vision_tower|audio_tower|language_model|mm_projector|transformer|unet|vae|controlnet)`)

// smallestPlausibleShare guards the same mistake from the other side. The full
// span of GGUF quants is about 9x, TQ1_0 at under two bits per weight against
// BF16 at sixteen (unsloth/DeepSeek-V3.1-GGUF runs 170 GB to 1.34 TB), so a
// twentieth is comfortably below any real quant while still discarding the
// components this is here to catch -- a VAE runs a few hundredths of its
// transformer. Set it any tighter and it starts dropping honest IQ1 quants.
const smallestPlausibleShare = 0.05

func isGGUF(p string) bool { return strings.HasSuffix(strings.ToLower(p), ".gguf") }

// oneCopyOfRoot sums the root-level tensor files that make up a single copy of
// the model. Repos often ship the same weights twice: Mistral keeps a
// single-file consolidated.safetensors next to the sharded copy, and older
// repos keep pytorch_model.bin next to model.safetensors. Adding both reports
// exactly double -- mistralai/Mistral-7B-Instruct-v0.3 measures 29.0 GB when it
// is 14.5 GB -- so we keep one set and drop the duplicate.
func oneCopyOfRoot(files []treeEntry) int64 {
	var safetensors, consolidated, other int64
	for _, f := range files {
		base := strings.ToLower(f.Path[strings.LastIndex(f.Path, "/")+1:])
		switch {
		case !strings.HasSuffix(base, ".safetensors"):
			other += f.Size
		case strings.HasPrefix(base, "consolidated"):
			consolidated += f.Size
		default:
			safetensors += f.Size
		}
	}
	// The largest set, not a preferred one. Preferring safetensors outright
	// would report a stray 0.3 GB file beside 140 GB of .bin shards and call
	// it a fit, and every copy here is the same model anyway.
	return max(safetensors, max(consolidated, other))
}

// WeightsSize measures the weight files (.safetensors/.bin/.gguf) in a model
// repo and reports what one loadable copy costs.
//
// The trap is that plenty of repos hold several complete models, and adding
// those together produces a number with no meaning. Two conventions do it:
// GGUF publishers put a whole quant in each sub-folder (unsloth/GLM-5.2-GGUF
// has 22, summing to ~10.6 TB) or as one flat file per quant at the root
// (bartowski/Qwen_Qwen3-32B-GGUF has 26, summing to 466 GB for a model whose
// smallest quant is 9 GB). Sharded weights, by contrast, genuinely are one
// model split across files and do have to be added up.
//
// So we group the weight files into candidates -- one complete set each -- and
// report the smallest, since that is the one with any chance of fitting.
// Variants says how many there were, so the caller can admit which it picked.
//
// Tensor files at the repo root settle it on their own: that is the model, and
// anything under a sub-folder is a second copy in another format (openai/gpt-oss-120b
// keeps whole metal/ and original/ copies beside it) rather than a smaller option.
//
// Sub-folders are only read as alternatives when they look like alternatives --
// they hold GGUFs, or they are named for a precision. Otherwise they are the
// components of one model, as in text_encoder/ plus vae/ plus transformer/, and
// have to be added up. Guessing wrong that way round would report a vision
// tower as if it were the whole model.
func WeightsSize(repo string) (Weights, error) {
	files, err := repoTree(repo)
	if err != nil {
		return Weights{}, err
	}

	var rootTensors []treeEntry
	candidates := map[string]int64{}
	subdirs := map[string]int64{}
	alternatives := map[string]bool{}
	for _, f := range files {
		if f.Type == "directory" || !isWeightFile(f.Path) || isCompanionFile(f.Path) {
			continue
		}
		dir, _, nested := strings.Cut(f.Path, "/")
		switch {
		case nested:
			subdirs[dir] += f.Size
			// A folder is an alternative when it is named for a precision, or
			// when it holds GGUFs and is not one of the known model parts --
			// a GGUF-quantized transformer/ plus vae/ pipeline is still one
			// model, and reading it as a choice would report the VAE.
			if quantFolder.MatchString(dir) || (isGGUF(f.Path) && !componentFolders.MatchString(dir)) {
				alternatives[dir] = true
			}
		case isGGUF(f.Path):
			// Root GGUFs are one quant per name, minus any shard suffix. The
			// name is lowered so a repo shipping .GGUF still groups its shards.
			key := strings.ToLower(f.Path)
			candidates[ggufShard.ReplaceAllString(key, ".gguf")] += f.Size
		default:
			rootTensors = append(rootTensors, f)
		}
	}

	// Anything at the repo root settles it, and sub-folders are then second
	// copies in another format (gpt-oss-120b keeps whole metal/ and original/
	// copies) rather than smaller options. Root tensors join the GGUFs as one
	// more candidate instead of short-circuiting, so a repo shipping both
	// still gets its smallest option compared.
	if n := oneCopyOfRoot(rootTensors); n > 0 {
		candidates[""] = n
	}
	if len(candidates) == 0 {
		switch {
		case len(alternatives) > 0:
			// Quant folders: each is a complete copy, so compare them.
			for dir := range alternatives {
				candidates[dir] = subdirs[dir]
			}
		default:
			// Components of one model: the sum is the model.
			var total int64
			for _, size := range subdirs {
				total += size
			}
			if total > 0 {
				candidates["."] = total
			}
		}
	}

	if len(candidates) == 0 {
		return Weights{}, nil
	}
	return pickSmallest(candidates), nil
}

// pickSmallest reports the smallest candidate, which is the one with any chance
// of fitting. Candidates far below their siblings are dropped first: real
// quants of one model stay within a single order of magnitude of each other, so
// anything under that is a component or a stray file rather than a choice.
func pickSmallest(candidates map[string]int64) Weights {
	var largest int64
	for _, size := range candidates {
		largest = max(largest, size)
	}
	floor := int64(float64(largest) * smallestPlausibleShare)
	smallest, n := int64(-1), 0
	for _, size := range candidates {
		if size < floor {
			continue
		}
		n++
		if smallest < 0 || size < smallest {
			smallest = size
		}
	}
	return Weights{SizeGB: float64(smallest) / 1e9, Variants: n}
}

func isWeightFile(path string) bool {
	p := strings.ToLower(path)
	return strings.HasSuffix(p, ".safetensors") ||
		strings.HasSuffix(p, ".bin") ||
		strings.HasSuffix(p, ".gguf")
}

// repoTree lists every file in a repo, following the Hub's cursor pagination.
// The listing is recursive because plenty of repos keep their weights one level
// down; WeightsSize decides what to do with the sub-folders.
//
// Entries are de-duplicated by path, because a cursor that fails to advance
// would otherwise have us add the same shards in again on every pass. Running
// out of pages is an error rather than a short answer: a truncated listing
// undercounts, and an undercount is the one wrong answer that tells someone a
// model fits when it does not.
func repoTree(repo string) ([]treeEntry, error) {
	next := baseURL + "/api/models/" + repo + "/tree/main?recursive=true"
	var all []treeEntry
	seen := map[string]bool{}
	for page := 0; page < maxTreePages; page++ {
		entries, link, err := treePage(next)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if !seen[e.Path] {
				seen[e.Path] = true
				all = append(all, e)
			}
		}
		if link == "" {
			return all, nil
		}
		next = link
	}
	return nil, fmt.Errorf("hub tree: %s has more than %d pages", repo, maxTreePages)
}

func treePage(u string) ([]treeEntry, string, error) {
	resp, err := httpClient.Get(u)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("hub tree: HTTP %d", resp.StatusCode)
	}
	var entries []treeEntry
	if err := json.NewDecoder(resp.Body).Decode(&entries); err != nil {
		return nil, "", err
	}
	return entries, nextPageURL(resp.Header.Get("Link"), baseURL), nil
}

// nextPageURL pulls the rel="next" target out of an RFC 8288 Link header,
// which is how the Hub hands back the cursor for the following page.
//
// The target has to stay on the host we asked, since otherwise a response gets
// to redirect the next request anywhere it likes. That is a parsed comparison
// of scheme and host rather than a string prefix, because prefixes are not
// host checks: https://huggingface.co.evil/ and https://huggingface.co@evil/
// both start with the Hub's URL and neither is the Hub.
func nextPageURL(header, want string) string {
	base, err := url.Parse(want)
	if err != nil {
		return ""
	}
	for _, link := range strings.Split(header, ",") {
		target, rest, ok := strings.Cut(strings.TrimSpace(link), ";")
		if !ok || !strings.Contains(rest, `rel="next"`) {
			continue
		}
		target = strings.TrimSpace(target)
		if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") {
			continue
		}
		u, err := url.Parse(target[1 : len(target)-1])
		if err != nil || u.User != nil {
			continue
		}
		if u.Scheme == base.Scheme && u.Host == base.Host {
			return u.String()
		}
	}
	return ""
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
