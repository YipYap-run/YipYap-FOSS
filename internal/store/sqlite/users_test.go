package sqlite

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestUserCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	user := createTestUser(t, s, org.ID)

	got, err := s.Users().GetByID(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != user.Email {
		t.Fatalf("email mismatch: got %s, want %s", got.Email, user.Email)
	}
}

func TestUserGetByEmail(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	user := createTestUser(t, s, org.ID)

	got, err := s.Users().GetByEmail(ctx, user.Email)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != user.ID {
		t.Fatalf("id mismatch")
	}
}

func TestUserListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	createTestUser(t, s, org.ID)
	createTestUser(t, s, org.ID)

	users, err := s.Users().ListByOrg(ctx, org.ID, store.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}
}

func TestUserDelete(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	user := createTestUser(t, s, org.ID)

	if err := s.Users().Delete(ctx, user.ID); err != nil {
		t.Fatal(err)
	}

	_, err := s.Users().GetByID(ctx, user.ID)
	if err == nil {
		t.Fatal("expected error after delete")
	}
}
