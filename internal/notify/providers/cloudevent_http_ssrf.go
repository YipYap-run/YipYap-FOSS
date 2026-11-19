package providers

import (
	"context"
	"fmt"
	"net"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// ssrfGuardedDialContext wraps a base DialContext with an SSRF check that
// fires at TCP-connect time. Unlike a string-matching pre-send validator
// this also defeats DNS rebinding: the attacker can flip the A record to
// 169.254.169.254 between create-time validation and Dial, but Dial
// re-resolves and this guard catches it (H-4).
//
// Redirect targets are also covered because http.Client.Do follows
// redirects by issuing fresh requests that go through the same
// DialContext.
//
// Opting out via YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true reduces
// this wrapper to a direct pass-through so localhost CloudEvent
// development still works.
func ssrfGuardedDialContext(base func(ctx context.Context, network, addr string) (net.Conn, error)) func(ctx context.Context, network, addr string) (net.Conn, error) {
	if base == nil {
		base = (&net.Dialer{}).DialContext
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if checker.AllowPrivateControlPlaneTargets {
			return base(ctx, network, addr)
		}
		host, _, err := net.SplitHostPort(addr)
		if err != nil {
			// Some networks (unix sockets etc.) aren't host:port, pass through.
			return base(ctx, network, addr)
		}
		if blocked, why := checker.IsPrivateHost(host); blocked {
			return nil, fmt.Errorf("%w: %s", checker.ErrSSRFBlocked, why)
		}
		return base(ctx, network, addr)
	}
}
