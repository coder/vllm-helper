package hf

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const repo = "Qwen/Qwen3-8B-FP8"

// newHub points the package at a local stand-in for the Hub. baseURL is
// package state, so tests using it can't run in parallel.
func newHub(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	old := baseURL
	baseURL = srv.URL
	t.Cleanup(func() { baseURL = old })
	return srv
}

// serveTestdata answers each Hub route from a file, or 404 if none is given.
func serveTestdata(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for route, file := range routes {
		body, err := os.ReadFile(filepath.Join("testdata", file))
		if err != nil {
			t.Fatal(err)
		}
		mux.HandleFunc(route, func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(body)
		})
	}
	return newHub(t, mux)
}

// serveTree answers any request with one page of a repo listing. It insists on
// recursive=true, because without it the Hub returns only the repo root and
// every sub-folder test here would pass against the bug it is guarding.
func serveTree(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return newHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("recursive") != "true" {
			t.Errorf("tree request %q is not recursive", r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(body))
	}))
}

func status(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(code)
	})
}

func TestLookup(t *testing.T) {
	serveTestdata(t, map[string]string{"/api/models/" + repo: "model.json"})

	m, err := Lookup(repo)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if m.ID != repo {
		t.Errorf("ID = %q, want %q", m.ID, repo)
	}
	if m.Downloads != 481233 || m.Likes != 912 {
		t.Errorf("Downloads/Likes = %d/%d, want 481233/912", m.Downloads, m.Likes)
	}
	if got := m.License(); got != "apache-2.0" {
		t.Errorf("License() = %q, want %q", got, "apache-2.0")
	}
	if got := m.QuantTags(); !reflect.DeepEqual(got, []string{"fp8"}) {
		t.Errorf("QuantTags() = %#v, want [fp8]", got)
	}
}

func TestLookupFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler http.Handler
		wantErr string
	}{{
		name:    "missing model names the repo",
		handler: status(http.StatusNotFound),
		wantErr: repo,
	}, {
		name:    "server error surfaces the status",
		handler: status(http.StatusInternalServerError),
		wantErr: "HTTP 500",
	}, {
		name: "malformed body is an error, not a zero model",
		handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("{not json"))
		}),
		wantErr: "invalid character",
	}}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newHub(t, tt.handler)
			_, err := Lookup(repo)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to mention %q", err, tt.wantErr)
			}
		})
	}
}

// The Hub only returns an ID for repos it knows; a bare 200 with an empty
// body must not leave callers with a nameless model.
func TestLookupFallsBackToTheRequestedRepo(t *testing.T) {
	newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"downloads":1}`))
	}))
	m, err := Lookup(repo)
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if m.ID != repo {
		t.Errorf("ID = %q, want the repo that was asked for", m.ID)
	}
}

func TestSearch(t *testing.T) {
	var gotQuery string
	newHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		body, err := os.ReadFile(filepath.Join("testdata", "search.json"))
		if err != nil {
			t.Error(err)
			return
		}
		_, _ = w.Write(body)
	}))

	models, err := Search("qwen3 8b", 25)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("got %d models, want 3", len(models))
	}
	if models[0].ID != "Qwen/Qwen3-8B-FP8" {
		t.Errorf("first result = %q, want the most downloaded", models[0].ID)
	}
	for _, want := range []string{"search=qwen3+8b", "filter=text-generation", "sort=downloads", "limit=25"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q is missing %q", gotQuery, want)
		}
	}
}

func TestSearchNon200(t *testing.T) {
	newHub(t, status(http.StatusTooManyRequests))
	if _, err := Search("qwen", 5); err == nil || !strings.Contains(err.Error(), "HTTP 429") {
		t.Errorf("error = %v, want it to report HTTP 429", err)
	}
}

func TestWeightsSize(t *testing.T) {
	serveTestdata(t, map[string]string{"/api/models/" + repo + "/tree/main": "tree.json"})

	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	// Only the two safetensors shards count, not the tokenizer or the index.
	if want := 6.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v", got.SizeGB, want)
	}
	if got.Variants != 1 {
		t.Errorf("Variants = %d, want 1 for a plain root layout", got.Variants)
	}
}

// A repo whose weights all live one level down is still one model, and used to
// measure as 0 because the listing stopped at the root.
func TestWeightsSizeInSubfolder(t *testing.T) {
	serveTree(t, `[
		{"type":"directory","path":"weights"},
		{"type":"file","path":"weights/model-00001-of-00002.safetensors","size":4000000000},
		{"type":"file","path":"weights/model-00002-of-00002.safetensors","size":2500000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 6.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v", got.SizeGB, want)
	}
	if got.Variants != 1 {
		t.Errorf("Variants = %d, want 1: one folder is one model, not a choice", got.Variants)
	}
}

