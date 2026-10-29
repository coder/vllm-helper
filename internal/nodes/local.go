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
// box when it was not. Either address naming this machine is enough: the
// head is commonly registered on loopback with an ssh target that names it.
func (n Node) IsLocal() bool {
	return n.isLocal(localHostName(), localAddrs())
}

func (n Node) isLocal(hostname string, addrs []string) bool {
	return hostIsLocal(n.httpHost(), hostname, addrs) ||
		hostIsLocal(n.sshHost(), hostname, addrs)
}

// Address is the host this node is reached at, for callers that dial it
// directly over HTTP (the API, a health probe) rather than through ssh.
func (n Node) Address() string {
	if h := n.httpHost(); h != "" {
		return h
	}
	return n.sshHost()
}

// httpHost is the host from --host, the URL the node serves on. It is the
// only address known to resolve for HTTP: the ssh target can be an alias from
// ~/.ssh/config that nothing but ssh understands.
func (n Node) httpHost() string {
	if u, err := url.Parse(n.Host); err == nil && u.Hostname() != "" {
		return u.Hostname()
	}
	return strings.TrimSpace(n.Host)
}

// sshHost is the host part of the ssh target, without the user.
func (n Node) sshHost() string {
	if _, host, ok := strings.Cut(n.SSH, "@"); ok {
		return host
	}
	return strings.TrimSpace(n.SSH)
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
