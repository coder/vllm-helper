package catalog

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setup creates the encodings directory and points vLLM at it, but nothing
// filled it -- so a gpt-oss model started healthy and then failed every
// request with "invalid tiktoken vocab file". Measured on a GB10.
func TestHarmonyVocabIsFetchedWhenMissing(t *testing.T) {
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, strings.TrimPrefix(r.URL.Path, "/"))
		w.Write([]byte("vocab bytes"))
	}))
	defer srv.Close()
	defer swapEncodingsURL(srv.URL + "/")()

	data := t.TempDir()
	if note := ensureHarmonyEncodings(data); note == "" {
		t.Error("fetched the vocab files without saying so")
	}
	for _, name := range harmonyEncodings {
		if _, err := os.Stat(filepath.Join(data, "tiktoken-encodings", name)); err != nil {
			t.Errorf("%s was not written: %v", name, err)
		}
	}

	// A second pull must not refetch: these are several megabytes and do
	// not change.
	before := len(asked)
	if note := ensureHarmonyEncodings(data); note != "" {
		t.Errorf("refetched files already on disk: %q", note)
	}
	if len(asked) != before {
		t.Errorf("made %d more requests for files already present", len(asked)-before)
	}
}

// A failed fetch has to say so: the server will start, look healthy, and then
// refuse every request.
func TestAFailedVocabFetchIsReported(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	defer srv.Close()
	defer swapEncodingsURL(srv.URL + "/")()

	note := ensureHarmonyEncodings(t.TempDir())
	if !strings.Contains(note, "harmony") || !strings.Contains(note, "error") {
		t.Errorf("note = %q, want it to name harmony and the failure", note)
	}
}

// Only gpt-oss-style models read a vocab from disk; nothing else should pay
// for the download.
func TestOnlyHarmonyModelsNeedTheVocab(t *testing.T) {
	for arch, want := range map[string]bool{
		"gpt_oss": true, "qwen3": false, "llama": false, "": false,
	} {
		if got := needsHarmony(arch); got != want {
			t.Errorf("needsHarmony(%q) = %v, want %v", arch, got, want)
		}
	}
}
