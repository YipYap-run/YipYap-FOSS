package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func mustSeedKey(t *testing.T, s *SQLiteStore, kid, status string, created time.Time) *store.OIDCSigningKey {
	t.Helper()
	k := &store.OIDCSigningKey{
		Kid:              kid,
		Algorithm:        "EdDSA",
		EncryptedPrivate: []byte("ciphertext-" + kid),
		PublicJWK:        `{"kty":"OKP","crv":"Ed25519","x":"dummy"}`,
		Status:           status,
		CreatedAt:        created,
	}
	if status == "active" {
		t2 := created
		k.ActivatedAt = &t2
	}
	if err := s.OIDCSigningKeys().Create(context.Background(), k); err != nil {
		t.Fatalf("seed %s: %v", kid, err)
	}
	return k
}

func TestOIDCSigningKeys_Create_Get(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	seed := mustSeedKey(t, s, "kid-A", "active", now)

	got, err := s.OIDCSigningKeys().GetByKID(ctx, "kid-A")
	if err != nil {
		t.Fatalf("GetByKID: %v", err)
	}
	if got == nil {
		t.Fatalf("expected key, got nil")
	}
	if got.Kid != seed.Kid || got.Algorithm != seed.Algorithm || got.Status != "active" {
		t.Fatalf("unexpected row: %+v", got)
	}
	if string(got.EncryptedPrivate) != "ciphertext-kid-A" {
		t.Fatalf("encrypted private mismatch: %q", string(got.EncryptedPrivate))
	}
	if got.ActivatedAt == nil {
		t.Fatalf("activated_at should be set for active key")
	}
}

func TestOIDCSigningKeys_GetByKID_NotFound(t *testing.T) {
	s := setupTestDB(t)
	got, err := s.OIDCSigningKeys().GetByKID(context.Background(), "nope")
	if err != nil {
		t.Fatalf("GetByKID: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing kid, got %+v", got)
	}
}

func TestOIDCSigningKeys_ListByStatus(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	mustSeedKey(t, s, "k1", "active", now.Add(-1*time.Hour))
	mustSeedKey(t, s, "k2", "retiring", now.Add(-2*time.Hour))
	mustSeedKey(t, s, "k3", "expired", now.Add(-3*time.Hour))

	public, err := s.OIDCSigningKeys().ListByStatus(ctx, "active", "retiring")
	if err != nil {
		t.Fatalf("ListByStatus: %v", err)
	}
	if len(public) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(public))
	}

	all, err := s.OIDCSigningKeys().ListByStatus(ctx)
	if err != nil {
		t.Fatalf("ListByStatus(all): %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(all))
	}
}

func TestOIDCSigningKeys_UpdateStatus(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	mustSeedKey(t, s, "k1", "active", now.Add(-1*time.Hour))

	retired := now
	if err := s.OIDCSigningKeys().UpdateStatus(ctx, "k1", "retiring", &retired); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}
	got, err := s.OIDCSigningKeys().GetByKID(ctx, "k1")
	if err != nil {
		t.Fatalf("GetByKID: %v", err)
	}
	if got.Status != "retiring" {
		t.Fatalf("status: want retiring, got %q", got.Status)
	}
	if got.RetiredAt == nil || !got.RetiredAt.Equal(retired) {
		t.Fatalf("retired_at mismatch: got %v want %v", got.RetiredAt, retired)
	}

	if err := s.OIDCSigningKeys().UpdateStatus(ctx, "nope", "retiring", &retired); err == nil {
		t.Fatalf("expected error for unknown kid")
	}
}

func TestOIDCSigningKeys_DeleteExpiredBefore(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// Expired row retired 40 days ago -> should be deleted with 30d cutoff.
	old := now.Add(-40 * 24 * time.Hour)
	mustSeedKey(t, s, "old", "expired", old)
	if err := s.OIDCSigningKeys().UpdateStatus(ctx, "old", "expired", &old); err != nil {
		t.Fatalf("set retired_at: %v", err)
	}

	// Expired row retired 10 days ago -> should remain.
	recent := now.Add(-10 * 24 * time.Hour)
	mustSeedKey(t, s, "recent", "expired", recent)
	if err := s.OIDCSigningKeys().UpdateStatus(ctx, "recent", "expired", &recent); err != nil {
		t.Fatalf("set retired_at: %v", err)
	}

	// Active row -> never deleted.
	mustSeedKey(t, s, "active", "active", now)

	cutoff := now.Add(-30 * 24 * time.Hour)
	n, err := s.OIDCSigningKeys().DeleteExpiredBefore(ctx, cutoff)
	if err != nil {
		t.Fatalf("DeleteExpiredBefore: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 deletion, got %d", n)
	}
	if got, _ := s.OIDCSigningKeys().GetByKID(ctx, "old"); got != nil {
		t.Fatalf("expected 'old' row deleted")
	}
	if got, _ := s.OIDCSigningKeys().GetByKID(ctx, "recent"); got == nil {
		t.Fatalf("expected 'recent' row to remain")
	}
	if got, _ := s.OIDCSigningKeys().GetByKID(ctx, "active"); got == nil {
		t.Fatalf("expected 'active' row to remain")
	}
}