// The GGUF convention puts a complete quant in each folder. Summing them would
// report ~10.6 TB for a repo like unsloth/GLM-5.2-GGUF, so we take the smallest.
func TestWeightsSizePicksSmallestVariant(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"UD-Q2_K_XL/model.gguf","size":9000000000},
		{"type":"file","path":"UD-IQ1_S/model-00001-of-00002.gguf","size":2000000000},
		{"type":"file","path":"UD-IQ1_S/model-00002-of-00002.gguf","size":1000000000},
		{"type":"file","path":"BF16/model.gguf","size":40000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 3.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (the summed UD-IQ1_S shards)", got.SizeGB, want)
	}
	if got.Variants != 3 {
		t.Errorf("Variants = %d, want 3", got.Variants)
	}
}

// The other GGUF convention: one flat file per quant at the repo root. Summing
// them puts bartowski/Qwen_Qwen3-32B-GGUF at 466 GB, so a 32B model that fits
// in 9 GB gets called too big.
func TestWeightsSizePicksSmallestRootGGUF(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"Qwen3-32B-Q8_0.gguf","size":35000000000},
		{"type":"file","path":"Qwen3-32B-IQ2_XXS.gguf","size":9000000000},
		{"type":"file","path":"Qwen3-32B-Q4_K_M.gguf","size":20000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 9.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v", got.SizeGB, want)
	}
	if got.Variants != 3 {
		t.Errorf("Variants = %d, want 3", got.Variants)
	}
}

// A split GGUF is one model, so its pieces still have to be added up -- the
// shard suffix is what separates this from the case above.
func TestWeightsSizeSumsRootGGUFShards(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"DeepSeek-UD-TQ1_0-00001-of-00002.gguf","size":4000000000},
		{"type":"file","path":"DeepSeek-UD-TQ1_0-00002-of-00002.gguf","size":2500000000},
		{"type":"file","path":"DeepSeek-UD-IQ2_M.gguf","size":90000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 6.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (the two shards, added)", got.SizeGB, want)
	}
	if got.Variants != 2 {
		t.Errorf("Variants = %d, want 2: two quants, one of them split", got.Variants)
	}
}

// A vision projector is small, ships beside the quants, and is useless alone.
// Counted as a candidate it wins "smallest" outright: unsloth/gemma-3-27b-it-GGUF
// reported its 0.86 GB mmproj instead of the 6.5 GB smallest real quant.
func TestWeightsSizeSkipsCompanionFiles(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"mmproj-BF16.gguf","size":860000000},
		{"type":"file","path":"gemma-3-27b-it-Q4_K_M.gguf","size":16000000000},
		{"type":"file","path":"gemma-3-27b-it-IQ2_XXS.gguf","size":6500000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 6.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (the smallest real quant, not the projector)", got.SizeGB, want)
	}
	if got.Variants != 2 {
		t.Errorf("Variants = %d, want 2: the projector is not a choice", got.Variants)
	}
}

// Mistral keeps a single-file consolidated.safetensors beside the sharded copy
// of the same weights. Summing both reports exactly double --
// mistralai/Mistral-7B-Instruct-v0.3 measured 29.0 GB when it is 14.5 GB.
func TestWeightsSizeDropsDuplicateRootCopies(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"consolidated.safetensors","size":14500000000},
		{"type":"file","path":"model-00001-of-00002.safetensors","size":10000000000},
		{"type":"file","path":"model-00002-of-00002.safetensors","size":4500000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 14.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (one copy, not both)", got.SizeGB, want)
	}
}

// The same weights as .bin and .safetensors, which older repos ship together.
func TestWeightsSizePrefersSafetensorsOverBin(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"pytorch_model.bin","size":8000000000},
		{"type":"file","path":"model.safetensors","size":8000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 8.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v", got.SizeGB, want)
	}
}

