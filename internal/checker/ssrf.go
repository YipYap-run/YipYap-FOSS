package checker

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// ErrControlPlanePrivateTarget is the sentinel returned by
// ValidateControlPlaneHTTPTarget when the URL host resolves into a
// private/internal range. Callers should errors.Is() and re-wrap with
// the offending field name (sink_url / token_url / issuer / jwks_url) so
// a 400 response identifies which control-plane URL triggered the
// rejection. Distinct from the monitor-side errPrivate so error wording
// can differentiate. See A2-NL-1.
var ErrControlPlanePrivateTarget = errors.New("control-plane URL cannot target private/internal IP addresses")

// AllowPrivateTargets disables SSRF protection for MONITOR targets
// (HTTP / TCP / Ping / DNS uptime checks). In SaaS mode this defaults to
// false; in FOSS/self-hosted mode it defaults to true so operators can
// monitor their own internal networks. Override via
// YIPYAP_ALLOW_PRIVATE_TARGETS.
//
// This toggle does NOT govern control-plane targets (CloudEvent sink URLs,
// OIDC token_url / issuer / JWKS), those are gated separately by
// AllowPrivateControlPlaneTargets. Splitting the two lets an operator run
// intranet monitors without simultaneously opening an SSRF avenue through
// attacker-controllable control-plane URLs.
var AllowPrivateTargets bool

// AllowPrivateControlPlaneTargets disables SSRF protection for
// CONTROL-PLANE URLs only: CloudEvent sink_url, OIDC token_url, OIDC
// issuer, JWKS URLs. Defaults to false in every build, an operator who
// genuinely wants to deliver CloudEvents to localhost for development
// sets YIPYAP_ALLOW_PRIVATE_CONTROL_PLANE_TARGETS=true explicitly.
//
// Keep this distinct from AllowPrivateTargets: monitor targets are user-
// configured hosts the operator already trusts to observe; control-plane
// URLs are configured through the same API surface that may be reachable
// by less-privileged org members, so the default-deny posture matters.
var AllowPrivateControlPlaneTargets bool

// ValidateHTTPTarget checks that an HTTP monitor URL does not target
// private/internal networks. Governed by AllowPrivateTargets.
func ValidateHTTPTarget(rawURL string) error {
	if AllowPrivateTargets {
		return nil
	}
	return validateHTTPTargetStrict(rawURL)
}

// ValidateControlPlaneHTTPTarget checks that a control-plane HTTP URL
// (CloudEvent sink, OIDC endpoint, JWKS, etc.) does not target
// private/internal networks. Governed by AllowPrivateControlPlaneTargets,
// crucially NOT by AllowPrivateTargets. A FOSS deployment that legitimately
// monitors 10.0.0.0/8 does not also want attacker-submitted sink_url to
// point at 10.0.0.0/8.
//
// On a private-target hit the returned error wraps
// ErrControlPlanePrivateTarget so callers can errors.Is() and re-wrap
// with the offending field name (sink_url / token_url / issuer /
// jwks_url) when surfacing a 400 to the operator. See A2-NL-1.
func ValidateControlPlaneHTTPTarget(rawURL string) error {
	if AllowPrivateControlPlaneTargets {
		return nil
	}
	if err := validateHTTPTargetStrict(rawURL); err != nil {
		// Replace the monitor-flavoured "monitors cannot target ..." text
		// with the control-plane-flavoured sentinel. Parse-level errors
		// (bad scheme, missing host) are kept as-is so they still surface
		// useful detail; only the private/internal IP class is rewrapped.
		if errors.Is(err, errPrivate) || strings.Contains(err.Error(), "monitors cannot target private") {
			return fmt.Errorf("%w: %s", ErrControlPlanePrivateTarget, rawURL)
		}
		return err
	}
	return nil
}

// validateHTTPTargetStrict is the shared parse+host-check body used by
// both ValidateHTTPTarget and ValidateControlPlaneHTTPTarget. It ignores
// the two allow-private toggles, callers must consult the appropriate
// toggle before invoking.
func validateHTTPTargetStrict(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL")
	}

	// Only allow http and https schemes.
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("unsupported URL scheme %q", u.Scheme)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("missing hostname")
	}

	return validateHost(host)
}

// ValidateTarget checks that a hostname or IP address does not point to a
// private/internal network. Use this for non-HTTP monitor types (TCP, Ping, DNS).
func ValidateTarget(host string) error {
	if AllowPrivateTargets {
		return nil
	}
	if host == "" {
		return fmt.Errorf("missing host")
	}
	return validateHost(host)
}

// validateHost is the shared implementation that normalizes all IP
// representations (decimal, hex, octal, short-form) before checking.
// It also rejects hostnames containing shell metacharacters to prevent
// command injection when the host is passed to system commands (e.g., ping).
func validateHost(host string) error {
	// Reject shell metacharacters  - these should never appear in a valid
	// hostname or IP address and could be dangerous if passed to exec.Command.
	if strings.ContainsAny(host, ";|&$`\\\"'(){}[]<>!\n\r\t ") {
		return fmt.Errorf("hostname contains invalid characters")
	}

	// Step 1: Try net.ParseIP (handles standard dotted-decimal and IPv6).
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return errPrivate
		}
		return nil
	}

	// Step 2: Try parsing non-standard IP representations that net.ParseIP
	// doesn't handle: decimal (2130706433), hex (0x7f000001), octal
	// (0177.0.0.1), short-form (127.1), and zero (0).
	if ip := parseNonStandardIP(host); ip != nil {
		if isPrivateIP(ip) {
			return errPrivate
		}
		return nil
	}

	// Step 3: It's a hostname. Resolve via DNS and check all addresses.
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil // DNS failure at validation time; the checker will fail at runtime
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return fmt.Errorf("monitors cannot target private/internal IP addresses (%s resolves to %s)", host, ip)
		}
	}
	return nil
}

