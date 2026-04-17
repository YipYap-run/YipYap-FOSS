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

// trustedProxyRealIP returns middleware that extracts the real client IP from
// proxy headers, but ONLY when the direct connection comes from a trusted
// proxy IP. When trustedNets is nil or empty, RemoteAddr is never overwritten.
func trustedProxyRealIP(trustedNets []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(trustedNets) > 0 && isTrustedProxy(r.RemoteAddr, trustedNets) {
				if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
					r.RemoteAddr = cfIP
				} else if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
					r.RemoteAddr = xrip
				} else if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					if i := strings.Index(xff, ","); i > 0 {
						r.RemoteAddr = strings.TrimSpace(xff[:i])
					} else {
						r.RemoteAddr = strings.TrimSpace(xff)
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
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
