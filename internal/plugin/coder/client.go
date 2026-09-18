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
	"strings"
	"sync"
	"time"
)

// ErrNotFound reports that the API has no such entry. A saved ID outlives its
// entry when the model is deleted in Coder's UI or CODER_URL changes; callers
// recover by re-creating.
var ErrNotFound = errors.New("no such entry")

// The two places the model-config collection has lived. Coder reserves the old
// path and 404s it rather than letting it fall through, so a client that knows
// only that one fails with no hint that the route moved.
const (
	routeOrg = "/api/v2/organizations/%s/chats/models"
	routeExp = "/api/experimental/chats/model-configs"
)

type Client struct {
	URL        string
	Token      string
	ProviderID string
	// Organization the model entries hang off, by name or UUID.
	Organization string
	HTTP         *http.Client

	// route is resolved once by asking the deployment which collection it
	// serves; a sync makes several calls and it cannot change under one.
	routeOnce sync.Once
	route     string
	// fellBack records that the org-scoped path 404'd, which means either an
	// old deployment or a bad organization -- indistinguishable by status.
	fellBack bool
}

func NewClient(url, token, providerID, organization string) *Client {
	return &Client{
		URL:          url,
		Token:        token,
		ProviderID:   providerID,
		Organization: organization,
		HTTP:         &http.Client{Timeout: 15 * time.Second},
	}
}

// ModelConfig is one entry in the deployment's model picker. Every field is
// always sent, which both API generations accept.
//
// Options carries Coder's per-model overrides. It is omitted when empty: an
// absent model_config means the stored options are NULL, and sending an empty
// object would clear whatever an operator set in the UI.
type ModelConfig struct {
	ID           string         `json:"id,omitempty"`
	AIProviderID string         `json:"ai_provider_id,omitempty"`
	Model        string         `json:"model"`
	DisplayName  string         `json:"display_name"`
	ContextLimit int64          `json:"context_limit"`
	Enabled      bool           `json:"enabled"`
	IsDefault    bool           `json:"is_default"`
	Options      map[string]any `json:"model_config,omitempty"`
}

// resolveRoute asks the deployment which collection path it serves. Anything
// but a 404 means the route exists -- 401 and 403 say the path is real and the
// token is not, which Check reports separately.
func (c *Client) resolveRoute() string {
	c.routeOnce.Do(func() {
		org := c.Organization
		if org == "" {
			org = "default"
		}
		c.route = fmt.Sprintf(routeOrg, org)
		req, err := http.NewRequest("GET", c.URL+c.route, nil)
		if err != nil {
			return
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Coder-Session-Token", c.Token)
		resp, err := c.HTTP.Do(req)
		if err != nil {
			return // the call that follows reports it
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound {
			c.route, c.fellBack = routeExp, true
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

// Check verifies the URL, token and API availability. It does not validate the
// provider ID -- the API has no cheap probe for that.
func (c *Client) Check() error {
	err := c.do("GET", "", nil, nil)
	if err != nil && c.fellBack {
		// Both paths gone: on a deployment new enough to have moved the
		// collection, a bad organization gives exactly this pair of 404s.
		org := c.Organization
		if org == "" {
			org = "default"
		}
		return fmt.Errorf("%w (no model-config API found; if this deployment is recent, "+
			"check CODER_ORGANIZATION -- %q did not resolve)", err, org)
	}
	return err
}

// createResponse is the entry the API echoes back. Only the ID is read:
// decoding fields we do not use is how this broke once already.
type createResponse struct {
	ID string `json:"id"`
}

// Entry is one model-config as the API returns it.
type Entry struct {
	ID           string `json:"id"`
	AIProviderID string `json:"ai_provider_id"`
	Model        string `json:"model"`
	Enabled      bool   `json:"enabled"`
}

// listResponse covers both shapes: the newer API wraps the collection, the
// older one returned it bare.
type listResponse struct {
	Models []Entry `json:"models"`
}

// List reports the model-configs on this client's provider.
//
// The endpoint returns every provider's entries -- an Anthropic model sits in
// the same list as ours -- so filtering here rather than at the call site is
// what stops a caller deleting somebody else's models.
func (c *Client) List() ([]Entry, error) {
	var wrapped listResponse
	if err := c.do("GET", "", nil, &wrapped); err != nil {
		return nil, err
	}
	var mine []Entry
	for _, e := range wrapped.Models {
		if e.AIProviderID == c.ProviderID {
			mine = append(mine, e)
		}
	}
	return mine, nil
}

// FindByModel reports an existing entry on our provider serving this model.
//
// Entries are tracked locally by profile name, but Coder's picker is keyed by
// model: two profiles naming one model, or a profile renamed, used to leave a
// second row behind. Adopting the existing row instead of posting another is
// what keeps the picker honest.
//
// An enabled row wins, so adoption converges on the one actually in use.
func (c *Client) FindByModel(model string) (string, error) {
	entries, err := c.List()
	if err != nil {
		return "", err
	}
	found := ""
	for _, e := range entries {
		if e.Model != model {
			continue
		}
		if e.Enabled {
			return e.ID, nil
		}
		if found == "" {
			found = e.ID
		}
	}
	return found, nil
}

// TokenKey describes the API key the token belongs to.
type TokenKey struct {
	ID        string    `json:"id"`
	Name      string    `json:"token_name"`
	ExpiresAt time.Time `json:"expires_at"`
}

// TokenExpiry reads when this token stops working. Coder caps token lifetime,
// so a working sync is always some days from breaking, and an expired token
// disappears from the token list entirely -- there is no later signal.
//
// A key id is the part of the token before the first dash; anything else is
// not a shape we can ask about.
func (c *Client) TokenExpiry() (TokenKey, error) {
	id, _, ok := strings.Cut(c.Token, "-")
	if !ok || id == "" {
		return TokenKey{}, fmt.Errorf("token is not in the id-secret form this check needs")
	}
	req, err := http.NewRequest("GET", c.URL+"/api/v2/users/me/keys/"+id, nil)
	if err != nil {
		return TokenKey{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Coder-Session-Token", c.Token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return TokenKey{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return TokenKey{}, fmt.Errorf("coder API GET /api/v2/users/me/keys/%s: HTTP %d", id, resp.StatusCode)
	}
	var k TokenKey
	if err := json.NewDecoder(resp.Body).Decode(&k); err != nil {
		return TokenKey{}, err
	}
	return k, nil
}

// Create posts a new model-config entry and returns its ID.
func (c *Client) Create(mc ModelConfig) (string, error) {
	mc.AIProviderID = c.ProviderID
	var created createResponse
	if err := c.do("POST", "", mc, &created); err != nil {
		return "", err
	}
	if created.ID == "" {
		// An entry we cannot name can never be updated or disabled, and the
		// next sync would create a second one.
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
