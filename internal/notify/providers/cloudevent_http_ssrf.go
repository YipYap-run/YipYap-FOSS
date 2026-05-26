package providers

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
)

// ErrSSRFBlocked is returned by the provider's DialContext when the
// resolved peer address falls in a blocked range. Wrapped into the
// returned error so callers can errors.Is() it.
var ErrSSRFBlocked = errors.New("cloudevent_http: resolved address is in a blocked range")

// cgnatPrefix is RFC6598 carrier-grade NAT (100.64.0.0/10). Not caught by
// net.IP.IsPrivate() but SSRF-relevant, increasingly used on corporate
// networks where the Kubernetes pod CIDR overlaps.
var cgnatPrefix = netip.MustParsePrefix("100.64.0.0/10")

// ipv6ULAPrefix is the IPv6 Unique Local Address range (fc00::/7), which
// serves the same role as RFC1918 IPv4 private ranges.
var ipv6ULAPrefix = netip.MustParsePrefix("fc00::/7")

// ipv4CompatToV4 returns the embedded IPv4 address from an IPv4-compatible
// IPv6 representation (RFC 4291 §2.5.5.1, written ::a.b.c.d). Returns nil
// when ip is not v4-compat, when it's the IPv4-MAPPED form (already
// handled by To4), or when the trailing four bytes are 0 or 1 (::, ::1
//, handled separately by IsLoopback / IsUnspecified). See R2-H4.
func ipv4CompatToV4(ip net.IP) net.IP {
	if len(ip) != net.IPv6len {
		return nil
	}
	for i := 0; i < 10; i++ {
		if ip[i] != 0 {
			return nil
		}
	}
	if ip[10] == 0xff && ip[11] == 0xff {
		// IPv4-MAPPED, caller already unwrapped via To4().
		return nil
	}
	if ip[10] != 0 || ip[11] != 0 {
		return nil
	}
	if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && (ip[15] == 0 || ip[15] == 1) {
		return nil
	}
	return net.IPv4(ip[12], ip[13], ip[14], ip[15])
}

// isPrivateAddr reports whether the given net.IP falls in a range we
// consider SSRF-blocked. Returns a short why-string for error messages.
//
// Blocked ranges:
//   - Loopback (127.0.0.0/8, ::1)
//   - RFC1918 private (10/8, 172.16/12, 192.168/16)
//   - Link-local unicast (169.254/16, fe80::/10)
//   - Link-local multicast
//   - Unspecified (0.0.0.0, ::)
//   - CGNAT (100.64/10)
//   - IPv6 ULA (fc00::/7)
//   - Multicast (224/4, ff00::/8)
//
// IPv4-mapped IPv6 (::ffff:a.b.c.d) is unwrapped before checking so
// https://[::ffff:169.254.169.254] is blocked too.
func isPrivateAddr(ip net.IP) (bool, string) {
	if ip == nil {
		return true, "nil address"
	}
	// Canonicalise: unwrap IPv4-mapped-in-IPv6 so ::ffff:10.0.0.1 is
	// treated as 10.0.0.1.
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	// Also unwrap IPv4-compatible IPv6 (::a.b.c.d, RFC 4291 §2.5.5.1).
	// To4() does NOT cover this form, it's treated as a generic v6
	// address and slips past IsPrivate/IsLinkLocalUnicast. Without this,
	// ::169.254.169.254 / ::10.0.0.1 reach the EC2 metadata service. R2-H4.
	if v4 := ipv4CompatToV4(ip); v4 != nil {
		ip = v4
	}

	if ip.IsLoopback() {
		return true, "loopback"
	}
	if ip.IsUnspecified() {
		return true, "unspecified"
	}
	if ip.IsLinkLocalUnicast() {
		return true, "link-local"
	}
	if ip.IsLinkLocalMulticast() {
		return true, "link-local multicast"
	}
	if ip.IsMulticast() {
		return true, "multicast"
	}
	if ip.IsPrivate() {
		return true, "rfc1918 / rfc4193"
	}

	// net.IP → netip.Addr for prefix checks that net.IP doesn't cover.
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return false, ""
	}
	// netip.AddrFromSlice wraps IPv4 as a 16-byte v4-in-v6 representation.
	// Unwrap so CGNAT is recognised on real v4 addrs.
	if addr.Is4In6() {
		addr = addr.Unmap()
	}
	if addr.Is4() && cgnatPrefix.Contains(addr) {
		return true, "cgnat"
	}
	if addr.Is6() && ipv6ULAPrefix.Contains(addr) {
		return true, "ipv6 ula"
	}
	return false, ""
}

// isPrivateHost resolves host (which may be a literal IP or a DNS name)
// and reports whether ANY resolved address is in a blocked range. A host
// resolving to a mix of public+private is blocked, the attacker's
// malicious record is enough.
//
// A host of "localhost" returns (true, "loopback") deterministically
// without DNS. A DNS resolution failure returns (false, ""), the caller
// is expected to treat unresolvable hosts as "cannot evaluate" and allow
// the send to reach the Dial path, which will re-resolve and surface a
// real error there.
func isPrivateHost(host string) (bool, string) {
	if host == "" {
		return true, "empty host"
	}
	// Literal IP fast path.
	if ip := net.ParseIP(host); ip != nil {
		if blocked, why := isPrivateAddr(ip); blocked {
			return true, why
		}
		return false, ""
	}
	// RFC 6761: "localhost" and any name ending in ".localhost" are
	// reserved and MUST be treated as loopback regardless of what DNS or
	// /etc/hosts says. Returning early is both correct (RFC-mandated)
	// and portable across environments where /etc/hosts may or may not
	// have the entry, CI runners typically don't, which made the
	// previous DNS-only path platform-dependent.
	lower := strings.ToLower(host)
	if lower == "localhost" || strings.HasSuffix(lower, ".localhost") {
		return true, "loopback"
	}
	// Name resolution for everything else.
	ips, err := net.LookupIP(host)
	if err != nil {
		// Can't evaluate; let Dial surface the real error.
		return false, ""
	}
	for _, ip := range ips {
		if blocked, why := isPrivateAddr(ip); blocked {
			return true, fmt.Sprintf("%s resolves to %s [%s]", host, ip, why)
		}
	}
	return false, ""
}

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
		if blocked, why := isPrivateHost(host); blocked {
			return nil, fmt.Errorf("%w: %s", ErrSSRFBlocked, why)
		}
		return base(ctx, network, addr)
	}
}