// Sub-folders that are parts of one model rather than choices between models.
// Taking the smallest here would report the VAE as the whole thing.
func TestWeightsSizeSumsComponentFolders(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"text_encoder/model.safetensors","size":250000000},
		{"type":"file","path":"transformer/model.safetensors","size":23800000000},
		{"type":"file","path":"vae/model.safetensors","size":170000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 24.22; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (all three components)", got.SizeGB, want)
	}
	if got.Variants != 1 {
		t.Errorf("Variants = %d, want 1: components are not alternatives", got.Variants)
	}
}

// A root-level layout wins outright: original/ ships the same weights in
// another format, so counting it too would double the answer.
func TestWeightsSizeIgnoresDuplicateFormatFolder(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"model.safetensors","size":8000000000},
		{"type":"file","path":"original/consolidated.00.pth","size":16000000000},
		{"type":"file","path":"original/consolidated.01.bin","size":16000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 8.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (root only)", got.SizeGB, want)
	}
}

func TestWeightsSizeWithoutWeightFiles(t *testing.T) {
	serveTree(t, `[{"type":"file","path":"original/consolidated.pth","size":1000000}]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if got.SizeGB != 0 {
		t.Errorf("SizeGB = %v, want 0 when nothing matches a weight extension", got.SizeGB)
	}
}

// Big repos come back a page at a time behind a Link header; missing the
// second page would silently undercount a sharded model.
func TestWeightsSizeFollowsPagination(t *testing.T) {
	var srv *httptest.Server
	srv = newHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link", `<`+srv.URL+`/api/models/`+repo+`/tree/main?cursor=next>; rel="next"`)
			_, _ = w.Write([]byte(`[{"type":"file","path":"model-00001-of-00002.safetensors","size":4000000000}]`))
			return
		}
		_, _ = w.Write([]byte(`[{"type":"file","path":"model-00002-of-00002.safetensors","size":2500000000}]`))
	}))
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 6.5; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (both pages)", got.SizeGB, want)
	}
}

// A cursor that never advances would otherwise have us add the same shards in
// on every pass, multiplying the total by the page budget.
func TestWeightsSizeStopsOnLoopingCursor(t *testing.T) {
	var srv *httptest.Server
	srv = newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Link", `<`+srv.URL+`/api/models/`+repo+`/tree/main?recursive=true&cursor=same>; rel="next"`)
		_, _ = w.Write([]byte(`[{"type":"file","path":"model.safetensors","size":8000000000}]`))
	}))
	_, err := WeightsSize(repo)
	if err == nil {
		t.Fatal("want an error when the cursor never advances, got none")
	}
	if !strings.Contains(err.Error(), "more than") {
		t.Errorf("error = %v, want it to report the page limit", err)
	}
}

// Truncating a listing undercounts, and an undercount is the wrong answer that
// tells someone a model fits when it does not. Better to say nothing.
func TestWeightsSizeFailsRatherThanTruncate(t *testing.T) {
	var srv *httptest.Server
	page := 0
	srv = newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		page++
		w.Header().Set("Link", fmt.Sprintf(`<%s/api/models/%s/tree/main?recursive=true&cursor=%d>; rel="next"`, srv.URL, repo, page))
		_, _ = w.Write([]byte(fmt.Sprintf(`[{"type":"file","path":"shard-%d.safetensors","size":1000000000}]`, page)))
	}))
	if _, err := WeightsSize(repo); err == nil {
		t.Fatal("want an error when the repo exceeds the page budget, got none")
	}
}

// Precision-named folders holding safetensors rather than GGUFs. Nothing else
// in this file reaches the quant-name half of the alternative test, so without
// this the rule could be deleted and the suite would still pass.
func TestWeightsSizeReadsQuantNamedFolders(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"bf16/model.safetensors","size":60000000000},
		{"type":"file","path":"fp8/model.safetensors","size":30000000000},
		{"type":"file","path":"int4/model.safetensors","size":15000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 15.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (the int4 copy)", got.SizeGB, want)
	}
	if got.Variants != 3 {
		t.Errorf("Variants = %d, want 3", got.Variants)
	}
}

// GGUFs inside folders named for parts of a model. Reading these as choices
// would report the VAE as though it were the whole thing.
func TestWeightsSizeSumsGGUFComponentFolders(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"transformer/model-Q4_K_M.gguf","size":20000000000},
		{"type":"file","path":"text_encoder/t5-Q8_0.gguf","size":5000000000},
		{"type":"file","path":"vae/vae-F16.gguf","size":300000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 25.3; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (all three parts)", got.SizeGB, want)
	}
	if got.Variants != 1 {
		t.Errorf("Variants = %d, want 1", got.Variants)
	}
}

// A stray small file must not beat the real weights. Preferring safetensors
// outright reported the 0.3 GB one and called a 140 GB model a fit.
func TestWeightsSizeIgnoresStrayRootFile(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"pytorch_lora_weights.safetensors","size":300000000},
		{"type":"file","path":"pytorch_model-00001-of-00002.bin","size":90000000000},
		{"type":"file","path":"pytorch_model-00002-of-00002.bin","size":50000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 140.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (the .bin shards)", got.SizeGB, want)
	}
}

// Root weights and root GGUF quants in one repo: the quants are real options,
// so the smallest of everything is the answer rather than the root set.
func TestWeightsSizeComparesRootTensorsWithRootGGUFs(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"model.safetensors","size":60000000000},
		{"type":"file","path":"model-Q4_K_M.gguf","size":20000000000},
		{"type":"file","path":"model-Q8_0.gguf","size":35000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 20.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v", got.SizeGB, want)
	}
	if got.Variants != 3 {
		t.Errorf("Variants = %d, want 3", got.Variants)
	}
}

// Quant folders beside a duplicate-format folder. The odd one out must not
// turn the whole repo into a single summed model.
func TestWeightsSizeKeepsQuantsBesideAnotherFolder(t *testing.T) {
	serveTree(t, `[
		{"type":"file","path":"UD-IQ1_S/model.gguf","size":9000000000},
		{"type":"file","path":"UD-Q4_K_M/model.gguf","size":20000000000},
		{"type":"file","path":"original/consolidated.safetensors","size":60000000000}
	]`)
	got, err := WeightsSize(repo)
	if err != nil {
		t.Fatalf("WeightsSize: %v", err)
	}
	if want := 9.0; got.SizeGB != want {
		t.Errorf("SizeGB = %v, want %v (smallest quant, not the sum)", got.SizeGB, want)
	}
}

func TestNextPageURL(t *testing.T) {
	const hub = "https://hub"
	tests := []struct {
		name, header, want string
	}{
		{name: "empty", header: "", want: ""},
		{name: "next", header: `<https://hub/x?cursor=abc>; rel="next"`, want: "https://hub/x?cursor=abc"},
		{name: "ignores other rels", header: `<https://hub/p>; rel="prev"`, want: ""},
		{
			name:   "picks next among several",
			header: `<https://hub/p>; rel="prev", <https://hub/n>; rel="next"`,
			want:   "https://hub/n",
		},
		{name: "unbracketed is ignored", header: `https://hub/n; rel="next"`, want: ""},
		// A response does not get to send us somewhere else.
		{name: "off-host target is refused", header: `<https://evil.example/n>; rel="next"`, want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPageURL(tt.header, hub); got != tt.want {
				t.Errorf("nextPageURL(%q) = %q, want %q", tt.header, got, tt.want)
			}
		})
	}
}

