package checker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// ErrUnresolvable is returned when a target host name has no addresses.
// Monitor saves reject such names (mon-4): a name that does not resolve
// at save time cannot be vetted, and it could later be pointed at an
// internal address.
var ErrUnresolvable = errors.New("host name does not resolve")

// lookupIPAddr resolves host names for both save-time validation and
// dial-time vetting. Tests replace it to simulate DNS rebinding.
var lookupIPAddr = net.DefaultResolver.LookupIPAddr

// validateResolveTimeout bounds the DNS lookup done while a monitor or
// control-plane URL is saved.
const validateResolveTimeout = 5 * time.Second

// dialFunc matches net.Dialer.DialContext.
type dialFunc func(ctx context.Context, network, addr string) (net.Conn, error)

// monitorBaseDial performs the actual connect for monitor checks once the
// address has been vetted. The per-check context carries the deadline.
// Tests replace it to route vetted addresses to local listeners.
var monitorBaseDial dialFunc = (&net.Dialer{KeepAlive: 30 * time.Second}).DialContext

// ConfigureTargets sets both SSRF toggles from process config and logs
// when either is relaxed. Every binary that runs checks or delivers
// control-plane traffic calls it at startup so the split binaries behave
// like the monolith.
func ConfigureTargets(allowPrivate, allowPrivateControlPlane bool) {
	AllowPrivateTargets = allowPrivate
	AllowPrivateControlPlaneTargets = allowPrivateControlPlane
	if allowPrivate {
		slog.Warn("SSRF protection: private/internal MONITOR targets ALLOWED (self-hosted mode)")
	}
	if allowPrivateControlPlane {
		slog.Warn("SSRF protection: private/internal CONTROL-PLANE targets ALLOWED (sink/OIDC URLs accept localhost + RFC1918)")
	}
}

// resolveVetted resolves host exactly once and returns its addresses only
// if every one of them is outside the blocked ranges. Literal IPs,
// including decimal/hex/octal/short forms, are vetted without DNS.
// Callers dial the returned addresses directly so a second lookup (and
// with it a DNS rebind) never happens.
func resolveVetted(ctx context.Context, host string) ([]net.IP, error) {
	if host == "" {
		return nil, errors.New("missing host")
	}
	// Shell metacharacters never appear in a valid host and are dangerous
	// if the host reaches exec.Command (ping).
	if strings.ContainsAny(host, ";|&$`\\\"'(){}[]<>!\n\r\t ") {
		return nil, errors.New("hostname contains invalid characters")
	}
	if strings.HasPrefix(host, "-") {
		return nil, errors.New("hostname contains invalid characters")
	}

	if ip := net.ParseIP(host); ip != nil {
		if blocked, _ := IsPrivateAddr(ip); blocked {
			return nil, errPrivate
		}
		return []net.IP{ip}, nil
	}
	if ip := parseNonStandardIP(host); ip != nil {
		if blocked, _ := IsPrivateAddr(ip); blocked {
			return nil, errPrivate
		}
		return []net.IP{ip}, nil
	}

	// RFC 6761: localhost and *.localhost are loopback whatever DNS says.
	lower := strings.ToLower(strings.TrimSuffix(host, "."))
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return nil, fmt.Errorf("%w (%s is loopback)", errPrivate, host)
	}

	addrs, err := lookupIPAddr(ctx, host)
	if err != nil || len(addrs) == 0 {
		// The resolver error is not included: it can name the internal
		// resolver address, which API callers should not see.
		return nil, fmt.Errorf("%w: %s", ErrUnresolvable, host)
	}
	ips := make([]net.IP, 0, len(addrs))
	for _, a := range addrs {
		if blocked, _ := IsPrivateAddr(a.IP); blocked {
			return nil, fmt.Errorf("%w (%s resolves to %s)", errPrivate, host, a.IP)
		}
		ips = append(ips, a.IP)
	}
	return ips, nil
}

// guardedDial resolves the host in addr once, refuses if any address is
// blocked, then connects to the vetted addresses in order. The base dialer
// only ever sees an IP literal, so it performs no lookup of its own.
func guardedDial(ctx context.Context, base dialFunc, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid address %q", ErrSSRFBlocked, addr)
	}
	ips, err := resolveVetted(ctx, host)
	if err != nil {
		if errors.Is(err, ErrUnresolvable) {
			return nil, err
		}
		// err already names the host and the address it resolved to; the
		// text lands in check history, so do not repeat the host.
		return nil, fmt.Errorf("%w: %w", ErrSSRFBlocked, err)
	}
	ips = filterFamily(network, ips)
	if len(ips) == 0 {
		return nil, fmt.Errorf("no %s address for %s", network, host)
	}
	var lastErr error
	for _, ip := range ips {
		conn, err := base(ctx, network, net.JoinHostPort(ip.String(), port))
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

// filterFamily drops addresses that cannot be used with a family-specific
// network such as "tcp4" or "udp6".
func filterFamily(network string, ips []net.IP) []net.IP {
	want4 := strings.HasSuffix(network, "4")
	want6 := strings.HasSuffix(network, "6")
	if !want4 && !want6 {
		return ips
	}
	out := ips[:0:0]
	for _, ip := range ips {
		is4 := ip.To4() != nil
		if (want4 && is4) || (want6 && !is4) {
			out = append(out, ip)
		}
	}
	return out
}

// monitorDialContext is the dialer for every monitor check that opens a
// socket (HTTP, TCP, the DNS nameserver). Governed by AllowPrivateTargets,
// read at dial time.
func monitorDialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if AllowPrivateTargets {
		return monitorBaseDial(ctx, network, addr)
	}
	return guardedDial(ctx, monitorBaseDial, network, addr)
}

// monitorPingTarget returns the address the ping check should probe. With
// private targets disallowed it resolves and vets the host once and
// returns a vetted IP literal (IPv4 preferred, since the raw socket path
// is ICMPv4), so the system ping command never does its own lookup.
func monitorPingTarget(ctx context.Context, host string) (string, error) {
	if host == "" {
		return "", errors.New("missing host")
	}
	if strings.HasPrefix(host, "-") {
		// Never let a host be read as a ping flag.
		return "", errors.New("hostname contains invalid characters")
	}
	if AllowPrivateTargets {
		return host, nil
	}
	ips, err := resolveVetted(ctx, host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return ips[0].String(), nil
}
