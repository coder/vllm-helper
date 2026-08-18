package hf

import (
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

func TestWeightsSizeGB(t *testing.T) {
	serveTestdata(t, map[string]string{"/api/models/" + repo + "/tree/main": "tree.json"})

	got, err := WeightsSizeGB(repo)
	if err != nil {
		t.Fatalf("WeightsSizeGB: %v", err)
	}
	// Only the two safetensors shards count, not the tokenizer or the index.
	if want := 6.5; got != want {
		t.Errorf("WeightsSizeGB = %v, want %v", got, want)
	}
}

func TestWeightsSizeGBWithoutRootWeights(t *testing.T) {
	newHub(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"path":"original/consolidated.pth","size":1000000}]`))
	}))
	got, err := WeightsSizeGB(repo)
	if err != nil {
		t.Fatalf("WeightsSizeGB: %v", err)
	}
	if got != 0 {
		t.Errorf("WeightsSizeGB = %v, want 0 when the weights live in a subfolder", got)
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
