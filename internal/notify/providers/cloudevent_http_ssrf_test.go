package providers

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// TestValidateCloudEventHTTPConfig_BlocksPrivateHTTPS is the H-3 regression.
// Prior to the fix, validateCloudEventHTTPConfig only gated http:// against
// localhost name matching. https://169.254.169.254 and friends slipped
// through. The fix moves to a scheme-agnostic host check.
func TestValidateCloudEventHTTPConfig_BlocksPrivateHTTPS(t *testing.T) {
	// Defensive: make sure the control-plane allow-private toggle is off.
	origCP := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = false
	defer func() { checker.AllowPrivateControlPlaneTargets = origCP }()

	blocked := []string{
		// IPv4 private / link-local / metadata endpoints on http AND https.
		"http://169.254.169.254/latest/api/token",
		"https://169.254.169.254/latest/api/token",
		"http://10.0.0.1/sink",
		"https://10.0.0.1/sink",
		"http://192.168.1.1/sink",
		"https://192.168.1.1/sink",
		"http://172.16.0.1/sink",
		"https://172.31.255.254/sink",
		// CGNAT.
		"http://100.64.0.1/sink",
		"https://100.127.255.254/sink",
		// IPv6 ULA + link-local + loopback.
		"https://[fd00::1]/sink",
		"https://[fe80::1]/sink",
		// IPv4-mapped IPv6 that decays to a private v4.
		"https://[::ffff:10.0.0.1]/sink",
		"https://[::ffff:169.254.169.254]/sink",
		// RFC 6761 reserves `localhost` AND every name ending in
		// `.localhost` for loopback. Using a subdomain because the bare
		// `localhost` host hits validateCloudEventHTTPConfig's explicit
		// dev-convenience exemption (isLocalhost short-circuits before the
		// SSRF gate). The .localhost-suffix path goes through the gate and
		// is platform-independent, no /etc/hosts dependency. (Earlier rev
		// used "localhost.localdomain", that's not RFC-mandated and CI
		// runners without it in /etc/hosts saw NXDOMAIN, treating the
		// hostname as non-private and letting it through.)
		"https://dev.localhost/sink",
	}
	for _, u := range blocked {
		cfg := &CloudEventHTTPConfig{SinkURL: u, Mode: "binary"}
		err := validateCloudEventHTTPConfig(cfg)
		if err == nil {
			t.Errorf("validateCloudEventHTTPConfig(%q) = nil; want SSRF block", u)
			continue
		}
		if !strings.Contains(err.Error(), "private range") && !strings.Contains(err.Error(), "unsupported scheme") {
			// "unsupported scheme" is fine for unknown scheme paths, but
			// anything here is http/https and we want the private-range
			// error to confirm the H-3 path fired.
			t.Errorf("validateCloudEventHTTPConfig(%q) blocked with unexpected message: %v", u, err)
		}
	}

	// Public URLs still pass.
	public := []string{
		"https://broker.example.com/ns/default",
		"https://1.1.1.1/sink",
		"https://8.8.8.8/sink",
	}
	for _, u := range public {
		cfg := &CloudEventHTTPConfig{SinkURL: u, Mode: "binary"}
		// Skip domain-based public tests in environments that can't resolve.
		if _, err := net.LookupHost(strings.TrimPrefix(strings.TrimSuffix(u[strings.Index(u, "//")+2:], "/sink"), "www.")); err != nil && strings.Contains(u, "example.com") {
			continue
		}
		if err := validateCloudEventHTTPConfig(cfg); err != nil {
			t.Errorf("validateCloudEventHTTPConfig(%q) = %v; want nil for public host", u, err)
		}
	}
}

// TestValidateCloudEventHTTPConfig_AllowsLocalhost confirms the
// localhost-pod deployment pattern survives the scheme-agnostic check.
func TestValidateCloudEventHTTPConfig_AllowsLocalhost(t *testing.T) {
	origCP := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = false
	defer func() { checker.AllowPrivateControlPlaneTargets = origCP }()

	for _, u := range []string{
		"http://localhost:8080/sink",
		"http://127.0.0.1:8080/sink",
		"http://[::1]:8080/sink",
	} {
		cfg := &CloudEventHTTPConfig{SinkURL: u, Mode: "binary"}
		if err := validateCloudEventHTTPConfig(cfg); err != nil {
			t.Errorf("validateCloudEventHTTPConfig(%q) = %v; want nil (localhost exception)", u, err)
		}
	}
}

