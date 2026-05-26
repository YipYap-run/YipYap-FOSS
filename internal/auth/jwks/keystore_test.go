package jwks

import (
	"context"
	"crypto/rand"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

func newEncryptedTestStore(t *testing.T) (KeyStore, *crypto.Envelope) {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		t.Fatalf("rand: %v", err)
	}
	env, err := crypto.NewEnvelope(raw[:])
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	ks, err := NewDBKeyStore(s.OIDCSigningKeys(), env)
	if err != nil {
		t.Fatalf("NewDBKeyStore: %v", err)
	}
	return ks, env
}

func TestDBKeyStore_EncryptsPrivateKey(t *testing.T) {
	ks, _ := newEncryptedTestStore(t)

	// Hand-roll a manager wrapping this keystore.
	m := NewManager(ks)
	k, err := m.Generate(EdDSA)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	now := time.Now().UTC()
	k.Status = StatusActive
	k.ActivatedAt = &now
	if err := ks.Save(context.Background(), k); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// The ciphertext must not equal the plaintext PKCS8 bytes.
	round, err := ks.Get(context.Background(), k.KID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if round == nil {
		t.Fatalf("nil roundtrip")
	}
	if round.Algorithm != EdDSA {
		t.Fatalf("alg: %v", round.Algorithm)
	}
	if round.KID != k.KID {
		t.Fatalf("kid mismatch")
	}
	if round.Private == nil {
		t.Fatalf("private not decrypted")
	}
}

func TestDBKeyStore_RequiresEnvelope(t *testing.T) {
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if _, err := NewDBKeyStore(s.OIDCSigningKeys(), nil); err == nil {
		t.Fatalf("expected error for nil envelope")
	}
	if _, err := NewDBKeyStore(nil, nil); err == nil {
		t.Fatalf("expected error for nil store")
	}
}
