package catalog

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// harmonyEncodings are the vocab files the harmony tokenizer reads, by the
// names it looks for under TIKTOKEN_ENCODINGS_BASE.
var harmonyEncodings = []string{"o200k_base.tiktoken", "cl100k_base.tiktoken"}

var encodingsURL = "https://openaipublic.blob.core.windows.net/encodings/"

// swapEncodingsURL points the fetch at a stand-in and returns a restore func.
func swapEncodingsURL(url string) func() {
	old := encodingsURL
	encodingsURL = url
	return func() { encodingsURL = old }
}

// needsHarmony reports whether a model tokenizes with harmony, which reads its
// vocab from disk rather than from the model repo.
func needsHarmony(modelType string) bool { return modelType == "gpt_oss" }

// ensureHarmonyEncodings downloads the vocab files if they are missing.
//
// setup creates this directory and points vLLM at it, but nothing filled it, so
// a gpt-oss model started healthy and then failed every request with "invalid
// tiktoken vocab file" -- a failure that looks like a broken model rather than
// a missing file. Returns a note for the user, empty when there was nothing to
// do or nothing useful to say.
func ensureHarmonyEncodings(dataDir string) string {
	dir := filepath.Join(dataDir, "tiktoken-encodings")
	var fetched []string
	for _, name := range harmonyEncodings {
		path := filepath.Join(dir, name)
		if st, err := os.Stat(path); err == nil && st.Size() > 0 {
			continue
		}
		if err := download(encodingsURL+name, path); err != nil {
			return fmt.Sprintf(
				"This model tokenizes with harmony, which needs %s in %s. "+
					"Fetching it failed (%v), and requests will return an error until it is there.",
				name, dir, err)
		}
		fetched = append(fetched, name)
	}
	if len(fetched) == 0 {
		return ""
	}
	return fmt.Sprintf("Fetched the harmony vocab files (%v) into %s -- this model reads them from disk.", fetched, dir)
}

func download(url, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	// Written beside the target and renamed, so an interrupted fetch cannot
	// leave a half file that later looks present and passes the size check.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tiktoken-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
