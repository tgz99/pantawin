// Package ssrf validates monitor target URLs so the check engine can never
// be pointed at internal infrastructure (spec sections 7.2 item 4 and 8).
//
// Validation runs at monitor creation AND again immediately before every
// check execution — creation-time-only validation is defeated by DNS
// rebinding (record flips to an internal IP after the monitor is created).
package ssrf

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
)

var ErrForbiddenTarget = errors.New("target resolves to a private, loopback, or otherwise forbidden address")

// Resolver is the subset of net.Resolver the guard needs; tests substitute
// a fake.
type Resolver interface {
	LookupIP(ctx context.Context, host string) ([]net.IP, error)
}

type netResolver struct{ r *net.Resolver }

func (n *netResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	addrs, err := n.r.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	ips := make([]net.IP, len(addrs))
	for i, a := range addrs {
		ips[i] = a.IP
	}
	return ips, nil
}

type Guard struct {
	resolver Resolver

	// AllowLoopback disables the loopback rejection ONLY — for integration
	// tests that point monitors at local httptest fixtures. Never set in
	// production wiring; all other forbidden ranges stay enforced.
	AllowLoopback bool
}

func NewGuard() *Guard {
	return &Guard{resolver: &netResolver{r: net.DefaultResolver}}
}

func NewGuardWithResolver(r Resolver) *Guard {
	return &Guard{resolver: r}
}

// Validate parses rawURL, requires an http(s) scheme, resolves the host,
// and rejects if ANY resolved address falls in a forbidden range. "Any"
// matters: attacker-controlled DNS can return a mix of public and private
// answers and rely on round-robin to eventually hit the private one.
func (g *Guard) Validate(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme must be http or https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("url has no host")
	}

	// Literal IP in the URL — no DNS involved.
	if ip := net.ParseIP(host); ip != nil {
		if g.forbidden(ip) {
			return fmt.Errorf("%w: %s", ErrForbiddenTarget, ip)
		}
		return nil
	}

	ips, err := g.resolver.LookupIP(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %s: no addresses", host)
	}
	for _, ip := range ips {
		if g.forbidden(ip) {
			return fmt.Errorf("%w: %s resolves to %s", ErrForbiddenTarget, host, ip)
		}
	}
	return nil
}

func (g *Guard) forbidden(ip net.IP) bool {
	if g.AllowLoopback && ip.IsLoopback() {
		return false
	}
	return isForbidden(ip)
}

// isForbidden covers loopback, RFC1918/ULA private space, link-local
// (including the cloud metadata endpoint 169.254.169.254), CGNAT,
// multicast, and unspecified addresses, for both IPv4 and IPv6.
func isForbidden(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() ||
		ip.IsInterfaceLocalMulticast() {
		return true
	}
	// CGNAT 100.64.0.0/10 — not covered by IsPrivate.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && v4[1] >= 64 && v4[1] <= 127 {
			return true
		}
	}
	return false
}
