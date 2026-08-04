// Package coder syncs a Coder deployment's AI-model picker with local
// profiles: each profile gets one persistent model-config entry, and
// switching flips enabled/is_default so only the loaded model is selectable.
// Sync failures never block local operations.
package coder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Client struct {
	URL        string
	Token      string
	ProviderID string
	HTTP       *http.Client
}

func NewClient(url, token, providerID string) *Client {
	return &Client{
		URL:        url,
		Token:      token,
		ProviderID: providerID,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}
}

type ModelConfig struct {
	ID           string `json:"id,omitempty"`
	AIProviderID string `json:"ai_provider_id,omitempty"`
	Model        string `json:"model"`
	DisplayName  string `json:"display_name"`
	ContextLimit int64  `json:"context_limit"`
	Enabled      bool   `json:"enabled"`
	IsDefault    bool   `json:"is_default"`
}

func (c *Client) do(method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.URL+"/api/experimental/chats/model-configs"+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Coder-Session-Token", c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coder API %s %s: HTTP %d", method, path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Check verifies the URL, token, and API availability by listing the
// deployment's model-configs. It doesn't validate the provider ID (the API
// has no cheap probe for that), but it catches the common failures: wrong
// URL, expired token, or a Coder build without the experimental chats API.
func (c *Client) Check() error {
	return c.do("GET", "", nil, nil)
}

// Create posts a new model-config entry and returns its ID.
func (c *Client) Create(mc ModelConfig) (string, error) {
	mc.AIProviderID = c.ProviderID
	var created ModelConfig
	if err := c.do("POST", "", mc, &created); err != nil {
		return "", err
	}
	return created.ID, nil
}

// Update patches an existing entry.
func (c *Client) Update(id string, mc ModelConfig) error {
	return c.do("PATCH", "/"+id, mc, nil)
}

// Disable flips an entry to disabled/non-default without touching its model.
func (c *Client) Disable(id string) error {
	return c.do("PATCH", "/"+id, map[string]bool{"enabled": false, "is_default": false}, nil)
}

// Delete removes an entry entirely.
func (c *Client) Delete(id string) error {
	return c.do("DELETE", "/"+id, nil, nil)
}
