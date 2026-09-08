package nodes

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/coder/vllm-helper/internal/config"
)

// NIC describes a network interface.
type NIC struct {
	Name  string `json:"name"`
	Speed string `json:"speed"`
	MTU   int    `json:"mtu"`
}

// Capabilities is a short summary of a node's serving capacity.
type Capabilities struct {
	Accelerator string `json:"accelerator"` // cuda|rocm|cpu
	GPUs        int    `json:"gpus"`
	PoolBytes   uint64 `json:"pool_bytes"` // memory pool for weights
	NICs        []NIC  `json:"nics"`
}

// Node is one machine in the cluster registry.
type Node struct {
	Name   string            `json:"name"`
	Host   string            `json:"host"` // scheme://host:port for API routing
	Labels map[string]string `json:"labels"`
	Caps   Capabilities      `json:"caps"`
}

// Registry persists known nodes (config-scoped, not per-profile).
type Registry struct {
	path string
	mu   sync.RWMutex
	m    map[string]Node
}

func pathDefault() string { return filepath.Join(config.ResolveDirs().Config, "nodes.json") }

// Load reads ~/.config/vllm-helper/nodes.json.
func Load() (*Registry, error) {
	p := pathDefault()
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return &Registry{path: p, m: map[string]Node{}}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Node
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, err
	}
	r := &Registry{path: p, m: map[string]Node{}}
	for _, n := range list {
		r.m[n.Name] = n
	}
	return r, nil
}

func (r *Registry) Save() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var list []Node
	for _, n := range r.m {
		list = append(list, n)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	_ = os.MkdirAll(filepath.Dir(r.path), 0o755)
	return os.WriteFile(r.path, b, 0o644)
}

func (r *Registry) Add(n Node) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.m == nil {
		r.m = map[string]Node{}
	}
	r.m[n.Name] = n
}

func (r *Registry) Remove(name string) { r.mu.Lock(); delete(r.m, name); r.mu.Unlock() }

func (r *Registry) Get(name string) (Node, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n, ok := r.m[name]
	return n, ok
}

func (r *Registry) List() []Node {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Node, 0, len(r.m))
	for _, n := range r.m {
		out = append(out, n)
	}
	return out
}

func (r *Registry) SetLabel(name, k, v string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	n, ok := r.m[name]
	if !ok {
		return false
	}
	if n.Labels == nil {
		n.Labels = map[string]string{}
	}
	n.Labels[k] = v
	r.m[name] = n
	return true
}