func TestContextLimit(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    int
		wantErr bool
	}{
		{name: "reads max_position_embeddings", body: `{"max_position_embeddings":40960}`, want: 40960},
		{name: "absent field is zero", body: `{"model_type":"qwen3"}`, want: 0},
		{name: "malformed config", body: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			got, err := ContextLimit(repo)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ContextLimit: %v", err)
			}
			if got != tt.want {
				t.Errorf("ContextLimit = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestContextLimitMissingConfig(t *testing.T) {
	newHub(t, status(http.StatusNotFound))
	if _, err := ContextLimit(repo); err == nil {
		t.Error("expected an error when the model has no config.json")
	}
}

// Topology flags describe someone else's deployment, so only the flags that
// belong to the model itself come out -- with the context clamped to what
// config.json actually supports.
func TestDetectFlags(t *testing.T) {
	serveTestdata(t, map[string]string{
		"/" + repo + "/raw/main/README.md":   "README.md",
		"/" + repo + "/raw/main/config.json": "config.json",
	})

	d, err := DetectFlags(repo)
	if err != nil {
		t.Fatalf("DetectFlags: %v", err)
	}
	want := `--enable-auto-tool-choice --tool-call-parser hermes --reasoning-parser deepseek_v3 ` +
		`--enable-prefix-caching --default-chat-template-kwargs {"thinking":true}`
	if d.ExtraArgs != want {
		t.Errorf("ExtraArgs = %q, want %q", d.ExtraArgs, want)
	}
	if d.MaxModelLen != "40960" {
		t.Errorf("MaxModelLen = %q, want the card's 131072 clamped to the model's 40960", d.MaxModelLen)
	}
	for _, unwanted := range []string{"--tensor-parallel-size", "--host", "--port", "--served-model-name"} {
		if strings.Contains(d.ExtraArgs, unwanted) {
			t.Errorf("%s describes someone else's deployment and shouldn't be adopted: %q", unwanted, d.ExtraArgs)
		}
	}
}

// A card with no snippet still has to pick up the model's real context
// limit. Skipping the clamp here is what let facebook/opt-125m be created
// with the generic 131072 default, which vLLM refuses to start against
// (max_position_embeddings=2048). See issue #13.
func TestDetectFlagsWithoutASnippetStillClampsContext(t *testing.T) {
	serveTestdata(t, map[string]string{
		"/" + repo + "/raw/main/README.md":   "README_no_snippet.md",
		"/" + repo + "/raw/main/config.json": "config_small_context.json",
	})
	d, err := DetectFlags(repo)
	if err != nil {
		t.Fatalf("DetectFlags: %v", err)
	}
	if d.ExtraArgs != "" {
		t.Errorf("ExtraArgs = %q, want empty: there is no snippet to adopt flags from", d.ExtraArgs)
	}
	if d.MaxModelLen != "2048" {
		t.Errorf("MaxModelLen = %q, want the model's own 2048", d.MaxModelLen)
	}
}

// Without a readable config.json there is no ceiling to clamp to, so the
// value stays empty and profile.Create's default applies.
func TestDetectFlagsWithoutASnippetOrConfig(t *testing.T) {
	newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("# A model card with no serving instructions.\n"))
	}))
	d, err := DetectFlags(repo)
	if err != nil {
		t.Fatalf("DetectFlags: %v", err)
	}
	if d.ExtraArgs != "" || d.MaxModelLen != "" {
		t.Errorf("DetectFlags = %+v, want everything empty", d)
	}
}

