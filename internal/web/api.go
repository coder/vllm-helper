package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"os/user"
	"strconv"
	"time"

	"github.com/coder/vllm-helper/internal/detect"
	"github.com/coder/vllm-helper/internal/profile"
)

// run executes a command and reports only whether it succeeded. Used for
// capability probes, where the output doesn't matter.
func run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	return cmd.Run()
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return "youruser"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// statusResponse is what the panel polls. Deliberately assembled field by
// field rather than marshalling internal structs: config and profiles both
// carry credentials, and an explicit DTO is the only shape where adding a
// secret has to be a conscious act.
type statusResponse struct {
	Service    string `json:"service"`
	State      string `json:"state"`
	Profile    string `json:"profile"`
	Model      string `json:"model"`
	Port       string `json:"port"`
	Healthy    bool   `json:"healthy"`
	HTTPStatus int    `json:"http_status"`
	Crashed    bool   `json:"crashed"`
	Restarts   int    `json:"restarts"`
	FailReason string `json:"fail_reason,omitempty"`
	LastLog    string `json:"last_log,omitempty"`

	// Phase collapses the above into the one thing a person actually wants
	// to know. The distinction that matters is loading vs failing: both
	// leave the endpoint dead, and telling someone to wait for a server
	// that is crash-looping wastes their afternoon.
	Phase string `json:"phase"`

	Privileged bool   `json:"privileged"`
	PrivReason string `json:"priv_reason,omitempty"`
	Build      string `json:"build"`
	Host       string `json:"host"`
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.status())
}

func (s *Server) status() statusResponse {
	h := s.svc.Health()
	r := statusResponse{
		Service:    s.cfg.ServiceName(),
		State:      h.State,
		Crashed:    h.Crashed,
		Restarts:   h.NRestarts,
		FailReason: h.Result,
		Profile:    s.cfg.ActiveProfile(),
		Port:       s.cfg.Port(),
		Model:      "unknown",
		Privileged: s.privileged,
		PrivReason: s.privReason,
		Build:      s.build,
		Host:       hostname(),
	}
	if active, err := parseActive(s.cfg); err == nil {
		if m := active.Get("MODEL"); m != "" {
			r.Model = m
		}
	}
	r.HTTPStatus, r.Healthy = probeHealth(s.cfg.Port())

	switch {
	case r.Healthy:
		r.Phase = "serving"
	case h.Crashed:
		r.Phase = "failing"
		r.LastLog = lastMeaningfulLine(s.svc.LastLogs(40))
	case h.State == "active" || h.State == "activating":
		// The port stays closed for the whole load -- vLLM binds only
		// after the weights are in memory -- so an unresponsive endpoint
		// on a running unit is the normal shape of a load in progress.
		r.Phase = "loading"
	case r.Profile == "":
		r.Phase = "idle"
	default:
		r.Phase = "stopped"
	}
	return r
}

// profileResponse describes one saved model. Note the absence of the raw
// profile file: it is an env file a user may have hand-edited, and anything
// they put in it would end up in a browser.
type profileResponse struct {
	Name        string `json:"name"`
	Model       string `json:"model"`
	DisplayName string `json:"display_name"`
	MaxModelLen string `json:"max_model_len"`
	MemUtil     string `json:"gpu_memory_utilization"`
	ExtraArgs   string `json:"extra_args"`
	Status      string `json:"status"`
	Active      bool   `json:"active"`
}

func (s *Server) handleProfiles(w http.ResponseWriter, _ *http.Request) {
	profiles, err := profile.List(s.cfg.Dirs)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	active := s.cfg.ActiveProfile()
	out := make([]profileResponse, 0, len(profiles))
	for _, p := range profiles {
		out = append(out, profileResponse{
			Name:        p.Name,
			Model:       p.Model(),
			DisplayName: p.DisplayName(),
			MaxModelLen: p.MaxModelLen(),
			MemUtil:     p.File.Get("GPU_MEMORY_UTILIZATION"),
			ExtraArgs:   p.ExtraArgs(),
			Status:      p.Status(),
			Active:      p.Name == active,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// machineResponse is the hardware the panel draws its memory rail from.
type machineResponse struct {
	detect.Hardware
	AvailableMemMB int `json:"available_mem_mb"`
}

func (s *Server) handleMachine(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, machineResponse{
		Hardware:       detect.Probe(),
		AvailableMemMB: detect.AvailableMemMB(),
	})
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	n := 200
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 && parsed <= 2000 {
			n = parsed
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"logs": s.svc.LastLogs(n),
		"hint": s.svc.LogHint(),
	})
}

// actionResponse is the result of something that changes state.
type actionResponse struct {
	OK       bool     `json:"ok"`
	Error    string   `json:"error,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

func (s *Server) handleSwitch(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if !s.requirePrivilege(w) {
		return
	}
	warnings, err := s.activate(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResponse{Error: err.Error(), Warnings: warnings})
		return
	}
	writeJSON(w, http.StatusOK, actionResponse{OK: true, Warnings: warnings})
}

func (s *Server) handleRestart(w http.ResponseWriter, r *http.Request) {
	if !s.requirePrivilege(w) {
		return
	}
	if err := s.privilegedCall(r.Context(), s.svc.Restart); err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, actionResponse{OK: true})
}

func (s *Server) handleStop(w http.ResponseWriter, r *http.Request) {
	if !s.requirePrivilege(w) {
		return
	}
	if err := s.privilegedCall(r.Context(), s.svc.Stop); err != nil {
		writeJSON(w, http.StatusInternalServerError, actionResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, actionResponse{OK: true})
}

func (s *Server) requirePrivilege(w http.ResponseWriter) bool {
	if s.privileged {
		return true
	}
	writeJSON(w, http.StatusForbidden, actionResponse{Error: s.privReason})
	return false
}

// privilegedCall bounds a systemctl call in time. Nothing in the service
// package takes a context, and a systemctl that blocks would otherwise hold
// an HTTP request open indefinitely.
func (s *Server) privilegedCall(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	go func() { done <- fn() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%w -- check 'journalctl -u %s'", err, s.cfg.ServiceName())
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Minute):
		return fmt.Errorf("systemctl did not return within two minutes")
	}
}
