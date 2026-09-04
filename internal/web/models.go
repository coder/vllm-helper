package web

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coder/vllm-helper/internal/catalog"
	"github.com/coder/vllm-helper/internal/control"
	"github.com/coder/vllm-helper/internal/detect"
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

// budget is this machine's weights budget, or the configured override.
func (s *Server) budget() float64 { return detect.BudgetGB(s.cfg.ModelBudgetGB()) }

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
		ch <- result{d: catalog.Inspect(m, s.budget(), s.caps(), s.cfg.KVContextTarget())}
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
		d := catalog.Inspect(m, s.budget(), s.caps(), s.cfg.KVContextTarget())
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
		case v.GGUF && d.ArchBlocked != "":
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": repo + " can't be served as GGUF -- it is " + d.ArchBlocked})
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
		plan := catalog.PrepareProfile(s.cfg.Dirs, repo, choice, s.caps(), s.cfg.KVContextTarget())
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

// ggufResponse describes the GGUF gap and what closing it would run, so the
// panel can show the same command and caveats the CLI does before anyone
// agrees to it.
type ggufResponse struct {
	Supported bool     `json:"supported"`
	Command   string   `json:"command,omitempty"`
	Caveats   []string `json:"caveats,omitempty"`
	// Reason is why installing is not offered here, when it isn't.
	Reason string `json:"reason,omitempty"`
	// Installing is true while a background install runs, so a page that was
	// reloaded -- or opened in a second tab -- still learns that one is under
	// way rather than offering to start another.
	Installing bool `json:"installing"`
	// Elapsed lets the page say how long it has been going, since a source
	// build is minutes and silence for that long reads as a hang.
	Elapsed int    `json:"elapsed_seconds,omitempty"`
	Error   string `json:"install_error,omitempty"`
	Log     string `json:"install_log,omitempty"`
}

func (s *Server) handleGGUF(w http.ResponseWriter, _ *http.Request) {
	st := s.installStatus()
	r := ggufResponse{
		Supported:  s.caps().SupportsGGUF(),
		Command:    control.GGUFInstallCommand(s.cfg),
		Caveats:    control.GGUFCaveats(),
		Installing: st.Running,
		Error:      st.Err,
	}
	if st.Running {
		r.Elapsed = int(time.Since(st.Started).Seconds())
	}
	// The log is only worth returning when it explains a failure. pip is
	// verbose and a successful build has nothing anyone needs to read.
	if st.Err != "" {
		r.Log = st.Log
	}
	if r.Command == "" {
		r.Reason = "couldn't find the interpreter next to " + s.cfg.VLLMBin() +
			" -- install the plugin by hand"
	}
	writeJSON(w, http.StatusOK, r)
}

// handleInstallGGUF starts installing the plugin vLLM needs to read GGUF, and
// returns immediately.
//
// It does not wait. The plugin builds a CUDA extension from source, which is
// minutes: a POST held open that long is dropped by browsers and proxies, and
// when it is, the page cannot tell a finished install from a lost one. So the
// work outlives the request and the page polls /api/gguf for the outcome --
// which also means a reload, or a second tab, sees the same truth.
//
// Deliberately not behind the privilege check the service controls use: this
// writes to the vLLM environment, not to systemd, and on a normal install that
// venv belongs to the user running the panel. Requiring root here would push
// people towards running the whole panel as root, which is worse.
//
// It also cannot disturb anything running. The plugin is read when an engine
// starts, so it takes effect on the next switch -- unlike every other mutation
// here, this one leaves the current model alone.
func (s *Server) handleInstallGGUF(w http.ResponseWriter, _ *http.Request) {
	if s.caps().SupportsGGUF() {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": true, "already": true,
			"message": "This vLLM already loads GGUF.",
		})
		return
	}
	if control.GGUFInstallCommand(s.cfg) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "couldn't find the interpreter next to " + s.cfg.VLLMBin() +
				" -- install the plugin by hand",
		})
		return
	}
	if !s.beginInstall() {
		writeJSON(w, http.StatusConflict, map[string]string{
			"error": "an install is already running",
		})
		return
	}

	// Detached from the request context on purpose: cancelling a half-finished
	// pip because someone closed a tab would leave site-packages in a state
	// nobody asked for.
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(),
			control.GGUFInstallTimeout*time.Second)
		defer cancel()

		var out bytes.Buffer
		err := control.InstallGGUF(ctx, s.cfg, &out)
		if err == nil {
			// Keyed by vLLM's version, which installing a plugin does not
			// change, so it would otherwise keep reporting the old answer.
			control.ForgetCaps(s.cfg.Dirs)
			if !s.caps().SupportsGGUF() {
				err = errors.New("installed, but vLLM still doesn't register the " +
					"GGUF plugin -- it may need a matching vLLM version")
			}
		}
		s.finishInstall(err, lastLines(out.String(), 20))
	}()

	writeJSON(w, http.StatusAccepted, map[string]any{
		"ok":      true,
		"started": true,
		"message": "Building from source. This takes a few minutes; nothing running is affected.",
	})
}

// lastLines keeps the tail of a long build log, which is where the error is.
func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
