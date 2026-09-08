package router

import (
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"
)

type backend struct {
	url     *url.URL
	healthy bool
}

type Pool struct {
	mu       sync.RWMutex
	backends []backend
	sticky   func(*http.Request) string
}

// NewPool creates a simple sticky router over backends.
func NewPool(backends []string, sticky func(*http.Request) string) (*Pool, error) {
	var bs []backend
	for _, s := range backends {
		u, err := url.Parse(s)
		if err != nil {
			return nil, err
		}
		bs = append(bs, backend{url: u, healthy: true})
	}
	if sticky == nil {
		sticky = func(*http.Request) string { return "" }
	}
	return &Pool{backends: bs, sticky: sticky}, nil
}

func (p *Pool) pick(key string) (backend, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.backends) == 0 {
		return backend{}, false
	}
	if key == "" {
		return p.backends[0], true
	}
	// Fixed but stable mapping.
	var h uint32
	for i := 0; i < len(key); i++ {
		h = h*16777619 ^ uint32(key[i])
	}
	return p.backends[int(h)%len(p.backends)], true
}

func (p *Pool) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	key := p.sticky(r)
	b, ok := p.pick(key)
	if !ok {
		http.Error(w, "no backends", http.StatusBadGateway)
		return
	}
	rx := httputil.NewSingleHostReverseProxy(b.url)
	rx.ErrorHandler = func(w http.ResponseWriter, _ *http.Request, _ error) {
		http.Error(w, "backend error", http.StatusBadGateway)
	}
	rx.ServeHTTP(w, r)
}
