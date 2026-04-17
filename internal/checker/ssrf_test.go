package checker

import (
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
