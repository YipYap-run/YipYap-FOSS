package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	e, err := NewEnvelope(key)
	if err != nil {
		t.Fatal(err)
	}

	plaintext := []byte(`{"bot_token":"xoxb-secret","channel_id":"C123"}`)
	ciphertext, err := e.Encrypt(plaintext)
	if err != nil {
		t.Fatal(err)
	}

	if string(ciphertext) == string(plaintext) {
		t.Fatal("ciphertext should differ from plaintext")
	}

	decrypted, err := e.Decrypt(ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(plaintext) {
		t.Fatalf("got %q, want %q", decrypted, plaintext)
	}
}

func TestBadKey(t *testing.T) {
	_, err := NewEnvelope([]byte("too-short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestTamperedCiphertext(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	e, _ := NewEnvelope(key)
	ct, _ := e.Encrypt([]byte("secret"))
	ct[len(ct)-1] ^= 0xff // flip a bit
	_, err := e.Decrypt(ct)
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestNilEnvelopePassthrough(t *testing.T) {
	var e *Envelope // nil
	data := []byte("plaintext")

	encrypted, err := e.Encrypt(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(encrypted) != string(data) {
		t.Fatal("nil envelope should passthrough encrypt")
	}

	decrypted, err := e.Decrypt(data)
	if err != nil {
		t.Fatal(err)
	}
	if string(decrypted) != string(data) {
		t.Fatal("nil envelope should passthrough decrypt")
	}
}

func TestFromHex(t *testing.T) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	hexKey := hex.EncodeToString(key)
	e, err := NewEnvelopeFromHex(hexKey)
	if err != nil {
		t.Fatal(err)
	}
	ct, _ := e.Encrypt([]byte("test"))
	pt, _ := e.Decrypt(ct)
	if string(pt) != "test" {
		t.Fatal("hex key round-trip failed")
	}
}
