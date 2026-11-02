package http

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"
)

// hostGuard decides which hosts a request may reach.
//
// The guard exists because a capability's URL may be derived from untrusted
// input. A capability that can reach any address on the machine, or on the
// network the runtime runs in, is a pivot: it can read the cloud metadata
// service, reach an internal API that trusts the network, or reach the
// runtime's own control surface.
//
// There are exactly two rules.
//
// When the policy lists no hosts, any host may be reached, but only through
// an address that is publicly routable. A loopback, private, link-local,
// multicast, or otherwise unroutable address is refused.
//
// When the policy lists hosts, only those hosts may be reached. A listed host
// is permitted even when it resolves to a private address, because listing it
// is the explicit and auditable decision to let a capability reach an
// internal service.
type hostGuard struct {
	// allowedHosts are the hosts the policy permits.
	allowedHosts []string

	// allowPrivate lifts the address restriction. It is set for the paths
	// where the request is authored by the operator rather than derived from
	// an assembly's input.
	allowPrivate bool
}

// newHostGuard builds the guard for a policy.
func newHostGuard(allowedHosts []string, allowPrivate bool) hostGuard {
	hosts := make([]string, 0, len(allowedHosts))
	for _, host := range allowedHosts {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			continue
		}
		hosts = append(hosts, trimmed)
	}
	return hostGuard{allowedHosts: hosts, allowPrivate: allowPrivate}
}

// restricts reports whether the guard names a fixed set of hosts.
func (g hostGuard) restricts() bool {
	return len(g.allowedHosts) > 0
}

// lists reports whether the host is one of the permitted hosts.
//
// A host matches an entry exactly, or through a leading "*." wildcard, which
// covers the named domain and its subdomains.
func (g hostGuard) lists(host string) bool {
	host = normalizeHost(host)
	for _, allowed := range g.allowedHosts {
		allowed = normalizeHost(allowed)
		if allowed == "" {
			continue
		}
		if strings.HasPrefix(allowed, "*.") {
			if host == allowed[2:] || strings.HasSuffix(host, allowed[1:]) {
				return true
			}
			continue
		}
		if host == allowed {
			return true
		}
	}
	return false
}

// refuses reports whether the host may not be reached.
//
// It is the name-level check: the address-level check happens when the
// connection is made, so a host that resolves to a private address is
// refused there.
func (g hostGuard) refuses(host string) error {
	if !g.restricts() {
		return nil
	}
	if g.lists(host) {
		return nil
	}
	return fmt.Errorf("neuron:core:http: the host %q is not permitted; the permitted hosts are %s",
		host, strings.Join(g.allowedHosts, ", "))
}

// dialContext connects to addr, refusing an address the policy does not
// permit.
//
// The address is resolved here rather than trusted from the URL: a host name
// is a claim about an address, and the claim is checked against the addresses
// it actually resolves to.
func (g hostGuard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: %w", err)
	}

	if g.restricts() {
		if err := g.refuses(host); err != nil {
			return nil, err
		}
		return dialer.DialContext(ctx, network, addr)
	}

	if g.allowPrivate {
		return dialer.DialContext(ctx, network, addr)
	}

	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("neuron:core:http: resolve %q: %w", host, err)
	}

	var refusal error
	for _, address := range addresses {
		if !isPubliclyRoutable(address.IP) {
			refusal = fmt.Errorf(
				"neuron:core:http: the host %q resolves to %s, which is not a publicly routable address; list the host in builtins.http.allowedHosts to permit it",
				host, address.IP)
			continue
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(address.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		refusal = err
	}

	if refusal == nil {
		refusal = fmt.Errorf("neuron:core:http: the host %q has no address to connect to", host)
	}
	return nil, refusal
}

// dialer is the base dialer. It carries no timeout of its own: the request's
// deadline is carried by the context, so the timeout applies to the whole
// operation rather than to each attempt.
var dialer = &net.Dialer{KeepAlive: 30 * time.Second}

// isPubliclyRoutable reports whether an address may be reached when the
// policy names no hosts.
//
// The refusal covers the loopback interface, the private ranges, the
// link-local ranges (which carry the cloud metadata address), the shared
// address space used by carrier-grade NAT, the multicast ranges, and the
// reserved range.
func isPubliclyRoutable(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	if v4 := ip.To4(); v4 != nil {
		// The shared address space (100.64.0.0/10) is not reachable from the
		// public internet.
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return false
		}
		// The reserved range (240.0.0.0/4) is not routable either.
		if v4[0] >= 240 {
			return false
		}
	}
	return true
}

// normalizeHost prepares a host name for comparison.
func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
}