// TestSSRFGuardedDialContext_BlocksPrivateAtDial is the H-4 regression.
// The scenario: create-time validation observed a public A for the host,
// but by Dial time the attacker has flipped the DNS to 127.0.0.1 (or any
// private range). The guarded DialContext re-resolves at connect time
// and rejects with ErrSSRFBlocked. Redirects follow the same path so a
// 302 to a private address is also blocked.
func TestSSRFGuardedDialContext_BlocksPrivateAtDial(t *testing.T) {
	origCP := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = false
	defer func() { checker.AllowPrivateControlPlaneTargets = origCP }()

	// Base dialer that would succeed were it not wrapped. We use a dialer
	// that would connect to 127.0.0.1 in production; here we just assert
	// the guard short-circuits before reaching the net layer.
	called := false
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return nil, errors.New("must not reach base dialer")
	}
	guarded := ssrfGuardedDialContext(base)

	// Simulate a post-rebind connect to 127.0.0.1.
	_, err := guarded(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil {
		t.Fatalf("expected ErrSSRFBlocked, got nil")
	}
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("expected errors.Is(err, ErrSSRFBlocked); got %v", err)
	}
	if called {
		t.Fatalf("base dialer must not be invoked when guard trips")
	}

	// Also simulate the metadata endpoint.
	_, err = guarded(context.Background(), "tcp", "169.254.169.254:80")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("metadata endpoint: expected ErrSSRFBlocked; got %v", err)
	}

	// IPv6 private must also trip.
	_, err = guarded(context.Background(), "tcp", "[fd00::1]:443")
	if !errors.Is(err, ErrSSRFBlocked) {
		t.Fatalf("IPv6 ULA: expected ErrSSRFBlocked; got %v", err)
	}

	// A public address reaches the base dialer.
	baseReached := false
	basePublic := func(ctx context.Context, network, addr string) (net.Conn, error) {
		baseReached = true
		return nil, errors.New("stop here")
	}
	guardedPublic := ssrfGuardedDialContext(basePublic)
	_, err = guardedPublic(context.Background(), "tcp", "8.8.8.8:443")
	if err == nil || err.Error() != "stop here" {
		t.Fatalf("public address: expected to reach base dialer; got %v", err)
	}
	if !baseReached {
		t.Fatalf("public address: base dialer not invoked")
	}
}

// TestSSRFGuardedDialContext_OptInBypasses confirms the opt-in disables
// the guard, needed for localhost CloudEvent development.
func TestSSRFGuardedDialContext_OptInBypasses(t *testing.T) {
	origCP := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = true
	defer func() { checker.AllowPrivateControlPlaneTargets = origCP }()

	reached := false
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		reached = true
		return nil, errors.New("base ok")
	}
	guarded := ssrfGuardedDialContext(base)

	_, err := guarded(context.Background(), "tcp", "127.0.0.1:443")
	if err == nil || err.Error() != "base ok" {
		t.Fatalf("opt-in: expected base dialer to run; got %v", err)
	}
	if !reached {
		t.Fatalf("opt-in: base dialer not invoked")
	}
}