func TestDetectFlagsMissingCard(t *testing.T) {
	newHub(t, status(http.StatusNotFound))
	if _, err := DetectFlags(repo); err == nil {
		t.Error("expected an error when the model card is missing")
	}
}

func TestWhoAmI(t *testing.T) {
	var gotAuth string
	newHub(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotAuth != "Bearer hf_good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"type":"user","name":"chris","fullname":"Chris"}`))
	}))

	name, err := WhoAmI("hf_good")
	if err != nil {
		t.Fatalf("WhoAmI: %v", err)
	}
	if name != "chris" {
		t.Errorf("WhoAmI = %q, want %q", name, "chris")
	}
	if gotAuth != "Bearer hf_good" {
		t.Errorf("Authorization = %q, want a bearer token", gotAuth)
	}

	if _, err := WhoAmI("hf_bad"); err == nil || !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %v, want the rejection reported as 401", err)
	}
}

func TestWhoAmINon200(t *testing.T) {
	newHub(t, status(http.StatusBadGateway))
	if _, err := WhoAmI("hf_good"); err == nil || !strings.Contains(err.Error(), "HTTP 502") {
		t.Errorf("error = %v, want it to report HTTP 502", err)
	}
}

func TestDetectChatTemplatePatch(t *testing.T) {
	tests := []struct {
		name    string
		file    string
		wantOK  bool
		missing bool
	}{
		{name: "template that crashes on a late system message", file: "chat_template_broken.jinja", wantOK: true},
		{name: "template without the check", file: "chat_template_clean.jinja"},
		{name: "model with no separate template file", missing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.missing {
				newHub(t, status(http.StatusNotFound))
			} else {
				serveTestdata(t, map[string]string{"/" + repo + "/raw/main/chat_template.jinja": tt.file})
			}
			patched, ok, err := DetectChatTemplatePatch(repo)
			if err != nil {
				t.Fatalf("DetectChatTemplatePatch: %v", err)
			}
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				if patched != "" {
					t.Errorf("patched = %q, want empty when there's nothing to fix", patched)
				}
				return
			}
			if strings.Contains(patched, "raise_exception") {
				t.Error("the patched template still raises")
			}
			if !strings.Contains(patched, `message.role == "system"`) {
				t.Error("the patch should leave the rest of the template alone")
			}
		})
	}
}

