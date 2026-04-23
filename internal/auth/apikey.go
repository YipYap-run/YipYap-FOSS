package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// APIKeyHasher hashes API keys with HMAC-SHA256 using a secret key so that a
// database dump alone cannot be used to validate stolen keys offline.
type APIKeyHasher struct {
	secret []byte
}

// NewAPIKeyHasher returns an APIKeyHasher using the supplied secret.
// If secret is empty, HashAPIKey falls back to plain SHA-256 for backward
// compatibility with deployments that have not yet set the key.
func NewAPIKeyHasher(secret []byte) *APIKeyHasher {
	return &APIKeyHasher{secret: secret}
}

// Hash returns the HMAC-SHA256 (or plain SHA-256 when secret is empty) hex
// digest of the given plaintext API key.
func (h *APIKeyHasher) Hash(plaintext string) string {
	if len(h.secret) == 0 {
		return HashAPIKey(plaintext)
	}
	mac := hmac.New(sha256.New, h.secret)
	mac.Write([]byte(plaintext))
	return hex.EncodeToString(mac.Sum(nil))
}

// GenerateAPIKey produces a new API key returning (plaintext, hash, prefix, error).
// The plaintext has the form "yy_" + 32 random bytes hex-encoded.
// The prefix is the first 11 characters of plaintext.
// The hash is the HMAC-SHA256 hex digest of the plaintext using the hasher's secret.
func (h *APIKeyHasher) GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate random bytes: %w", err)
	}
	plaintext = "yy_" + hex.EncodeToString(buf)
	prefix = plaintext[:11]
	hash = h.Hash(plaintext)
	return plaintext, hash, prefix, nil
}

// GenerateAPIKey produces a new API key returning (plaintext, hash, prefix, error).
// The plaintext has the form "yy_" + 32 random bytes hex-encoded.
// The prefix is the first 11 characters of plaintext.
// The hash is the plain SHA-256 hex digest of the plaintext.
//
// Deprecated: use APIKeyHasher.GenerateAPIKey instead so that the hash is
// protected with HMAC-SHA256.
func GenerateAPIKey() (plaintext, hash, prefix string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", "", fmt.Errorf("generate random bytes: %w", err)
	}

	plaintext = "yy_" + hex.EncodeToString(buf)
	prefix = plaintext[:11]
	hash = HashAPIKey(plaintext)
	return plaintext, hash, prefix, nil
}

// HashAPIKey returns the plain SHA-256 hex digest of the given plaintext API key.
//
// Deprecated: use APIKeyHasher.Hash instead so that the hash is protected with
// HMAC-SHA256.
func HashAPIKey(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))
	return hex.EncodeToString(sum[:])
}
