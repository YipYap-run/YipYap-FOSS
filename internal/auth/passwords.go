package auth

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// passwordCost is the bcrypt cost for stored hashes and the timing dummy.
// Test binaries use the minimum cost: cost 12 dominates the suite's CPU time
// (several times over under -race) and tests never depend on the work factor.
// Production binaries are never test binaries, so they always get 12.
var passwordCost = func() int {
	if testing.Testing() {
		return bcrypt.MinCost
	}
	return 12
}()

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	return string(hash), err
}

func VerifyPassword(hash, password string) error {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}

// dummyHash is a fixed hash at passwordCost used to equalize timing when a
// lookup misses, so an attacker cannot distinguish "user exists" from "user
// does not exist" by timing. The cost matches HashPassword so the comparison
// takes the same wall-clock time as a real one.
var dummyHash = func() []byte {
	h, err := bcrypt.GenerateFromPassword([]byte("yipyap-dummy-password-for-timing"), passwordCost)
	if err != nil {
		return nil
	}
	return h
}()

// DummyVerify runs a bcrypt comparison against a fixed hash at passwordCost
// so a failed login for a nonexistent user takes the same time as a real one.
func DummyVerify(password string) error {
	if dummyHash == nil {
		return bcrypt.ErrHashTooShort
	}
	return bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
}