// TestIsPrivateAddr_Matrix asserts the blocked-range matrix from the
// security brief.
func TestIsPrivateAddr_Matrix(t *testing.T) {
	tests := []struct {
		ip      string
		blocked bool
		why     string
	}{
		{"127.0.0.1", true, "loopback"},
		{"0.0.0.0", true, "unspecified"},
		{"169.254.169.254", true, "link-local"},
		{"10.0.0.1", true, "rfc1918"},
		{"172.16.0.1", true, "rfc1918"},
		{"192.168.1.1", true, "rfc1918"},
		{"100.64.0.1", true, "cgnat"},
		{"100.127.255.254", true, "cgnat"},
		{"::1", true, "loopback"},
		{"fe80::1", true, "link-local"},
		// net.IP.IsPrivate() already claims fc00::/7 (RFC4193); the why
		// string is "rfc1918 / rfc4193", both paths satisfy the H-3 block.
		{"fd00::1", true, "rfc"},
		{"fc00::1", true, "rfc"},
		{"::", true, "unspecified"},
		// Public.
		{"8.8.8.8", false, ""},
		{"1.1.1.1", false, ""},
		{"100.63.255.254", false, ""}, // just below CGNAT
		{"100.128.0.1", false, ""},    // just above CGNAT
		{"2001:4860:4860::8888", false, ""},
	}
	for _, tt := range tests {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("parse %q", tt.ip)
		}
		got, why := isPrivateAddr(ip)
		if got != tt.blocked {
			t.Errorf("isPrivateAddr(%q) = (%v, %q); want blocked=%v", tt.ip, got, why, tt.blocked)
			continue
		}
		if tt.blocked && tt.why != "" && !strings.Contains(why, tt.why) {
			t.Errorf("isPrivateAddr(%q) why=%q; want substring %q", tt.ip, why, tt.why)
		}
	}
}

// TestIsPrivateAddr_BlocksIPv4CompatV6 is the R2-H4 regression. The
// IPv4-compatible IPv6 form (::a.b.c.d, RFC 4291 §2.5.5.1) was treated as
// a generic public v6 by Go's stdlib, IsPrivate / IsLinkLocalUnicast
// only see the v6 representation. Without the explicit unwrap, an
// attacker reaches 169.254.169.254 by phrasing it as ::169.254.169.254.
func TestIsPrivateAddr_BlocksIPv4CompatV6(t *testing.T) {
	cases := []struct {
		ip      string
		blocked bool
	}{
		{"::169.254.169.254", true},
		{"::10.0.0.1", true},
		{"::192.168.1.1", true},
		{"::172.16.0.1", true},
		{"::127.0.0.1", true},
		// MAPPED form already covered by other tests; re-assert for clarity.
		{"::ffff:10.0.0.1", true},
		{"::ffff:169.254.169.254", true},
		// Public v4 wrapped as v4-compat MUST still be considered public.
		{"::8.8.8.8", false},
		{"::1.1.1.1", false},
	}
	for _, tt := range cases {
		ip := net.ParseIP(tt.ip)
		if ip == nil {
			t.Fatalf("parse %q", tt.ip)
		}
		got, why := isPrivateAddr(ip)
		if got != tt.blocked {
			t.Errorf("isPrivateAddr(%q) = (%v, %q); want blocked=%v", tt.ip, got, why, tt.blocked)
		}
	}
}

// TestSSRFGuardedDialContext_BlocksIPv4CompatV6 confirms the dial-time
// guard also rejects the IPv4-compat v6 form. Pairs with the matrix test
// above; the dial path is what actually fires when a sink redirects to
// or DNS-rebinds onto ::169.254.169.254.
func TestSSRFGuardedDialContext_BlocksIPv4CompatV6(t *testing.T) {
	origCP := checker.AllowPrivateControlPlaneTargets
	checker.AllowPrivateControlPlaneTargets = false
	defer func() { checker.AllowPrivateControlPlaneTargets = origCP }()

	called := false
	base := func(ctx context.Context, network, addr string) (net.Conn, error) {
		called = true
		return nil, errors.New("must not reach base dialer")
	}
	guarded := ssrfGuardedDialContext(base)

	for _, addr := range []string{
		"[::169.254.169.254]:80",
		"[::10.0.0.1]:443",
		"[::127.0.0.1]:8080",
	} {
		_, err := guarded(context.Background(), "tcp", addr)
		if !errors.Is(err, ErrSSRFBlocked) {
			t.Errorf("guarded(%q) = %v; want ErrSSRFBlocked", addr, err)
		}
	}
	if called {
		t.Fatalf("base dialer must not be invoked for IPv4-compat v6 private addresses")
	}
}
