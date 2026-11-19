package auth

import "golang.org/x/crypto/bcrypt"

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	return string(hash), err
}

func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// dummyHash is a fixed cost-12 hash used to equalize timing when a lookup
// misses, so an attacker cannot distinguish "user exists" from "user does not
// exist" by timing. The cost matches HashPassword so the comparison takes the
// same wall-clock time as a real one.
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("yipyap-dummy-password-for-timing"), 12)
	if err != nil {
		return nil
	}
	return h
}()

// DummyVerify runs a bcrypt comparison against a fixed cost-12 hash so a
// failed login for a nonexistent user takes the same time as a real one.
func DummyVerify(password string) error {
	if dummyHash == nil {
		return bcrypt.ErrHashTooShort
	}
	return bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}
