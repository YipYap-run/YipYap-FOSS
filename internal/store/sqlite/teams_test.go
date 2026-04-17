package sqlite

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestTeamCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	team := createTestTeam(t, s, org.ID)

	got, err := s.Teams().GetByID(ctx, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Test Team" {
		t.Fatalf("unexpected name: %s", got.Name)
	}
}

func TestTeamListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	createTestTeam(t, s, org.ID)
	createTestTeam(t, s, org.ID)

	teams, err := s.Teams().ListByOrg(ctx, org.ID, store.ListParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(teams) != 2 {
		t.Fatalf("expected 2, got %d", len(teams))
	}
}

func TestTeamMembers(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	team := createTestTeam(t, s, org.ID)
	user := createTestUser(t, s, org.ID)

	member := &domain.TeamMember{TeamID: team.ID, UserID: user.ID, Position: 0}
	if err := s.Teams().AddMember(ctx, member); err != nil {
		t.Fatal(err)
	}

	members, err := s.Teams().ListMembers(ctx, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].UserID != user.ID {
		t.Fatalf("unexpected members: %+v", members)
	}

	member.Position = 1
	if err := s.Teams().UpdateMember(ctx, member); err != nil {
		t.Fatal(err)
	}

	if err := s.Teams().RemoveMember(ctx, team.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	members, err = s.Teams().ListMembers(ctx, team.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatal("expected 0 members after remove")
	}
}
