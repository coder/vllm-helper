package recipe

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Placeholders a recipe may reference in its env values. They are discovered on
// each node at launch time rather than stored in the recipe: an interface name
// or address is a property of the machine, not of the model.
const (
	PlaceholderIface  = "IFACE_NAME"
	PlaceholderIBIf   = "IB_IF"
	PlaceholderNodeIP = "NODE_IP"
)

// Placeholders returns the placeholder names this recipe references, sorted, so
// a launcher knows what it has to discover before it can expand the env.
func (r TestedRecipe) Placeholders() []string {
	seen := map[string]bool{}
	for _, v := range r.Env {
		if name, ok := placeholderName(v); ok {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// NodeEnv resolves the recipe env for one node. Only a whole value of the form
// "$NAME" is a placeholder; there is no general interpolation, so an unset or
// misspelled name fails here rather than reaching vLLM as an empty string and
// silently falling back to the wrong interface.
func (r TestedRecipe) NodeEnv(discovered map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(r.Env))
	for k, v := range r.Env {
		name, ok := placeholderName(v)
		if !ok {
			out[k] = v
			continue
		}
		got := discovered[name]
		if got == "" {
			return nil, fmt.Errorf("%s=$%s but %s was not discovered on this node", k, name, name)
		}
		out[k] = got
	}
	return out, nil
}

func placeholderName(v string) (string, bool) {
	if !strings.HasPrefix(v, "$") || strings.ContainsAny(v, " \t") {
		return "", false
	}
	return strings.TrimPrefix(v, "$"), true
}

// ServeArgs is the argv after "vllm serve <model>". The recipe is authoritative:
// these are the flags we verified, so nothing is recomputed from the profile.
func (r TestedRecipe) ServeArgs() []string {
	args := append([]string{}, r.Args...)
	// Host and port are the launcher's business, not the model's, which is why
	// the recipe carries "port" as a field instead of inside args.
	return append(args, "--host", "0.0.0.0", "--port", strconv.Itoa(r.Port))
}

// DistArgs adds the multi-node flags for one rank. Rank 0 is the head that
// serves the API; every other rank runs headless.
func (r TestedRecipe) DistArgs(rank int, masterAddr string, masterPort int) []string {
	args := []string{
		"--nnodes", strconv.Itoa(r.Nodes),
		"--node-rank", strconv.Itoa(rank),
		"--master-addr", masterAddr,
		"--master-port", strconv.Itoa(masterPort),
	}
	if rank != 0 {
		args = append(args, "--headless")
	}
	return args
}

// NeedsCluster reports whether the recipe must be launched across more than one
// node, so a launcher can refuse early instead of half-starting a shard.
func (r TestedRecipe) NeedsCluster() bool { return r.Topology == "shard" && r.Nodes > 1 }
