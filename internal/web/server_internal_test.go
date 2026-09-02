package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/vllm-helper/internal/config"
	"github.com/coder/vllm-helper/internal/service"
)

// newTestServer builds a server over a throwaway config tree that contains
// secrets, so the leak tests below have something real to find.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	dirs := config.Dirs{
		Config:   filepath.Join(root, "config"),
		State:    filepath.Join(root, "state"),
		Data:     filepath.Join(root, "data"),
		Profiles: filepath.Join(root, "config", "models"),
	}
	for _, d := range []string{dirs.Config, dirs.State, dirs.Profiles} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(dirs.ConfigFile(), "PORT=8001\nSERVICE_NAME=vllm.service\nSERVICE_SCOPE=user\n")
	write(dirs.SecretsFile(), "VLLM_API_KEY=sk-do-not-leak-me\nCODER_API_TOKEN=coder-do-not-leak\nHF_TOKEN=hf_do_not_leak\n")
	write(dirs.ProfileFile("demo"), "MODEL=org/demo\nMAX_MODEL_LEN=4096\n")

	cfgFile, err := config.ParseEnvFile(dirs.ConfigFile())
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := config.ParseEnvFile(dirs.SecretsFile())
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Dirs: dirs, File: cfgFile, Secrets: secrets}

	token, err := loadOrCreateToken(dirs.State)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		cfg:        cfg,
		build:      "test",
		token:      token,
		svc:        service.Service{Name: "vllm.service", Scope: "user", NonInteractive: true},
		privileged: true,
	}
	return s
}

// The panel can stop a server and rewrite profiles, so an unauthenticated
// request must not reach a handler even on loopback.
func TestUnauthenticatedRequestsAreRejected(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(Static())

	for _, path := range []string{"/", "/api/status", "/api/profiles", "/api/machine", "/api/logs"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a token = %d, want 401", path, rec.Code)
		}
	}
}

func TestTokenIsAcceptedInHeaderCookieAndQuery(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(Static())

	header := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	header.Header.Set(tokenHeader, s.token)

	cookie := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	cookie.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})

	query := httptest.NewRequest(http.MethodGet, "/api/status?token="+s.token, nil)

	for name, r := range map[string]*http.Request{"header": header, "cookie": cookie, "query": query} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		if rec.Code != http.StatusOK {
			t.Errorf("%s auth = %d, want 200", name, rec.Code)
		}
	}

	wrong := httptest.NewRequest(http.MethodGet, "/api/status?token=nope", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, wrong)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("a wrong token = %d, want 401", rec.Code)
	}
}

// A cookie alone must not be enough to change anything. Any page on the
// internet can POST here and the browser would attach the cookie; it cannot
// read the token, so requiring it in a header is what stops the request.
func TestMutationsNeedTheTokenInAHeader(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(Static())

	cookieOnly := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	cookieOnly.AddCookie(&http.Cookie{Name: tokenCookie, Value: s.token})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, cookieOnly)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with only a cookie = %d, want 403", rec.Code)
	}

	queryOnly := httptest.NewRequest(http.MethodPost, "/api/restart?token="+s.token, nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, queryOnly)
	if rec.Code != http.StatusForbidden {
		t.Errorf("POST with only a query token = %d, want 403", rec.Code)
	}
}

// Every response is assembled from an explicit DTO precisely so a credential
// can't ride along. This asserts that end to end, against a config that
// really does hold secrets.
func TestNoResponseLeaksACredential(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(Static())

	secrets := []string{"sk-do-not-leak-me", "coder-do-not-leak", "hf_do_not_leak"}
	for _, path := range []string{"/api/status", "/api/profiles", "/api/machine", "/api/logs"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set(tokenHeader, s.token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)

		body := rec.Body.String()
		for _, secret := range secrets {
			if strings.Contains(body, secret) {
				t.Errorf("%s leaked a credential: %s", path, body)
			}
		}
	}
}

