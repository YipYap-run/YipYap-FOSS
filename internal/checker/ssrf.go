package checker

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// AllowPrivateTargets disables SSRF protection. In SaaS mode this defaults to
// false; in FOSS/self-hosted mode it defaults to true so operators can monitor
// their own internal networks. Override via YIPYAP_ALLOW_PRIVATE_TARGETS.
var AllowPrivateTargets bool

// ValidateHTTPTarget checks that an HTTP monitor URL does not target private/internal networks.
func ValidateHTTPTarget(rawURL string) error {
	if AllowPrivateTargets {
		return nil
	}
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
	// Also catch 0.0.0.0 (unspecified) which binds to all interfaces.
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified()
}
