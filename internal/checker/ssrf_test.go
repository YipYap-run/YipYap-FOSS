package checker

import (
	"errors"
	"strings"
	"testing"
)

func TestParseNonStandardIP(t *testing.T) {
	tests := []struct {
		input    string
		wantNil  bool
		wantStr  string
	}{
		// Decimal
		{"2130706433", false, "127.0.0.1"},
		// Hex
		{"0x7f000001", false, "127.0.0.1"},
		// Octal
		{"0177.0.0.1", false, "127.0.0.1"},
		// Dotted hex
		{"0x7f.0x0.0x0.0x1", false, "127.0.0.1"},
		// Short-form
		{"127.1", false, "127.0.0.1"},
		// Zero
		{"0", false, "0.0.0.0"},
		// Regular dotted-decimal should return nil (handled by net.ParseIP)
		{"127.0.0.1", true, ""},
		{"10.0.0.1", true, ""},
		// Garbage
		{"notanip", true, ""},
		{"", true, ""},
	}
	for _, tt := range tests {
		ip := parseNonStandardIP(tt.input)
		if tt.wantNil {
			if ip != nil {
				t.Errorf("parseNonStandardIP(%q) = %v, want nil", tt.input, ip)
			}
			continue
		}
		if ip == nil {
			t.Errorf("parseNonStandardIP(%q) = nil, want %s", tt.input, tt.wantStr)
			continue
		}
		if ip.String() != tt.wantStr {
			t.Errorf("parseNonStandardIP(%q) = %s, want %s", tt.input, ip.String(), tt.wantStr)
		}
	}
}

func TestValidateTarget_BlocksNonStandardIPs(t *testing.T) {
	// Ensure private targets are NOT allowed for this test.
	origAllow := AllowPrivateTargets
	AllowPrivateTargets = false
	defer func() { AllowPrivateTargets = origAllow }()

	blocked := []string{
		"127.0.0.1",
		"2130706433",       // decimal 127.0.0.1
		"0x7f000001",       // hex 127.0.0.1
		"0177.0.0.1",       // octal 127.0.0.1
		"0x7f.0x0.0x0.0x1", // dotted hex
		"127.1",            // short-form
		"0",                // 0.0.0.0
		"10.0.0.1",
		"192.168.1.1",
		"172.16.0.1",
		"169.254.169.254",
		"::1",
	}
	for _, host := range blocked {
		if err := ValidateTarget(host); err == nil {
			t.Errorf("ValidateTarget(%q) = nil, want error (should be blocked)", host)
		}
	}

	allowed := []string{
		"8.8.8.8",
		"1.1.1.1",
		"example.com",
	}
	for _, host := range allowed {
		if err := ValidateTarget(host); err != nil {
			t.Errorf("ValidateTarget(%q) = %v, want nil (should be allowed)", host, err)
		}
	}
}

func TestValidateHTTPTarget_BlocksSchemes(t *testing.T) {
	origAllow := AllowPrivateTargets
	AllowPrivateTargets = false
	defer func() { AllowPrivateTargets = origAllow }()

	blocked := []string{
		"gopher://127.0.0.1:6379/_INFO",
		"file:///etc/passwd",
		"dict://127.0.0.1:11211/stats",
		"ftp://127.0.0.1/",
		"javascript:alert(1)",
	}
	for _, u := range blocked {
		if err := ValidateHTTPTarget(u); err == nil {
			t.Errorf("ValidateHTTPTarget(%q) = nil, want error (should be blocked)", u)
		}
	}

	allowed := []string{
		"https://example.com",
		"http://example.com",
	}
	for _, u := range allowed {
		if err := ValidateHTTPTarget(u); err != nil {
			t.Errorf("ValidateHTTPTarget(%q) = %v, want nil", u, err)
		}
	}
}

func TestValidateTarget_AllowsPrivateWhenConfigured(t *testing.T) {
	origAllow := AllowPrivateTargets
	AllowPrivateTargets = true
	defer func() { AllowPrivateTargets = origAllow }()

	// Everything should pass when AllowPrivateTargets is true.
	targets := []string{"127.0.0.1", "2130706433", "10.0.0.1", "0x7f000001"}
	for _, host := range targets {
		if err := ValidateTarget(host); err != nil {
			t.Errorf("ValidateTarget(%q) with AllowPrivateTargets=true: %v", host, err)
		}
	}
}

// TestValidateControlPlaneHTTPTarget_SplitFromMonitorToggle is the H-2
// regression: with AllowPrivateTargets=true (FOSS-style intranet
// monitoring) AND AllowPrivateControlPlaneTargets=false (default), a
// private monitor target is allowed while a private control-plane URL
// (sink_url, token_url, issuer) is still blocked.
func TestValidateControlPlaneHTTPTarget_SplitFromMonitorToggle(t *testing.T) {
	origMonitor := AllowPrivateTargets
	origCP := AllowPrivateControlPlaneTargets
	defer func() {
		AllowPrivateTargets = origMonitor
		AllowPrivateControlPlaneTargets = origCP
	}()

	// FOSS-like default: intranet monitoring enabled; control-plane default-deny.
	AllowPrivateTargets = true
	AllowPrivateControlPlaneTargets = false

	// Monitor side: private hosts allowed.
	for _, host := range []string{"10.0.0.1", "192.168.1.10", "127.0.0.1"} {
		if err := ValidateTarget(host); err != nil {
			t.Errorf("ValidateTarget(%q) with AllowPrivateTargets=true: %v (must allow intranet monitor)", host, err)
		}
	}
	for _, u := range []string{"http://10.0.0.1/healthz", "https://192.168.1.10/"} {
		if err := ValidateHTTPTarget(u); err != nil {
			t.Errorf("ValidateHTTPTarget(%q) with AllowPrivateTargets=true: %v (must allow intranet monitor)", u, err)
		}
	}

	// Control-plane side: the SAME private hosts must still be rejected.
	blockedCP := []string{
		"http://10.0.0.1/",
		"https://10.0.0.1/",
		"http://169.254.169.254/latest/api/token",
		"https://192.168.1.10/token",
		"http://127.0.0.1:8080/",
		"https://[::1]/",
	}
	for _, u := range blockedCP {
		if err := ValidateControlPlaneHTTPTarget(u); err == nil {
			t.Errorf("ValidateControlPlaneHTTPTarget(%q) = nil with AllowPrivateControlPlaneTargets=false; must block private/loopback", u)
		}
	}

	// Public is still fine.
	for _, u := range []string{"https://broker.example.com/ns/default", "https://issuer.example.com/token"} {
		if err := ValidateControlPlaneHTTPTarget(u); err != nil {
			t.Errorf("ValidateControlPlaneHTTPTarget(%q) = %v; must allow public", u, err)
		}
	}
}

