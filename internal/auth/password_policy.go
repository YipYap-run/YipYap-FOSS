package auth

import (
	"errors"
	"fmt"
	"unicode/utf8"
)

// Password policy shared by every path that sets a password: registration,
// change, reset, admin-created users and admin temporary passwords. The SPA
// mirrors these values in web/src/utils/password-policy.js.
const (
	// MinPasswordLength is the minimum number of characters.
	MinPasswordLength = 8
	// MaxPasswordBytes is the bcrypt input limit. Longer input is refused
	// rather than silently truncated.
	MaxPasswordBytes = 72
)

var (
	// ErrPasswordTooShort is returned for passwords under MinPasswordLength.
	ErrPasswordTooShort = fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	// ErrPasswordTooLong is returned for passwords over MaxPasswordBytes.
	ErrPasswordTooLong = fmt.Errorf("password must be at most %d characters (accented and non-Latin characters count as more than one)", MaxPasswordBytes)
	// ErrPasswordInvalid is returned for passwords that are not valid UTF-8.
	ErrPasswordInvalid = errors.New("password contains invalid characters")
)

// ValidatePassword applies the shared password policy.
func ValidatePassword(pw string) error {
	if !utf8.ValidString(pw) {
		return ErrPasswordInvalid
	}
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return ErrPasswordTooShort
	}
	if len(pw) > MaxPasswordBytes {
		return ErrPasswordTooLong
	}
	return nil
}
