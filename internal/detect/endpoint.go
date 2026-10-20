package detect

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Endpoint is an OpenAI-compatible server found by probing localhost ports,
// the catch-all layer for servers whose process we can't see.
type Endpoint struct {
	Port      string   `json:"port"`
	Healthy   bool     `json:"healthy"`    // GET /health returned 200 (vLLM-style health route)
	NeedsAuth bool     `json:"needs_auth"` // /v1/models returned 401/403
	Models    []string `json:"models"`     // model IDs from /v1/models when reachable
}

var probeClient = &http.Client{Timeout: 2 * time.Second}

// ProbePorts checks the given ports for OpenAI-compatible servers. Pass an
// apiKey ("" for none) to see through auth-protected /v1/models.
func ProbePorts(ports []string, apiKey string) []Endpoint {
	var out []Endpoint
	for _, port := range ports {
		if ep, ok := probeOne(port, apiKey); ok {
			out = append(out, ep)
		}
	}
	return out
}

// DefaultPorts are where vLLM-ish servers usually live: vLLM's default
// (8000), our default (8001), and common neighbors.
func DefaultPorts(extra ...string) []string {
	base := []string{"8000", "8001", "8080", "8888", "5000"}
	seen := map[string]bool{}
	var out []string
	for _, p := range append(extra, base...) {
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func probeOne(port, apiKey string) (Endpoint, bool) {
	ep, ok := ProbeURL("http://localhost:"+port, apiKey)
	ep.Port = port
	return ep, ok
}

// ProbeURL checks one server by base URL (scheme://host:port), for a server
// that is not on this machine: a cluster's head can live on another node, and
// asking localhost about it reports a healthy remote endpoint as down.
func ProbeURL(base, apiKey string) (Endpoint, bool) {
	var ep Endpoint
	if u, err := url.Parse(base); err == nil {
		ep.Port = u.Port()
	}
	base = strings.TrimSuffix(base, "/")

	if resp, err := probeClient.Get(base + "/health"); err == nil {
		resp.Body.Close()
		ep.Healthy = resp.StatusCode == http.StatusOK
	}

	req, err := http.NewRequest("GET", base+"/v1/models", nil)
	if err != nil {
		return ep, ep.Healthy
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	resp, err := probeClient.Do(req)
	if err != nil {
		// No listener (or not HTTP) and no health route -> nothing here.
		return ep, ep.Healthy
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.NewDecoder(resp.Body).Decode(&body) == nil {
			for _, m := range body.Data {
				ep.Models = append(ep.Models, m.ID)
			}
		}
		return ep, true
	case http.StatusUnauthorized, http.StatusForbidden:
		ep.NeedsAuth = true
		return ep, true
	default:
		return ep, ep.Healthy
	}
}