func TestPatchSystemMessageCheck(t *testing.T) {
	t.Parallel()

	const withCheck = `{%- for message in messages %}
    {%- set content = render_content(message.content, true)|trim %}
    {%- if message.role == "system" %}
        {%- if not loop.first %}
            {{- raise_exception('System message must be at the beginning.') }}
        {%- endif %}
    {%- elif message.role == "user" %}
        {{- '<|im_start|>' + message.role + '\n' + content + '<|im_end|>' + '\n' }}
    {%- endif %}
{%- endfor %}`

	t.Run("patches the exact known shape", func(t *testing.T) {
		t.Parallel()
		patched, ok, err := patchSystemMessageCheck(withCheck)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !ok {
			t.Fatal("expected ok=true, got false")
		}
		if strings.Contains(patched, "raise_exception") {
			t.Error("patched template still contains the raise_exception call")
		}
		if !strings.Contains(patched, `if not loop.first`) {
			t.Error("patch should leave the surrounding if/endif guard intact")
		}
		if !strings.Contains(patched, `message.role == "system"`) {
			t.Error("patch should leave unrelated template logic untouched")
		}
	})

	t.Run("no-op when the check isn't present", func(t *testing.T) {
		t.Parallel()
		const clean = `{%- for message in messages %}
    {{- message.content }}
{%- endfor %}`
		patched, ok, err := patchSystemMessageCheck(clean)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ok {
			t.Error("expected ok=false for a template without the issue")
		}
		if patched != "" {
			t.Error("expected empty patched output when ok=false")
		}
	})

	t.Run("errors instead of silently no-op when the string is present but the shape doesn't match", func(t *testing.T) {
		t.Parallel()
		const weirdShape = "System message must be at the beginning. (mentioned in a comment, not the real check)"
		_, ok, err := patchSystemMessageCheck(weirdShape)
		if ok {
			t.Error("expected ok=false when the regex can't safely match")
		}
		if err == nil {
			t.Error("expected an error flagging the unmatched shape, got nil")
		}
	})
}

func TestClampContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		value string
		limit int
		want  string
	}{
		{"caps a card that oversells the context", "131072", 40960, "40960"},
		{"leaves a value the model supports alone", "32768", 40960, "32768"},
		{"fills in the limit when the card says nothing", "", 40960, "40960"},
		{"keeps the value when the limit is unknown", "131072", 0, "131072"},
		{"allows exactly the limit", "40960", 40960, "40960"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := clampContext(tc.value, tc.limit); got != tc.want {
				t.Errorf("clampContext(%q, %d) = %q, want %q", tc.value, tc.limit, got, tc.want)
			}
		})
	}
}

func TestModelLicenseAndQuantTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		tags      []string
		want      string
		wantQuant []string
	}{
		{"license tag", []string{"license:mit", "fp8"}, "mit", []string{"fp8"}},
		{"no license tag", []string{"text-generation"}, "unknown", nil},
		{"quant tags are case-insensitive", []string{"NVFP4", "GGUF"}, "unknown", []string{"NVFP4", "GGUF"}},
		{"no tags at all", nil, "unknown", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			m := Model{Tags: tt.tags}
			if got := m.License(); got != tt.want {
				t.Errorf("License() = %q, want %q", got, tt.want)
			}
			if got := m.QuantTags(); !reflect.DeepEqual(got, tt.wantQuant) {
				t.Errorf("QuantTags() = %#v, want %#v", got, tt.wantQuant)
			}
		})
	}
}
