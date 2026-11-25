package checker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// ErrSSRFBlocked is returned by SSRFGuardedDialContext when the resolved
// peer address falls in a private/internal range.
var ErrSSRFBlocked = errors.New("checker: resolved address is in a blocked range")

// SSRFGuardedDialContext wraps a base DialContext with an SSRF check that
// fires at TCP-connect time. It resolves the host once, refuses when any
// address is in a blocked range, and hands the base dialer only the
// vetted IP literal, so a DNS rebind between the check and the connect
// has nothing to act on (mon-4). Redirects follow the same DialContext
// because http.Client.Do issues fresh requests through the same
// transport, so a 302 to a private address is also caught.
//
// Operators who genuinely want to deliver control-plane traffic to a
// private host (development against localhost, intra-cluster Kubernetes
// services on 10.0.0.0/8) opt in via
// YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true; this wrapper then
// reduces to a direct pass-through.
//
// Blocked ranges are those of IsPrivateAddr, the same set the save-time
// checks use. Use this for control-plane call sites such as the OIDC
// verifier discovery and JWKS-fetch transports (A2-NM-1). Monitor checks
// use monitorDialContext, governed by AllowPrivateTargets instead.
func SSRFGuardedDialContext(base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if AllowPrivateControlPlaneTargets {
			return base(ctx, network, addr)
		}
		return guardedDial(ctx, base, network, addr)
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