// The token lives in the state dir rather than secrets.env because
// everything in secrets.env is inlined into active.env and handed to vLLM's
// environment. A panel credential has no business being there.
func TestTokenIsStoredPrivatelyAndReused(t *testing.T) {
	dir := t.TempDir()
	first, err := loadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 32 {
		t.Errorf("token is %d chars, want something long enough not to guess", len(first))
	}
	again, err := loadOrCreateToken(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again != first {
		t.Error("a second call minted a new token; every restart would invalidate open tabs")
	}
	info, err := os.Stat(filepath.Join(dir, "web-token"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %v, want 0600", perm)
	}
}

// Without privilege the buttons are disabled and the reason is shown, so a
// click can't fail with a bare "exit status 1".
func TestActionsRefuseWithoutPrivilege(t *testing.T) {
	s := newTestServer(t)
	s.setPrivilege(false, "needs passwordless sudo")
	h := s.Handler(Static())

	r := httptest.NewRequest(http.MethodPost, "/api/restart", nil)
	r.Header.Set(tokenHeader, s.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("restart without privilege = %d, want 403", rec.Code)
	}
	var got actionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got.Error, "sudo") {
		t.Errorf("error = %q, want it to explain what's missing", got.Error)
	}
}

func TestListenURL(t *testing.T) {
	t.Parallel()
	// 0.0.0.0 is where it binds, not somewhere anyone can browse to.
	if got := ListenURL("0.0.0.0:8080", "abc"); got != "http://localhost:8080/?token=abc" {
		t.Errorf("ListenURL(0.0.0.0) = %q", got)
	}
	if got := ListenURL("127.0.0.1:9000", "abc"); got != "http://127.0.0.1:9000/?token=abc" {
		t.Errorf("ListenURL(loopback) = %q", got)
	}
}

func TestLoopbackOnly(t *testing.T) {
	t.Parallel()
	for addr, want := range map[string]bool{
		"127.0.0.1:8080": true,
		"localhost:8080": true,
		"[::1]:8080":     true,
		"0.0.0.0:8080":   false,
		"192.168.1.5:80": false,
	} {
		if got := LoopbackOnly(addr); got != want {
			t.Errorf("LoopbackOnly(%q) = %v, want %v", addr, got, want)
		}
	}
}

// The panel polls, and every health probe is a line in the log of the very
// server someone is likely reading. Several tabs open on one machine must not
// multiply into several streams of them, so a recent answer is reused.
func TestHealthProbeIsReusedBetweenCallers(t *testing.T) {
	var probes int32
	vllm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			atomic.AddInt32(&probes, 1)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer vllm.Close()

	s := newTestServer(t)
	s.cfg.File.Set("PORT", portOf(t, vllm.URL))

	// Ten callers, as ten open tabs would be.
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, ok := s.health(); !ok {
				t.Error("health() = not ok, want the endpoint reported healthy")
			}
		}()
	}
	wg.Wait()

	if got := atomic.LoadInt32(&probes); got != 1 {
		t.Errorf("probes = %d, want 1: concurrent callers should share one probe", got)
	}

	// And an answer older than the TTL is not reused, or the panel would
	// never notice the server going down.
	s.healthMu.Lock()
	s.healthAt = time.Now().Add(-2 * healthProbeTTL)
	s.healthMu.Unlock()
	if _, ok := s.health(); !ok {
		t.Error("health() = not ok after expiry")
	}
	if got := atomic.LoadInt32(&probes); got != 2 {
		t.Errorf("probes = %d, want 2: a stale answer should be replaced", got)
	}
}

// portOf pulls the port out of an httptest URL.
func portOf(t *testing.T, rawURL string) string {
	t.Helper()
	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

// Reloading the page drops the token from the URL -- it is stripped from the
// address bar on the first visit -- and the cookie is HttpOnly, so the page
// cannot read it back. A tab in that state must still be able to act, which
// means recovering the token from the server over the cookie it does have.
func TestReloadedTabCanRecoverTheTokenAndMutate(t *testing.T) {
	s := newTestServer(t)
	h := s.Handler(nil)

	// A tab with only the cookie, exactly as it is after a reload.
	cookie := &http.Cookie{Name: tokenCookie, Value: s.token}

	req := httptest.NewRequest("GET", "/api/token", nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/token = %d, want 200: a reloaded tab cannot recover the token", rec.Code)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Token != s.token {
		t.Fatal("GET /api/token returned the wrong token")
	}

	// The recovered token must satisfy the mutation check -- the actual
	// symptom was a restart button that refused with "no token".
	req = httptest.NewRequest("POST", "/api/restart", nil)
	req.AddCookie(cookie)
	req.Header.Set(tokenHeader, body.Token)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code == http.StatusForbidden || rec.Code == http.StatusUnauthorized {
		t.Errorf("POST /api/restart = %d, want the recovered token to be accepted", rec.Code)
	}

	// And the endpoint is not a way in: without the cookie it says nothing.
	req = httptest.NewRequest("GET", "/api/token", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET /api/token = %d, want 401", rec.Code)
	}
	if strings.Contains(rec.Body.String(), s.token) {
		t.Error("unauthenticated GET /api/token leaked the token")
	}
}
