package detect

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	ep := Endpoint{Port: port}

	if resp, err := probeClient.Get(fmt.Sprintf("http://localhost:%s/health", port)); err == nil {
		resp.Body.Close()
		ep.Healthy = resp.StatusCode == http.StatusOK
	}

	req, _ := http.NewRequest("GET", fmt.Sprintf("http://localhost:%s/v1/models", port), nil)
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
