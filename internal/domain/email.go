package domain

import (
	"errors"
	"net/mail"
	"strings"
)

// MaxEmailLength is the longest address accepted (RFC 5321 path limit).
const MaxEmailLength = 254

// ErrInvalidEmail is returned by ValidateEmail for input that is not a bare
// email address.
var ErrInvalidEmail = errors.New("enter a valid email address")

// NormalizeEmail trims surrounding space and lower-cases an address. Every
// entry point and store lookup uses it, so sign-in, reset and uniqueness do
// not depend on the case the user typed.
func NormalizeEmail(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateEmail checks that s (already normalized) is a bare address such as
// "name@example.com": no display name, no angle brackets, one local part and
// one domain with a dot.
func ValidateEmail(s string) error {
	if s == "" || len(s) > MaxEmailLength || strings.ContainsAny(s, " \t\r\n<>,;") {
		return ErrInvalidEmail
	}
	addr, err := mail.ParseAddress(s)
	if err != nil || addr.Name != "" || addr.Address != s {
		return ErrInvalidEmail
	}
	at := strings.LastIndexByte(s, '@')
	if at <= 0 || at == len(s)-1 {
		return ErrInvalidEmail
	}
	host := s[at+1:]
	if !strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return ErrInvalidEmail
	}
	return nil
}