// TestValidateControlPlaneHTTPTarget_OptInAllowsPrivate confirms the
// control-plane toggle actually relaxes when explicitly set. Needed for
// localhost CloudEvent development.
func TestValidateControlPlaneHTTPTarget_OptInAllowsPrivate(t *testing.T) {
	origCP := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = true
	defer func() { AllowPrivateControlPlaneTargets = origCP }()

	for _, u := range []string{"http://127.0.0.1:8080/", "http://localhost/", "http://10.0.0.1/"} {
		if err := ValidateControlPlaneHTTPTarget(u); err != nil {
			t.Errorf("ValidateControlPlaneHTTPTarget(%q) with opt-in: %v", u, err)
		}
	}
}

// TestValidateControlPlaneHTTPTarget_BlocksIPv4CompatV6 is the R2-H4
// regression. ::169.254.169.254 / ::10.0.0.1 etc. (RFC 4291 IPv4-compatible
// IPv6) were silently passing both isPrivateIP / isPrivateAddr because Go
// stdlib only unwraps the IPv4-MAPPED form via To4(). An attacker could
// phrase the EC2 metadata IP as ::169.254.169.254 to bypass SSRF gating.
func TestValidateControlPlaneHTTPTarget_BlocksIPv4CompatV6(t *testing.T) {
	origCP := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = false
	defer func() { AllowPrivateControlPlaneTargets = origCP }()

	blocked := []string{
		"http://[::169.254.169.254]/latest/api/token",
		"https://[::169.254.169.254]/latest/api/token",
		"http://[::10.0.0.1]/sink",
		"http://[::192.168.1.1]/sink",
		"http://[::172.16.0.1]/sink",
		"http://[::127.0.0.1]/sink",
	}
	for _, u := range blocked {
		if err := ValidateControlPlaneHTTPTarget(u); err == nil {
			t.Errorf("ValidateControlPlaneHTTPTarget(%q) = nil; must block IPv4-compat v6", u)
		}
	}

	bareHosts := []string{
		"::169.254.169.254",
		"::10.0.0.1",
		"::192.168.1.1",
		"::172.16.0.1",
		"::127.0.0.1",
	}
	for _, h := range bareHosts {
		if err := ValidateTarget(h); err == nil {
			t.Errorf("ValidateTarget(%q) = nil; must block IPv4-compat v6", h)
		}
	}
}

// TestValidateTarget_BlocksHexAndDecimalV4 confirms the existing
// non-standard-IP normaliser keeps catching hex/decimal v4 forms in the
// presence of the R2-H4 v4-compat-v6 unwrap. Belt-and-braces regression.
func TestValidateTarget_BlocksHexAndDecimalV4(t *testing.T) {
	origAllow := AllowPrivateTargets
	AllowPrivateTargets = false
	defer func() { AllowPrivateTargets = origAllow }()

	blocked := []string{
		"0xa9fea9fe", // hex 169.254.169.254
		"2852039166", // decimal 169.254.169.254
		"0x7f000001", // hex 127.0.0.1
		"2130706433", // decimal 127.0.0.1
	}
	for _, h := range blocked {
		if err := ValidateTarget(h); err == nil {
			t.Errorf("ValidateTarget(%q) = nil; must block (non-standard form of private v4)", h)
		}
	}
}

// TestValidateControlPlaneHTTPTarget_FieldErrorIsControlPlane is the
// A2-NL-1 regression at the checker level. Before the fix, a private
// control-plane URL came back through the same "monitors cannot target
// private…" error string used by the monitor-side check, so the 400
// body was misleading when the offender was a token_url or issuer.
// ValidateControlPlaneHTTPTarget now returns a wrapped
// ErrControlPlanePrivateTarget so callers can errors.Is() and the error
// text says "control-plane URL" rather than "monitors".
func TestValidateControlPlaneHTTPTarget_FieldErrorIsControlPlane(t *testing.T) {
	origCP := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = false
	defer func() { AllowPrivateControlPlaneTargets = origCP }()

	err := ValidateControlPlaneHTTPTarget("http://10.0.0.1/")
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrControlPlanePrivateTarget) {
		t.Errorf("err must wrap ErrControlPlanePrivateTarget; got %v", err)
	}
	msg := err.Error()
	if strings.Contains(msg, "monitors cannot target") {
		t.Errorf("error must not use monitor-flavoured wording; got %q", msg)
	}
	if !strings.Contains(msg, "control-plane") {
		t.Errorf("error must identify control-plane category; got %q", msg)
	}
}
