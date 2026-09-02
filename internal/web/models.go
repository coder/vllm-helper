package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/vllm-helper/internal/catalog"
	"github.com/coder/vllm-helper/internal/control"
	"github.com/coder/vllm-helper/internal/hf"
	"github.com/coder/vllm-helper/internal/profile"
)

// hubTimeout bounds a call that leaves this machine. The Hub client already
// times out per request, but inspecting a repo makes several -- a large tree
// is walked a page at a time -- so the whole operation needs its own ceiling
// or a slow Hub holds a browser request open indefinitely.
const hubTimeout = 90 * time.Second

// searchLimit matches the CLI's. Long enough to find what you meant, short
// enough to read.
const searchLimit = 25

// repoParam pulls a repo id out of the request and checks it is one.
//
// Everything downstream treats this string as trustworthy: it goes into a URL
// sent to the Hub, and profile.NameFromRepo turns it into a filename. This is
// where that trust is established.
func repoParam(r *http.Request) (string, error) {
	repo := strings.TrimSpace(r.URL.Query().Get("repo"))
	if repo == "" {
		return "", errors.New("no model given")
	}
	if !catalog.ValidRepo(repo) {
		return "", fmt.Errorf("%q is not a HuggingFace repo id -- expected something like org/model-name", repo)
	}
	return repo, nil
}

// budget is the configured weights budget, or 0 when there isn't one.
func (s *Server) budget() float64 {
	gb, _ := strconv.ParseFloat(s.cfg.ModelBudgetGB(), 64)
	return gb
}

type searchResponse struct {
	Query   string           `json:"query"`
	Results []catalog.Result `json:"results"`
}

// handleSearch queries the Hub. Read-only and outbound: it tells the caller
// nothing about this machine, so it is a plain GET.
func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "type something to search for"})
		return
	}
	models, err := hf.Search(query, searchLimit)
	if err != nil {
		// The Hub being unreachable is the common case here and is not this
		// machine's fault, so say which side failed.
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "HuggingFace search failed (network or rate limit): " + err.Error(),
		})
		return
	}
	out := make([]catalog.Result, 0, len(models))
	for _, m := range models {
		out = append(out, catalog.ToResult(m))
	}
	writeJSON(w, http.StatusOK, searchResponse{Query: query, Results: out})
}

// inspectResponse adds what only this machine knows: whether a profile for
// this model already exists, so the UI can offer "switch to it" rather than
// a pull that would fail as a duplicate.
type inspectResponse struct {
	catalog.Detail
	ExistingProfile string `json:"existing_profile,omitempty"`
}

// handleInspect measures one model against this machine.
func (s *Server) handleInspect(w http.ResponseWriter, r *http.Request) {
	repo, err := repoParam(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), hubTimeout)
	defer cancel()

	type result struct {
		d   catalog.Detail
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := hf.Lookup(repo)
		if err != nil {
			ch <- result{err: err}
			return
		}
		ch <- result{d: catalog.Inspect(m, s.budget(), s.caps())}
	}()

	select {
	case <-ctx.Done():
		writeJSON(w, http.StatusGatewayTimeout, map[string]string{
			"error": "HuggingFace did not answer in time -- try again",
		})
	case res := <-ch:
		if res.err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "could not read " + repo + " from HuggingFace: " + res.err.Error(),
			})
			return
		}
		out := inspectResponse{Detail: res.d}
		name := profile.NameFromRepo(repo)
		if _, err := profile.Load(s.cfg.Dirs, name); err == nil {
			out.ExistingProfile = name
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// pullResponse reports what was saved and what was noticed doing it.
type pullResponse struct {
	OK       bool     `json:"ok"`
	Profile  string   `json:"profile"`
	Existing bool     `json:"existing"`
	Notes    []string `json:"notes,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// handlePull saves a profile for a model and switches to it.
//
// No weights are downloaded here. vLLM fetches them itself when it starts,
// which is why this returns as soon as the service has been told to restart:
// the download shows up as the ordinary "loading" phase, and the panel
// already knows how to display that.
func (s *Server) handlePull(w http.ResponseWriter, r *http.Request) {
	repo, err := repoParam(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if privileged, reason := s.privilege(); !privileged {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": reason})
		return
	}

	// An explicit quant is resolved against what the repo actually holds,
	// so a stale page cannot ask for one that is gone -- or one that vLLM
	// has no way to load.
	var choice *catalog.Choice
	if want := strings.TrimSpace(r.URL.Query().Get("quant")); want != "" {
		m, err := hf.Lookup(repo)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{
				"error": "could not re-read " + repo + " from HuggingFace: " + err.Error()})
			return
		}
		d := catalog.Inspect(m, s.budget(), s.caps())
		v, ok := catalog.FindVariant(d.All, want)
		switch {
		case !ok:
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": repo + " has no quant called " + want})
			return
		case !v.Servable():
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": want + " is " + v.Blocked})
			return
		case v.GGUF && !d.GGUFReady:
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "this vLLM cannot load GGUF -- install vllm-gguf-plugin first"})
			return
		}
		choice = &catalog.Choice{Variant: v, BaseModel: d.BaseModel}
	}

	name := profile.NameFromRepo(repo)
	if choice != nil {
		if _, ok := choice.Variant.Selector(); ok {
			name += "-" + strings.ToLower(choice.Variant.Label)
		}
	}
	out := pullResponse{OK: true, Profile: name}

	// An existing profile is reused rather than overwritten. Someone may
	// have edited it into something that works, and silently replacing that
	// with freshly-guessed defaults would be the worst kind of helpful.
	if _, err := profile.Load(s.cfg.Dirs, name); err == nil {
		out.Existing = true
		out.Notes = append(out.Notes,
			"Profile '"+name+"' already existed -- switching to it with its saved settings.")
	} else {
		plan := catalog.PrepareProfile(s.cfg.Dirs, repo, choice, s.caps())
		out.Notes = append(out.Notes, plan.Notes...)
		if _, err := profile.Create(s.cfg.Dirs, plan.Opts); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
	}

	warnings, err := s.activate(r.Context(), name)
	if err != nil {
		// The profile is saved either way: a failed restart is worth
		// reporting, but it does not make the profile wrong.
		writeJSON(w, http.StatusInternalServerError, map[string]string{
			"error": "saved profile '" + name + "' but could not switch to it: " + err.Error(),
		})
		return
	}
	out.Warnings = warnings
	writeJSON(w, http.StatusOK, out)
}

// handleRemoveProfile deletes a saved model, and its Coder entry when sync is
// configured.
func (s *Server) handleRemoveProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if _, err := profile.Load(s.cfg.Dirs, name); err != nil {
		// Load resolves the name against the profiles directory, so this
		// also rejects anything that isn't a profile that exists.
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	if name == s.cfg.ActiveProfile() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "'" + name + "' is the active model -- switch to something else first",
		})
		return
	}
	// Tell the plugins before the file goes, since they key off the name.
	var pluginOut bytes.Buffer
	control.NotifyRemoved(r.Context(), s.cfg, name, &pluginOut)
	if err := profile.Remove(s.cfg.Dirs, name); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "removed": name})
}
