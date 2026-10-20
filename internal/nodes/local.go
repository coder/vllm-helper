package nodes

import (
	"net"
	"net/url"
	"os"
	"strings"
)

// IsLocal reports whether this node is the machine we are running on, so
// callers can skip ssh. Deciding this per node matters: orchestration used to
// assume the head was always local, which silently ran commands on the wrong
// box when it was not.
func (n Node) IsLocal() bool {
	return hostIsLocal(n.hostName(), localHostName(), localAddrs())
}

// Address is the host this node is reached at, for callers that dial it
// directly (a health probe) rather than through ssh.
func (n Node) Address() string { return n.hostName() }

// hostName is where the node answers, preferring the SSH target because that
// is what an orchestration hop would dial.
func (n Node) hostName() string {
	if n.SSH != "" {
		if _, host, ok := strings.Cut(n.SSH, "@"); ok {
			return host
		}
		return n.SSH
	}
	if u, err := url.Parse(n.Host); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return n.Host
}

// hostIsLocal is the whole decision, kept pure so it can be tested without
// depending on whatever machine the tests run on.
func hostIsLocal(host, hostname string, addrs []string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	switch host {
	case "":
		return false
	case "localhost":
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() {
			return true
		}
		for _, a := range addrs {
			if own := net.ParseIP(a); own != nil && own.Equal(ip) {
				return true
			}
		}
		// A literal address that is not ours is remote, whatever it is named.
		return false
	}
	// A name only matches a name. Compare short forms so a registry holding
	// an FQDN still matches a host that reports itself unqualified.
	short := shortHost(host)
	return short != "" && short == shortHost(hostname)
}

func shortHost(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.Index(s, "."); i >= 0 {
		return s[:i]
	}
	return s
}

func localHostName() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// localAddrs lets a node registered by IP still be recognised as this machine,
// which is common once nodes are addressed over a dedicated link.
func localAddrs() []string {
	ifaces, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ifaces))
	for _, a := range ifaces {
		if ipnet, ok := a.(*net.IPNet); ok {
			out = append(out, ipnet.IP.String())
		}
	}
	return out
}
