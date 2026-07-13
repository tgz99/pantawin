package ssrf

import (
	"context"
	"errors"
	"net"
	"testing"
)

// fakeResolver lets tests control DNS answers without real lookups.
type fakeResolver struct {
	ips map[string][]net.IP
	err error
}

func (f *fakeResolver) LookupIP(ctx context.Context, host string) ([]net.IP, error) {
	if f.err != nil {
		return nil, f.err
	}
	ips, ok := f.ips[host]
	if !ok {
		return nil, &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}
	}
	return ips, nil
}

func guardWith(ips map[string][]net.IP) *Guard {
	return NewGuardWithResolver(&fakeResolver{ips: ips})
}

func TestValidate_AllowsPublicIPv4(t *testing.T) {
	g := guardWith(map[string][]net.IP{"example.com": {net.ParseIP("93.184.216.34")}})
	if err := g.Validate(context.Background(), "https://example.com/health"); err != nil {
		t.Errorf("public IPv4 host should be allowed, got %v", err)
	}
}

func TestValidate_RequiresHTTPScheme(t *testing.T) {
	g := guardWith(nil)
	for _, raw := range []string{
		"ftp://example.com",
		"file:///etc/passwd",
		"gopher://example.com",
		"example.com",       // no scheme at all
		"//example.com",     // protocol-relative
		"javascript:alert(1)",
	} {
		if err := g.Validate(context.Background(), raw); err == nil {
			t.Errorf("expected rejection for %q, got nil", raw)
		}
	}
}

func TestValidate_RejectsPrivateAndSpecialRanges(t *testing.T) {
	cases := map[string]string{
		"10.0.0.5":        "rfc1918 10/8",
		"172.16.0.9":      "rfc1918 172.16/12",
		"192.168.1.1":     "rfc1918 192.168/16",
		"127.0.0.1":       "loopback",
		"169.254.169.254": "link-local / cloud metadata",
		"0.0.0.0":         "unspecified",
		"224.0.0.1":       "multicast",
		"100.64.0.1":      "CGNAT 100.64/10",
	}
	for ip, label := range cases {
		g := guardWith(map[string][]net.IP{"sneaky.example.com": {net.ParseIP(ip)}})
		err := g.Validate(context.Background(), "https://sneaky.example.com/x")
		if !errors.Is(err, ErrForbiddenTarget) {
			t.Errorf("host resolving to %s (%s) should be rejected with ErrForbiddenTarget, got %v", ip, label, err)
		}
	}
}

func TestValidate_RejectsPrivateIPv6(t *testing.T) {
	for _, ip := range []string{"::1", "fc00::1", "fe80::1", "ff02::1"} {
		g := guardWith(map[string][]net.IP{"v6.example.com": {net.ParseIP(ip)}})
		err := g.Validate(context.Background(), "https://v6.example.com/")
		if !errors.Is(err, ErrForbiddenTarget) {
			t.Errorf("host resolving to %s should be rejected, got %v", ip, err)
		}
	}
}

func TestValidate_RejectsIfAnyResolvedIPIsPrivate(t *testing.T) {
	// DNS answers with one public and one private IP — the private one must
	// cause rejection (attacker-controlled DNS can round-robin).
	g := guardWith(map[string][]net.IP{
		"mixed.example.com": {net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.5")},
	})
	err := g.Validate(context.Background(), "https://mixed.example.com/")
	if !errors.Is(err, ErrForbiddenTarget) {
		t.Errorf("mixed public+private resolution should be rejected, got %v", err)
	}
}

func TestValidate_RejectsLiteralPrivateIPURL(t *testing.T) {
	g := guardWith(nil)
	for _, raw := range []string{
		"http://127.0.0.1:8081/healthz",
		"http://10.1.2.3/",
		"http://[::1]/",
		"http://169.254.169.254/latest/meta-data/",
	} {
		if err := g.Validate(context.Background(), raw); !errors.Is(err, ErrForbiddenTarget) {
			t.Errorf("literal private-IP URL %q should be rejected, got %v", raw, err)
		}
	}
}

func TestValidate_AllowsLiteralPublicIPURL(t *testing.T) {
	g := guardWith(nil)
	if err := g.Validate(context.Background(), "http://93.184.216.34/"); err != nil {
		t.Errorf("literal public-IP URL should be allowed, got %v", err)
	}
}

func TestValidate_DNSFailureIsError(t *testing.T) {
	g := NewGuardWithResolver(&fakeResolver{err: errors.New("dns broke")})
	if err := g.Validate(context.Background(), "https://example.com/"); err == nil {
		t.Error("resolver failure should surface as an error, not pass validation")
	}
}

// The DNS-rebinding defense is re-validating before every check execution —
// this test simulates a record that changes from public to private between
// two validations of the same URL.
func TestValidate_CatchesDNSRebinding(t *testing.T) {
	resolver := &fakeResolver{ips: map[string][]net.IP{
		"rebind.example.com": {net.ParseIP("93.184.216.34")},
	}}
	g := NewGuardWithResolver(resolver)

	if err := g.Validate(context.Background(), "https://rebind.example.com/"); err != nil {
		t.Fatalf("first validation (public IP) should pass, got %v", err)
	}

	// Attacker flips the record to an internal address.
	resolver.ips["rebind.example.com"] = []net.IP{net.ParseIP("192.168.1.10")}

	if err := g.Validate(context.Background(), "https://rebind.example.com/"); !errors.Is(err, ErrForbiddenTarget) {
		t.Errorf("re-validation after rebinding should reject, got %v", err)
	}
}
