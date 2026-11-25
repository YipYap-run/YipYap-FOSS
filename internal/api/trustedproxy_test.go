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

func TestTrustedProxyRealIP_Table(t *testing.T) {
	// A self-hosted proxy on 10.0.0.0/8 plus the built-in Cloudflare ranges.
	selfHosted := []*net.IPNet{mustParseCIDR("10.0.0.0/8")}
	withCF := append([]*net.IPNet{mustParseCIDR("10.0.0.0/8")}, ParseTrustedProxyCIDRs("cloudflare")...)

	const cfEdge = "172.64.10.20:443" // inside 172.64.0.0/13

	tests := []struct {
		name    string
		nets    []*net.IPNet
		peer    string
		headers map[string][]string
		want    string
	}{
		{
			name:    "cf header from cloudflare edge is honored",
			nets:    withCF,
			peer:    cfEdge,
			headers: map[string][]string{"CF-Connecting-IP": {"203.0.113.50"}},
			want:    "203.0.113.50",
		},
		{
			name: "cf header from non-cloudflare trusted proxy is ignored",
			nets: selfHosted,
			peer: "10.0.0.5:1234",
			headers: map[string][]string{
				"CF-Connecting-IP": {"1.2.3.4"},
				"X-Forwarded-For":  {"198.51.100.7"},
			},
			want: "198.51.100.7",
		},
		{
			name:    "cf header alone from non-cloudflare trusted proxy keeps peer",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"CF-Connecting-IP": {"1.2.3.4"}},
			want:    "10.0.0.5:1234",
		},
		{
			name:    "cf header from untrusted peer is ignored",
			nets:    withCF,
			peer:    "192.168.1.100:9999",
			headers: map[string][]string{"CF-Connecting-IP": {"1.2.3.4"}},
			want:    "192.168.1.100:9999",
		},
		{
			name: "cf header that is not an IP falls through to xff",
			nets: withCF,
			peer: cfEdge,
			headers: map[string][]string{
				"CF-Connecting-IP": {"not-an-ip"},
				"X-Forwarded-For":  {"203.0.113.9"},
			},
			want: "203.0.113.9",
		},
		{
			name:    "xff spoofed leftmost entry is ignored",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4, 203.0.113.1"}},
			want:    "203.0.113.1",
		},
		{
			name:    "xff skips trusted hops from the right",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4, 203.0.113.1, 10.0.0.9, 10.0.0.8"}},
			want:    "203.0.113.1",
		},
		{
			name:    "xff across multiple header lines",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4", "203.0.113.2"}},
			want:    "203.0.113.2",
		},
		{
			name:    "xff all trusted keeps peer",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"10.1.1.1, 10.0.0.9"}},
			want:    "10.0.0.5:1234",
		},
		{
			name:    "xff malformed rightmost entry keeps peer",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4, evil"}},
			want:    "10.0.0.5:1234",
		},
		{
			name:    "xff with port is rejected",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"203.0.113.1:5555"}},
			want:    "10.0.0.5:1234",
		},
		{
			name:    "xff ipv6 is accepted and canonicalized",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Forwarded-For": {"2001:DB8::1"}},
			want:    "2001:db8::1",
		},
		{
			name:    "x-real-ip used when no xff",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Real-IP": {"198.51.100.1"}},
			want:    "198.51.100.1",
		},
		{
			name: "x-real-ip ignored when xff present",
			nets: selfHosted,
			peer: "10.0.0.5:1234",
			headers: map[string][]string{
				"X-Real-IP":       {"1.2.3.4"},
				"X-Forwarded-For": {"198.51.100.3"},
			},
			want: "198.51.100.3",
		},
		{
			name:    "x-real-ip that is not an IP keeps peer",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: map[string][]string{"X-Real-IP": {"spoofed"}},
			want:    "10.0.0.5:1234",
		},
		{
			name:    "xff from untrusted peer is ignored",
			nets:    selfHosted,
			peer:    "198.51.100.200:443",
			headers: map[string][]string{"X-Forwarded-For": {"1.2.3.4"}, "X-Real-IP": {"1.2.3.4"}},
			want:    "198.51.100.200:443",
		},
		{
			name:    "no headers keeps peer",
			nets:    selfHosted,
			peer:    "10.0.0.5:1234",
			headers: nil,
			want:    "10.0.0.5:1234",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var captured string
			handler := trustedProxyRealIP(tt.nets)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = r.RemoteAddr
			}))
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tt.peer
			for k, vs := range tt.headers {
				for _, v := range vs {
					req.Header.Add(k, v)
				}
			}
			handler.ServeHTTP(httptest.NewRecorder(), req)
			if captured != tt.want {
				t.Errorf("RemoteAddr = %q, want %q", captured, tt.want)
			}
		})
	}
}

func TestRightmostUntrustedXFF(t *testing.T) {
	nets := []*net.IPNet{mustParseCIDR("10.0.0.0/8")}
	tests := []struct {
		name string
		xff  string
		want string
	}{
		{"client then trusted hops", "203.0.113.9, 10.0.0.9, 10.0.0.8", "203.0.113.9"},
		{"all hops trusted", "10.1.1.1, 10.0.0.9", ""},
		{"single trusted hop", "10.0.0.9", ""},
		{"empty entries skipped", "203.0.113.9, ,", "203.0.113.9"},
		{"malformed entry stops the walk", "203.0.113.9, evil", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rightmostUntrustedXFF(tt.xff, nets); got != tt.want {
				t.Fatalf("rightmostUntrustedXFF(%q) = %q, want %q", tt.xff, got, tt.want)
			}
		})
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
