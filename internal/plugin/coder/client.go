// Package coder syncs a Coder deployment's AI-model picker with local
// profiles: each profile gets one persistent model-config entry, and
// switching flips enabled/is_default so only the loaded model is selectable.
// Sync failures never block local operations.
//
// The endpoint is Coder's chats API, which has changed shape more than once:
// the provider moved from a name to a UUID, and the collection itself moved
// from /api/experimental/chats/model-configs to an organization-scoped
// /api/v2/organizations/{org}/chats/models. Keeping it behind the plugin seam
// means that churn lands in this package alone.
package coder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// ErrNotFound reports that the API has no such entry. Callers recover from it
// rather than failing: a saved model-config ID outlives the entry it names
// when someone deletes the model in Coder's UI, or when the deployment behind
// CODER_URL changes, and re-creating is what the user wanted either way.
var ErrNotFound = errors.New("no such entry")

// The two places the model-config collection has lived. Newer deployments
// serve only the first: Coder's router reserves the old path and answers it
// with a 404 rather than letting it fall through, so a client that knows only
// the old one fails with no hint that the route simply moved.
const (
	routeOrg = "/api/v2/organizations/%s/chats/models"
	routeExp = "/api/experimental/chats/model-configs"
)

// defaultOrg is the organization the models hang off. Coder resolves the
// literal "default" server-side, which avoids having to look up a UUID that
// single-organization deployments never needed to know.
const defaultOrg = "default"

type Client struct {
	URL        string
	Token      string
	ProviderID string
	HTTP       *http.Client

	// route is resolved once, on first use, by asking the deployment which
	// collection it serves. Cached because every sync makes several calls
	// and the answer cannot change underneath a single command.
	routeOnce sync.Once
	route     string
}

func NewClient(url, token, providerID string) *Client {
	return &Client{
		URL:        url,
		Token:      token,
		ProviderID: providerID,
		HTTP:       &http.Client{Timeout: 15 * time.Second},
	}
}

// ModelConfig is one entry in the deployment's model picker.
//
// The pointer fields are what the newer API calls optional: nil preserves the
// stored value on a PATCH. They are always sent by the callers here, which
// both APIs accept -- the older one required them, and the newer one treats an
// explicit value as an explicit value.
type ModelConfig struct {
	ID           string `json:"id,omitempty"`
	AIProviderID string `json:"ai_provider_id,omitempty"`
	Model        string `json:"model"`
	DisplayName  string `json:"display_name"`
	ContextLimit int64  `json:"context_limit"`
	Enabled      bool   `json:"enabled"`
	IsDefault    bool   `json:"is_default"`
}

// resolveRoute asks the deployment which collection path it serves.
//
// A GET of the organization-scoped path answers definitively: anything but a
// 404 means the route exists (401 and 403 say the path is real and the token
// is not, which is a different problem and reported by Check). Only a 404
// sends us to the pre-2.37 path.
func (c *Client) resolveRoute() string {
	c.routeOnce.Do(func() {
		c.route = fmt.Sprintf(routeOrg, defaultOrg)
		req, err := http.NewRequest("GET", c.URL+c.route, nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Coder-Session-Token", c.Token)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			// Unreachable deployments are reported by the call that
			// follows; guessing the older route here would only change
			// which URL appears in that error.
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			c.route = routeExp
		}
	})
	return c.route
}

func (c *Client) do(method, path string, body any, out any) error {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return err
		}
	}
	req, err := http.NewRequest(method, c.URL+c.resolveRoute()+path, &buf)
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
	if resp.StatusCode == http.StatusNotFound && path != "" {
		return fmt.Errorf("coder API %s %s: %w", method, c.resolveRoute()+path, ErrNotFound)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("coder API %s %s: HTTP %d", method, c.resolveRoute()+path, resp.StatusCode)
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// Check verifies the URL, token, and API availability by listing the
// deployment's model-configs. It doesn't validate the provider ID (the API
// has no cheap probe for that), but it catches the common failures: wrong
// URL, expired token, or a Coder build with neither collection route.
func (c *Client) Check() error {
	return c.do("GET", "", nil, nil)
}

// createResponse is the entry the API echoes back. Only the ID is read: the
// rest is what we just sent, and decoding fields we do not use is how a client
// breaks on a field that changed shape somewhere else in the payload.
type createResponse struct {
	ID string `json:"id"`
}

// Create posts a new model-config entry and returns its ID.
func (c *Client) Create(mc ModelConfig) (string, error) {
	mc.AIProviderID = c.ProviderID
	var created createResponse
	if err := c.do("POST", "", mc, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		// The entry exists on the deployment but we cannot name it, so it
		// can never be updated or disabled later. Better to say so than to
		// record nothing and create a second one on the next sync.
		return "", fmt.Errorf("coder API created the entry but returned no id")
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
