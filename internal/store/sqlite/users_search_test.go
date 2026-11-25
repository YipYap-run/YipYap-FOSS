package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

func createNamedUser(t *testing.T, s *SQLiteStore, orgID, email, name string, role domain.UserRole) *domain.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u := &domain.User{
		ID: uuid.New().String(), OrgID: orgID, Email: email, Name: name,
		PasswordHash: "hashed", Role: role, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.Users().Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

// TestUserSearchByOrg covers the paged member list (ux-3): the total counts
// every match, not just the page, and the search, role and ID filters stay
// inside the org.
func TestUserSearchByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	other := createTestOrg(t, s)

	for i := 0; i < 55; i++ {
		createTestUser(t, s, org.ID)
	}
	alice := createNamedUser(t, s, org.ID, "alice@corp.test", "Alice Admin", domain.RoleAdmin)
	bob := createNamedUser(t, s, org.ID, "bob@corp.test", "Bob 100%", domain.RoleMember)
	gone := createNamedUser(t, s, org.ID, "alice.gone@corp.test", "", domain.RoleAdmin)
	if err := s.Users().Disable(ctx, gone.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	outsider := createNamedUser(t, s, other.ID, "alice@other.test", "Alice Elsewhere", domain.RoleAdmin)

	page, total, err := s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{ListParams: store.ListParams{Limit: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 57 || len(page) != 50 {
		t.Fatalf("first page: total=%d len=%d, want 57 and 50", total, len(page))
	}
	rest, total, err := s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{ListParams: store.ListParams{Limit: 50, Offset: 50}})
	if err != nil {
		t.Fatal(err)
	}
	if total != 57 || len(rest) != 7 {
		t.Fatalf("second page: total=%d len=%d, want 57 and 7", total, len(rest))
	}
	seen := map[string]bool{}
	for _, u := range append(page, rest...) {
		if seen[u.ID] {
			t.Fatalf("user %s appears on both pages", u.Email)
		}
		seen[u.ID] = true
	}

	got, total, err := s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{Query: "ALICE"})
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(got) != 1 || got[0].ID != alice.ID {
		t.Fatalf("q=ALICE: got %d (total %d), want only alice", len(got), total)
	}

	got, _, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{Query: "bob 100"})
	if err != nil || len(got) != 1 || got[0].ID != bob.ID {
		t.Fatalf("name search: got %v, err %v", got, err)
	}
	// A literal % must not act as a wildcard.
	got, _, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{Query: "%"})
	if err != nil || len(got) != 1 || got[0].ID != bob.ID {
		t.Fatalf("literal %% search: got %d users, err %v", len(got), err)
	}
	got, _, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{Query: "_"})
	if err != nil || len(got) != 0 {
		t.Fatalf("literal _ search: got %d users, err %v", len(got), err)
	}

	got, total, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{Role: "admin"})
	if err != nil || total != 1 || got[0].ID != alice.ID {
		t.Fatalf("role=admin: got %d (total %d), err %v", len(got), total, err)
	}

	got, total, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{IDs: []string{alice.ID, bob.ID, outsider.ID, gone.ID}})
	if err != nil || total != 2 || len(got) != 2 {
		t.Fatalf("ids: got %d (total %d), err %v; want alice and bob only", len(got), total, err)
	}
	got, total, err = s.Users().SearchByOrg(ctx, org.ID, store.UserFilter{IDs: []string{}})
	if err != nil || total != 0 || len(got) != 0 {
		t.Fatalf("empty ids: got %d (total %d), err %v", len(got), total, err)
	}
}
