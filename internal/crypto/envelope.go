package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// Envelope provides AES-256-GCM envelope encryption for notification config
// blobs. A nil *Envelope is safe to use and acts as a passthrough (no
// encryption), which is the default for embedded single-box mode.
type Envelope struct {
	gcm cipher.AEAD
}

// NewEnvelope creates an Envelope from a raw 32-byte key.
func NewEnvelope(key []byte) (*Envelope, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("key must be 32 bytes, got %d", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("gcm: %w", err)
	}
	return &Envelope{gcm: gcm}, nil
}

// NewEnvelopeFromHex creates an Envelope from a hex-encoded 32-byte key.
func NewEnvelopeFromHex(hexKey string) (*Envelope, error) {
	key, err := hex.DecodeString(hexKey)
	if err != nil {
		return nil, fmt.Errorf("decode hex key: %w", err)
	}
	return NewEnvelope(key)
}

// Encrypt encrypts plaintext and returns nonce+ciphertext.
// A nil receiver is a no-op passthrough.
func (e *Envelope) Encrypt(plaintext []byte) ([]byte, error) {
	if e == nil {
		return plaintext, nil
	}
	nonce := make([]byte, e.gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return e.gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt decrypts nonce+ciphertext produced by Encrypt.
// A nil receiver is a no-op passthrough.
func (e *Envelope) Decrypt(ciphertext []byte) ([]byte, error) {
	if e == nil {
		return ciphertext, nil
	}
	nonceSize := e.gcm.NonceSize()
	if len(ciphertext) < nonceSize+e.gcm.Overhead() {
		return nil, fmt.Errorf("ciphertext too short")
	}
	nonce, ct := ciphertext[:nonceSize], ciphertext[nonceSize:]
	return e.gcm.Open(nil, nonce, ct, nil)
}
