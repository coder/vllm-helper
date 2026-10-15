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

func TestNodeHostNameReadsSSHThenHost(t *testing.T) {
	cases := []struct {
		name string
		node Node
		want string
	}{
		{"ssh wins", Node{SSH: "coder@box", Host: "http://elsewhere:8001"}, "box"},
		{"ssh without a user", Node{SSH: "box"}, "box"},
		{"host url", Node{Host: "http://127.0.0.1:8001"}, "127.0.0.1"},
		{"bare host", Node{Host: "box"}, "box"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.node.hostName(); got != c.want {
				t.Errorf("hostName() = %q, want %q", got, c.want)
			}
		})
	}
}

// The registry the two-Spark pool actually uses: the head is registered on
// loopback and the worker by hostname.
func TestRegistryShapeInUseResolvesHeadLocal(t *testing.T) {
	head := Node{Name: "spark1", Host: "http://127.0.0.1:8001"}
	worker := Node{Name: "spark2", Host: "http://other-box:8001"}
	if !hostIsLocal(head.hostName(), "this-box", nil) {
		t.Error("head on loopback must be local")
	}
	if hostIsLocal(worker.hostName(), "this-box", nil) {
		t.Error("worker by hostname must be remote")
	}
}