var errPrivate = fmt.Errorf("monitors cannot target private/internal IP addresses")

// parseNonStandardIP handles IP representations that Go's net.ParseIP does not:
//   - Pure decimal:    2130706433  → 127.0.0.1
//   - Hex:             0x7f000001  → 127.0.0.1
//   - Octal octets:    0177.0.0.1  → 127.0.0.1
//   - Dotted hex:      0x7f.0x0.0x0.0x1 → 127.0.0.1
//   - Short-form:      127.1       → 127.0.0.1
//   - Zero:            0           → 0.0.0.0
//   - IPv6 any:        ::          → ::
func parseNonStandardIP(host string) net.IP {
	// Strip brackets for IPv6 like [::]
	host = strings.TrimPrefix(strings.TrimSuffix(host, "]"), "[")

	// Try as a single integer (decimal or hex).
	if n, err := strconv.ParseUint(host, 0, 32); err == nil {
		return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
	}

	// Try dotted parts with octal/hex octets (e.g., 0177.0.0.1 or 0x7f.0.0.1)
	// and short-form (e.g., 127.1 → 127.0.0.1).
	parts := strings.Split(host, ".")
	if len(parts) < 2 || len(parts) > 4 {
		return nil
	}

	octets := make([]byte, 4)
	hasNonStandard := false

	for i, part := range parts {
		if i >= 4 {
			return nil
		}
		n, err := strconv.ParseUint(part, 0, 8)
		if err != nil {
			return nil
		}
		// Track whether any octet used non-decimal notation.
		if strings.HasPrefix(part, "0x") || strings.HasPrefix(part, "0X") ||
			(len(part) > 1 && part[0] == '0' && !strings.ContainsAny(part, "89")) {
			hasNonStandard = true
		}
		octets[i] = byte(n)
	}

	// Short-form: 127.1 means 127.0.0.1 (last part fills the remaining octets).
	if len(parts) < 4 {
		hasNonStandard = true
		// Move the last parsed value to the last octet position.
		octets[3] = octets[len(parts)-1]
		// Zero out the intermediate octets.
		for i := len(parts) - 1; i < 3; i++ {
			octets[i] = 0
		}
	}

	if !hasNonStandard {
		return nil // Standard dotted-decimal; net.ParseIP already handled this.
	}

	return net.IPv4(octets[0], octets[1], octets[2], octets[3])
}

func isPrivateIP(ip net.IP) bool {
	// Unwrap IPv4-compatible IPv6 (::a.b.c.d, RFC 4291 §2.5.5.1) before
	// the net.IP property checks. Go's stdlib only normalises the
	// IPv4-MAPPED form (::ffff:a.b.c.d) via To4(); the IPv4-COMPAT form
	// is treated as a generic v6 address and slips past IsPrivate /
	// IsLinkLocalUnicast for the metadata range. See R2-H4.
	if v4 := ipv4CompatToV4(ip); v4 != nil {
		ip = v4
	}
	// Also catch 0.0.0.0 (unspecified) which binds to all interfaces.
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}

// ipv4CompatToV4 returns the embedded IPv4 address from an IPv4-compatible
// IPv6 representation (RFC 4291 §2.5.5.1: 0:0:0:0:0:0:a.b.c.d, written
// ::a.b.c.d). Returns nil when ip is not an IPv4-compat v6, or when the
// embedded v4 is the all-zero or special unspecified ::1 (IPv6 loopback
// is already handled by ip.IsLoopback()).
//
// Distinct from net.IP.To4() which only handles IPv4-MAPPED IPv6
// (::ffff:a.b.c.d), Go treats those two RFC 4291 forms differently and
// only the mapped form auto-unwraps. SSRF protection must cover both, or
// an attacker can phrase 169.254.169.254 as ::169.254.169.254 and bypass.
func ipv4CompatToV4(ip net.IP) net.IP {
	// Only 16-byte (canonical IPv6) representations are candidates; a
	// 4-byte v4 already passes the check directly.
	if len(ip) != net.IPv6len {
		return nil
	}
	// Skip IPv4-MAPPED (::ffff:a.b.c.d): bytes 10-11 are 0xff,0xff. To4()
	// already handles this correctly elsewhere.
	for i := 0; i < 10; i++ {
		if ip[i] != 0 {
			return nil
		}
	}
	if ip[10] == 0xff && ip[11] == 0xff {
		return nil
	}
	if ip[10] != 0 || ip[11] != 0 {
		return nil
	}
	// Reject ::0 (unspecified) and ::1 (loopback), they're not v4-compat
	// addresses, the bare ::1 case is handled elsewhere by ip.IsLoopback().
	if ip[12] == 0 && ip[13] == 0 && ip[14] == 0 && (ip[15] == 0 || ip[15] == 1) {
		return nil
	}
	return net.IPv4(ip[12], ip[13], ip[14], ip[15])
}
