package checker

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// AllowPolicy reports whether private destinations are allowed right now.
// It is read on every connection so tests and config reloads see the
// current toggle value.
type AllowPolicy func() bool

// MonitorTargetPolicy follows AllowPrivateTargets, the same toggle that
// ValidateHTTPTarget uses when a webhook or ntfy URL is saved, so the
// delivery-time check never rejects what create-time validation accepted.
func MonitorTargetPolicy() bool { return AllowPrivateTargets }

// ControlPlanePolicy follows AllowPrivateControlPlaneTargets, the toggle
// behind ValidateControlPlaneHTTPTarget (CloudEvent sinks, OIDC URLs).
func ControlPlanePolicy() bool { return AllowPrivateControlPlaneTargets }

// guardedControl returns a net.Dialer Control hook that rejects the
// connection when the socket address it is about to connect to is in a
// blocked range. Control runs after name resolution, on the exact IP the
// kernel will connect to, so a DNS answer that changes between a pre-check
// and the connect (rebinding) cannot slip a private address through.
func guardedControl(allow AllowPolicy) func(network, address string, c syscall.RawConn) error {
	return func(network, address string, _ syscall.RawConn) error {
		if allow != nil && allow() {
			return nil
		}
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return fmt.Errorf("%w: %s", ErrSSRFBlocked, address)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			return fmt.Errorf("%w: %s", ErrSSRFBlocked, host)
		}
		if blocked, why := IsPrivateAddr(ip); blocked {
			return fmt.Errorf("%w: %s (%s)", ErrSSRFBlocked, ip, why)
		}
		return nil
	}
}

// guardedDialContext resolves the host itself, refuses the dial when any
// answer is in a blocked range, and then connects to the checked IPs
// directly, so the address that was checked is the address that is used.
// The dialer's Control hook re-checks the socket address as a second
// line. TLS still verifies against the URL hostname because the transport
// sets SNI from the request, not from the dialed address.
func guardedDialContext(allow AllowPolicy) func(ctx context.Context, network, addr string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
		Control:   guardedControl(allow),
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		if allow != nil && allow() {
			return d.DialContext(ctx, network, addr)
		}
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", ErrSSRFBlocked, addr)
		}
		ip := net.ParseIP(host)
		if ip == nil {
			ip = parseNonStandardIP(host)
		}
		if ip != nil {
			if blocked, why := IsPrivateAddr(ip); blocked {
				return nil, fmt.Errorf("%w: %s (%s)", ErrSSRFBlocked, ip, why)
			}
			return d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		}
		lower := strings.ToLower(host)
		if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
			return nil, fmt.Errorf("%w: %s (loopback)", ErrSSRFBlocked, host)
		}
		answers, err := lookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		if len(answers) == 0 {
			return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
		}
		for _, a := range answers {
			if blocked, why := IsPrivateAddr(a.IP); blocked {
				return nil, fmt.Errorf("%w: %s resolves to %s (%s)", ErrSSRFBlocked, host, a.IP, why)
			}
		}
		var lastErr error
		for _, a := range answers {
			conn, err := d.DialContext(ctx, network, net.JoinHostPort(a.IP.String(), port))
			if err == nil {
				return conn, nil
			}
			lastErr = err
			if ctx.Err() != nil {
				break
			}
		}
		return nil, lastErr
	}
}

// NewGuardedHTTPClient returns the HTTP client for requests to URLs that
// an org member typed in (webhooks, ntfy servers, CloudEvent sinks, OIDC
// token endpoints). Unless allow reports true:
//
//   - every TCP connect is refused when the peer IP is loopback, private,
//     link-local, CGNAT, ULA, multicast or unspecified, checked on the
//     resolved address so DNS rebinding does not get around it;
//   - redirects are not followed, so a public endpoint cannot bounce the
//     request (and any custom headers) to another host;
//   - proxy environment variables are ignored, because a proxy would
//     resolve and connect to the target where this check cannot see it.
//
// A redirect response is returned to the caller as-is (3xx status).
func NewGuardedHTTPClient(timeout time.Duration, allow AllowPolicy) *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           guardedDialContext(allow),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}
