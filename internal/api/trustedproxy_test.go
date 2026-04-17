package api

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func mustParseCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

func TestIsTrustedProxy(t *testing.T) {
	nets := []*net.IPNet{mustParseCIDR("10.0.0.0/8"), mustParseCIDR("172.64.0.0/13")}

	tests := []struct {
		addr    string
		trusted bool
	}{
		{"10.0.0.1:1234", true},
		{"10.255.255.255:80", true},
		{"192.168.1.1:80", false},
		{"172.64.0.1:443", true},
		{"172.72.0.1:443", false}, // outside /13
		{"10.0.0.1", true},       // bare IP, no port
		{"garbage", false},
	}

	for _, tt := range tests {
		got := isTrustedProxy(tt.addr, nets)
		if got != tt.trusted {
			t.Errorf("isTrustedProxy(%q) = %v, want %v", tt.addr, got, tt.trusted)
		}
	}
}

func TestTrustedProxyRealIP_Trusted(t *testing.T) {
	nets := []*net.IPNet{mustParseCIDR("10.0.0.0/8")}
	mw := trustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
	}))

	// CF-Connecting-IP from trusted proxy.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("CF-Connecting-IP", "203.0.113.50")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "203.0.113.50" {
		t.Errorf("expected 203.0.113.50, got %s", captured)
	}

	// X-Real-IP fallback.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Real-IP", "198.51.100.1")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "198.51.100.1" {
		t.Errorf("expected 198.51.100.1, got %s", captured)
	}

	// X-Forwarded-For with multiple IPs.
	req = httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "10.0.0.5:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.1, 10.0.0.5")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "203.0.113.1" {
		t.Errorf("expected 203.0.113.1, got %s", captured)
	}
}

func TestTrustedProxyRealIP_Untrusted(t *testing.T) {
	nets := []*net.IPNet{mustParseCIDR("10.0.0.0/8")}
	mw := trustedProxyRealIP(nets)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
	}))

	// Spoofed CF-Connecting-IP from untrusted source  - must be ignored.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.168.1.100:9999"
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "192.168.1.100:9999" {
		t.Errorf("expected original RemoteAddr 192.168.1.100:9999, got %s", captured)
	}
}

func TestTrustedProxyRealIP_NoNets(t *testing.T) {
	mw := trustedProxyRealIP(nil)

	var captured string
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.RemoteAddr
	}))

	// No trusted nets configured  - headers always ignored.
	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "1.2.3.4:80"
	req.Header.Set("CF-Connecting-IP", "spoofed")
	handler.ServeHTTP(httptest.NewRecorder(), req)
	if captured != "1.2.3.4:80" {
		t.Errorf("expected original RemoteAddr 1.2.3.4:80, got %s", captured)
	}
}

func TestParseTrustedProxyCIDRs(t *testing.T) {
	// Empty string returns nil.
	if got := ParseTrustedProxyCIDRs(""); got != nil {
		t.Errorf("expected nil, got %v", got)
	}

	// Custom CIDRs.
	nets := ParseTrustedProxyCIDRs("10.0.0.0/8, 192.168.0.0/16")
	if len(nets) != 2 {
		t.Fatalf("expected 2 nets, got %d", len(nets))
	}

	// "cloudflare" keyword.
	nets = ParseTrustedProxyCIDRs("cloudflare")
	if len(nets) == 0 {
		t.Fatal("expected cloudflare CIDRs, got 0")
	}
	// Cloudflare has at least 15 IPv4 + some IPv6 ranges.
	if len(nets) < 15 {
		t.Errorf("expected at least 15 cloudflare CIDRs, got %d", len(nets))
	}

	// Invalid CIDR is skipped.
	nets = ParseTrustedProxyCIDRs("10.0.0.0/8, not-a-cidr, 192.168.0.0/16")
	if len(nets) != 2 {
		t.Errorf("expected 2 nets (invalid skipped), got %d", len(nets))
	}
}
