package checker

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"
)

// ErrSSRFBlocked is returned by SSRFGuardedDialContext when the resolved
// peer address falls in a private/internal range. The notify/providers
// package keeps its own ErrSSRFBlocked sentinel for backward compat with
// callers that errors.Is() against it; this one is independent so
// non-provider call sites (e.g. the OIDC verifier transport) can match
// without importing the provider package.
var ErrSSRFBlocked = errors.New("checker: resolved address is in a blocked range")

// SSRFGuardedDialContext wraps a base DialContext with an SSRF check that
// fires at TCP-connect time. The check re-resolves the host for every
// connection so a DNS rebind that flips a public A record to
// 169.254.169.254 between create-time validation and Dial is rejected
// before bytes leave the process. Redirects follow the same DialContext
// because http.Client.Do issues fresh requests through the same
// transport, so a 302 to a private address is also caught.
//
// Operators who genuinely want to deliver control-plane traffic to a
// private host (development against localhost, intra-cluster Kubernetes
// services on 10.0.0.0/8) opt in via
// YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true; this wrapper then
// reduces to a direct pass-through.
//
// Distinct from the provider-internal ssrfGuardedDialContext, which also
// covers CGNAT (100.64/10) and IPv6 ULA (fc00::/7). The check here goes
// through validateHost so it sees the same blocked ranges as
// ValidateControlPlaneHTTPTarget, including the IPv4-compat IPv6
// (R2-H4) and non-standard IP forms (decimal/hex/octal). Use this for
// new call sites such as the OIDC verifier discovery and JWKS-fetch
// transports (A2-NM-1).
func SSRFGuardedDialContext(base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if AllowPrivateControlPlaneTargets {
			return base(ctx, network, addr)
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			// Some networks (unix sockets etc.) aren't host:port, let the
			// base dialer surface the real error rather than reject here.
			return base(ctx, network, addr)
		}
		// validateHost already handles literal IPs (including non-standard
		// hex/decimal/octal forms and the IPv4-compat IPv6 that R2-H4
		// fixed) AND DNS-resolved hostnames. Any rejection from
		// validateHost is an SSRF block, translate to ErrSSRFBlocked.
		if err := validateHost(host); err != nil {
			return nil, fmt.Errorf("%w: %s (%v)", ErrSSRFBlocked, host, err)
		}
		return base(ctx, network, addr)
	}
}

// NewSSRFGuardedHTTPClient returns an *http.Client whose Transport uses
// SSRFGuardedDialContext to re-check the resolved peer address at every
// TCP connect. The returned client honours HTTP_PROXY / HTTPS_PROXY via
// http.ProxyFromEnvironment and applies the supplied wall-clock timeout
// to every request.
//
// Use for OIDC discovery and JWKS-fetch transports so the Issuer +
// JWKSURL paths are SSRF-protected even though the OIDC library
// otherwise reaches for http.DefaultClient. See A2-NM-1.
func NewSSRFGuardedHTTPClient(timeout time.Duration) *http.Client {
	baseDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           SSRFGuardedDialContext(baseDialer.DialContext),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
	}
}
