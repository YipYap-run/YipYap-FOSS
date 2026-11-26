package handlers

import (
	"net"
	"net/http"

	"github.com/go-chi/httprate"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
)

// RateLimitKeyByOrg is an httprate.KeyFunc that keys on the
// authenticated org ID. Requires auth middleware to have populated
// claims on the request context (i.e. use this AFTER authMiddleware).
// Requests without claims fall back to "anonymous", those are 401'd
// before reaching the handler but keying defensively avoids a nil
// panic inside httprate.
func RateLimitKeyByOrg(r *http.Request) (string, error) {
	if claims := middleware.GetClaims(r.Context()); claims != nil && claims.OrgID != "" {
		return "org:" + claims.OrgID, nil
	}
	return "anonymous", nil
}

// RateLimitKeyByClientIP keys rate limits on the resolved client IP. The
// trustedProxyRealIP middleware already overwrites r.RemoteAddr with the
// trusted client IP (CF-Connecting-IP / X-Real-IP / X-Forwarded-For) when the
// direct peer is a trusted proxy, so RemoteAddr is the right key here.
// CanonicalizeIP buckets IPv6 by /64.
func RateLimitKeyByClientIP(r *http.Request) (string, error) {
	ip := r.RemoteAddr
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	return httprate.CanonicalizeIP(ip), nil
}
