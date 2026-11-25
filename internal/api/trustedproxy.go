package api

import (
	"log/slog"
	"net"
	"net/http"
	"strings"
)

// Cloudflare's published IP ranges (https://www.cloudflare.com/ips/).
// Last updated: 2026-04-15. These change infrequently; update when
// Cloudflare announces new ranges.
var cloudflareCIDRs = []string{
	// IPv4
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	// IPv6
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

// ParseTrustedProxyCIDRs parses a comma-separated list of CIDRs or the
// special keyword "cloudflare" into a slice of *net.IPNet. Unknown tokens
// are logged and skipped.
func ParseTrustedProxyCIDRs(raw string) []*net.IPNet {
	if raw == "" {
		return nil
	}

	var nets []*net.IPNet
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if strings.EqualFold(tok, "cloudflare") {
			for _, cidr := range cloudflareCIDRs {
				_, n, err := net.ParseCIDR(cidr)
				if err != nil {
					slog.Error("invalid built-in cloudflare CIDR", "cidr", cidr, "error", err)
					continue
				}
				nets = append(nets, n)
			}
			continue
		}
		_, n, err := net.ParseCIDR(tok)
		if err != nil {
			slog.Error("invalid trusted proxy CIDR, skipping", "cidr", tok, "error", err)
			continue
		}
		nets = append(nets, n)
	}
	if len(nets) > 0 {
		slog.Info("trusted proxy CIDRs loaded", "count", len(nets))
	}
	return nets
}

// builtinCloudflareNets holds the parsed cloudflareCIDRs. CF-Connecting-IP is
// only honored when the direct peer is inside one of these ranges, since any
// other proxy may forward a client-supplied value unchanged.
var builtinCloudflareNets = func() []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cloudflareCIDRs))
	for _, cidr := range cloudflareCIDRs {
		_, n, err := net.ParseCIDR(cidr)
		if err != nil {
			panic("invalid built-in cloudflare CIDR " + cidr)
		}
		nets = append(nets, n)
	}
	return nets
}()

// trustedProxyRealIP returns middleware that extracts the real client IP from
// proxy headers, but ONLY when the direct connection comes from a trusted
// proxy IP. When trustedNets is nil or empty, RemoteAddr is never overwritten.
//
// Resolution order for a trusted peer:
//  1. CF-Connecting-IP, only when the peer is in Cloudflare's published ranges.
//  2. X-Forwarded-For, walked from the right, skipping trusted hops. The first
//     untrusted entry is the client. Proxies append the address they saw, so
//     entries to the left of that point are client-controlled and ignored.
//  3. X-Real-IP, only when no X-Forwarded-For header is present.
//
// Every candidate must parse as an IP. Anything else leaves RemoteAddr as the
// peer address.
func trustedProxyRealIP(trustedNets []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedNets) > 0 && isTrustedProxy(r.RemoteAddr, trustedNets) {
				if ip := clientIPFromHeaders(r, trustedNets); ip != "" {
					r.RemoteAddr = ip
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientIPFromHeaders returns the client IP derived from proxy headers, or ""
// when the headers do not yield a valid address. The caller has already
// checked that the peer is a trusted proxy.
func clientIPFromHeaders(r *http.Request, trustedNets []*net.IPNet) string {
	if isTrustedProxy(r.RemoteAddr, builtinCloudflareNets) {
		if ip := parseIPString(r.Header.Get("CF-Connecting-IP")); ip != "" {
			return ip
		}
	}

	if xffValues := r.Header.Values("X-Forwarded-For"); len(xffValues) > 0 {
		return rightmostUntrustedXFF(strings.Join(xffValues, ","), trustedNets)
	}

	return parseIPString(r.Header.Get("X-Real-IP"))
}

// rightmostUntrustedXFF walks an X-Forwarded-For list from the right and
// returns the first entry that is not a trusted proxy. A malformed entry stops
// the walk and yields "" so the caller keeps the peer address. When every
// entry is trusted the list names no client, so it also yields "" rather than
// adopting the leftmost, client-controlled entry.
func rightmostUntrustedXFF(xff string, trustedNets []*net.IPNet) string {
	parts := strings.Split(xff, ",")
	for i := len(parts) - 1; i >= 0; i-- {
		candidate := strings.TrimSpace(parts[i])
		if candidate == "" {
			continue
		}
		ip := parseIPString(candidate)
		if ip == "" {
			return ""
		}
		if !isTrustedProxy(ip, trustedNets) {
			return ip
		}
	}
	return ""
}

// parseIPString returns the canonical form of s when it is a bare IPv4 or
// IPv6 address, or "" otherwise.
func parseIPString(s string) string {
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return ""
	}
	return ip.String()
}

// isTrustedProxy checks whether the remote address belongs to any of the
// trusted networks. It handles the "ip:port" format of RemoteAddr.
func isTrustedProxy(remoteAddr string, nets []*net.IPNet) bool {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		// RemoteAddr may be a bare IP (e.g., when set by a prior middleware).
		host = remoteAddr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
