package nodes

import "testing"

func TestHostIsLocal(t *testing.T) {
	addrs := []string{"10.44.0.1", "192.168.1.125"}
	cases := []struct {
		name string
		host string
		want bool
	}{
		{"loopback literal", "127.0.0.1", true},
		{"ipv6 loopback", "::1", true},
		{"localhost by name", "localhost", true},
		{"our own interface address", "10.44.0.1", true},
		{"the other node's address", "10.44.0.2", false},
		{"our short hostname", "spark-head", true},
		{"our fqdn", "spark-head.localdomain", true},
		{"another host", "spark-head-2", false},
		{"case insensitive", "SPARK-HEAD", true},
		{"empty is never local", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hostIsLocal(c.host, "spark-head.localdomain", addrs); got != c.want {
				t.Errorf("hostIsLocal(%q) = %v, want %v", c.host, got, c.want)
			}
		})
	}
}

// A node named like this machine but addressed elsewhere is remote: the
// address is the fact, the registry name is just a label.
func TestHostIsLocalTrustsTheAddressOverTheName(t *testing.T) {
	if hostIsLocal("10.44.0.2", "spark-head", []string{"10.44.0.1"}) {
		t.Error("an address that is not ours must not be local")
	}
}

// Address is dialled over HTTP, so it must come from --host. An ssh target
// can be an alias from ~/.ssh/config that only ssh can resolve; preferring it
// pointed the recipe endpoint, and so status and the Coder sync, at a name
// nothing answers HTTP on.
func TestNodeAddressReadsHostThenSSH(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want string
	}{
		{"host wins over an ssh alias", Node{SSH: "gpu-box", Host: "http://10.44.0.2:8001"}, "10.44.0.2"},
		{"host wins over user@host", Node{SSH: "coder@box", Host: "http://elsewhere:8001"}, "elsewhere"},
		{"bare host", Node{Host: "box"}, "box"},
		{"ssh when there is no host", Node{SSH: "coder@box"}, "box"},
		{"ssh without a user", Node{SSH: "box"}, "box"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.node.Address(); got != c.want {
				t.Errorf("Address() = %q, want %q", got, c.want)
			}
		})
	}
}

// Either address naming this machine makes the node local; a node is remote
// only when neither does.
func TestNodeLocalityChecksBothAddresses(t *testing.T) {
	const hostname = "this-box"
	addrs := []string{"10.44.0.1"}
	cases := []struct {
		name string
		node Node
		want bool
	}{
		{"loopback host with an alias ssh target", Node{Host: "http://127.0.0.1:8001", SSH: "gpu-head"}, true},
		{"own address as host", Node{Host: "http://10.44.0.1:8001", SSH: "coder@gpu-head"}, true},
		{"ssh target names this machine", Node{Host: "http://spark1:8001", SSH: "coder@this-box"}, true},
		{"both remote", Node{Host: "http://10.44.0.2:8001", SSH: "coder@other-box"}, false},
		{"remote host and an alias", Node{Host: "http://10.44.0.2:8001", SSH: "gpu-worker"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.node.isLocal(hostname, addrs); got != c.want {
				t.Errorf("isLocal() = %v, want %v", got, c.want)
			}
		})
	}
}

// The registry the two-Spark pool actually uses: the head is registered on
// loopback and the worker by hostname.
func TestRegistryShapeInUseResolvesHeadLocal(t *testing.T) {
	head := Node{Name: "spark1", Host: "http://127.0.0.1:8001"}
	worker := Node{Name: "spark2", Host: "http://other-box:8001"}
	if !head.isLocal("this-box", nil) {
		t.Error("head on loopback must be local")
	}
	if worker.isLocal("this-box", nil) {
		t.Error("worker by hostname must be remote")
	}
}
