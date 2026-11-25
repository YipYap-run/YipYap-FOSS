package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestAPIKeyCreateAndGetByHash(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	key := &domain.APIKey{
		ID:        uuid.New().String(),
		OrgID:     org.ID,
		Name:      "CI Key",
		KeyHash:   "sha256-abc123",
		Prefix:    "yy_",
		Scopes:    []string{"monitors:read", "checks:write"},
		CreatedBy: "user-1",
		CreatedAt: now,
	}
	if err := s.APIKeys().Create(ctx, key); err != nil {
		t.Fatal(err)
	}

	got, err := s.APIKeys().GetByHash(ctx, "sha256-abc123")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "CI Key" || len(got.Scopes) != 2 {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestAPIKeyListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	for i := 0; i < 2; i++ {
		key := &domain.APIKey{
			ID:        uuid.New().String(),
			OrgID:     org.ID,
			Name:      "Key",
			KeyHash:   uuid.New().String(),
			Prefix:    "yy_",
			Scopes:    []string{"read"},
			CreatedAt: now,
		}
		if err := s.APIKeys().Create(ctx, key); err != nil {
			t.Fatal(err)
		}
	}

	keys, err := s.APIKeys().ListByOrg(ctx, org.ID, store.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2, got %d", len(keys))
	}
}

func TestAPIKeyUpdateLastUsed(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	key := &domain.APIKey{
		ID:        uuid.New().String(),
		OrgID:     org.ID,
		Name:      "Key",
		KeyHash:   "hash-xyz",
		Prefix:    "yy_",
		Scopes:    []string{"read"},
		CreatedAt: now,
	}
	if err := s.APIKeys().Create(ctx, key); err != nil {
		t.Fatal(err)
	}

	usedAt := now.Add(5 * time.Minute)
	if err := s.APIKeys().UpdateLastUsed(ctx, key.ID, usedAt); err != nil {
		t.Fatal(err)
	}

	got, err := s.APIKeys().GetByHash(ctx, "hash-xyz")
	if err != nil {
		t.Fatal(err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(usedAt) {
		t.Fatalf("last_used_at mismatch: got %v, want %v", got.LastUsedAt, usedAt)
	}
}

func TestAPIKeyListAndDeleteByCreator(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	other := createTestOrg(t, s)

	now := time.Now().UTC().Truncate(time.Second)
	add := func(orgID, creator string) {
		if err := s.APIKeys().Create(ctx, &domain.APIKey{
			ID: uuid.New().String(), OrgID: orgID, Name: "k", KeyHash: uuid.New().String(),
			Prefix: "yy_", Scopes: []string{"read-only"}, CreatedBy: creator, CreatedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	add(org.ID, "u1")
	add(org.ID, "u1")
	add(org.ID, "u2")
	add(other.ID, "u1") // same creator ID in another org must not be touched

	keys, err := s.APIKeys().ListByCreator(ctx, org.ID, "u1")
	if err != nil || len(keys) != 2 {
		t.Fatalf("ListByCreator = %d, %v; want 2", len(keys), err)
	}
	n, err := s.APIKeys().DeleteByCreator(ctx, org.ID, "u1")
	if err != nil || n != 2 {
		t.Fatalf("DeleteByCreator = %d, %v; want 2", n, err)
	}
	if left, _ := s.APIKeys().ListByOrg(ctx, org.ID, store.ListParams{}); len(left) != 1 {
		t.Errorf("org keys left = %d, want 1", len(left))
	}
	if left, _ := s.APIKeys().ListByCreator(ctx, other.ID, "u1"); len(left) != 1 {
		t.Errorf("other org keys left = %d, want 1", len(left))
	}
}
