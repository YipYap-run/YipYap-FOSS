package domain

import "testing"

func TestNormalizeAndValidateEmail(t *testing.T) {
	if got := NormalizeEmail("  Jane.Doe@Example.COM \n"); got != "jane.doe@example.com" {
		t.Fatalf("NormalizeEmail = %q", got)
	}
	for _, ok := range []string{"a@b.co", "first.last+tag@sub.example.org"} {
		if err := ValidateEmail(ok); err != nil {
			t.Errorf("ValidateEmail(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "plain", "@example.com", "a@", "a@localhost", "Jane <j@x.com>", "a b@x.com", "a@x.com,b@y.com", "a@.com", "a@x.com."} {
		if err := ValidateEmail(bad); err == nil {
			t.Errorf("ValidateEmail(%q) accepted", bad)
		}
	}
}
