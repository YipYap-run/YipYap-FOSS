package checker

import (
	"errors"
	"testing"
)

func TestValidateControlPlaneHost(t *testing.T) {
	orig := AllowPrivateControlPlaneTargets
	AllowPrivateControlPlaneTargets = false
	t.Cleanup(func() { AllowPrivateControlPlaneTargets = orig })

	for _, h := range []string{"10.0.0.1", "127.0.0.1", "[::1]", "::1", "169.254.169.254", "100.64.0.1", "0x7f000001", "localhost", "a.localhost"} {
		if err := ValidateControlPlaneHost(h); !errors.Is(err, ErrControlPlanePrivateTarget) {
			t.Errorf("ValidateControlPlaneHost(%q) = %v, want ErrControlPlanePrivateTarget", h, err)
		}
	}
	for _, h := range []string{"8.8.8.8", "2001:4860:4860::8888"} {
		if err := ValidateControlPlaneHost(h); err != nil {
			t.Errorf("ValidateControlPlaneHost(%q) = %v, want nil", h, err)
		}
	}
	if err := ValidateControlPlaneHost(""); err == nil {
		t.Error("empty host accepted")
	}
	if err := ValidateControlPlaneHost("bad;host"); err == nil || errors.Is(err, ErrControlPlanePrivateTarget) {
		t.Errorf("metacharacter host: %v", err)
	}

	AllowPrivateControlPlaneTargets = true
	if err := ValidateControlPlaneHost("10.0.0.1"); err != nil {
		t.Errorf("opt-in still blocked: %v", err)
	}
}
