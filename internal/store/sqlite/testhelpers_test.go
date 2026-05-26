package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func setupTestDB(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := New(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func createTestOrg(t *testing.T, s *SQLiteStore) *domain.Org {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	org := &domain.Org{
		ID:        uuid.New().String(),
		Name:      "Test Org",
		Slug:      "test-org-" + uuid.New().String()[:8],
		Plan:      domain.PlanFree,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.Orgs().Create(context.Background(), org); err != nil {
		t.Fatal(err)
	}
	return org
}

func createTestUser(t *testing.T, s *SQLiteStore, orgID string) *domain.User {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	u := &domain.User{
		ID:           uuid.New().String(),
		OrgID:        orgID,
		Email:        uuid.New().String()[:8] + "@test.com",
		PasswordHash: "hashed",
		Role:         domain.RoleMember,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := s.Users().Create(context.Background(), u); err != nil {
		t.Fatal(err)
	}
	return u
}

func createTestEscalationPolicy(t *testing.T, s *SQLiteStore, orgID string) *domain.EscalationPolicy {
	t.Helper()
	p := &domain.EscalationPolicy{
		ID:    uuid.New().String(),
		OrgID: orgID,
		Name:  "Test Policy",
		Loop:  false,
	}
	if err := s.EscalationPolicies().Create(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	return p
}

func createTestMonitor(t *testing.T, s *SQLiteStore, orgID string) *domain.Monitor {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	m := &domain.Monitor{
		ID:              uuid.New().String(),
		OrgID:           orgID,
		Name:            "Test Monitor",
		Type:            domain.MonitorHTTP,
		Config:          json.RawMessage(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Regions:         []string{"us-east-1"},
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	return m
}

func createTestTeam(t *testing.T, s *SQLiteStore, orgID string) *domain.Team {
	t.Helper()
	team := &domain.Team{
		ID:    uuid.New().String(),
		OrgID: orgID,
		Name:  "Test Team",
	}
	if err := s.Teams().Create(context.Background(), team); err != nil {
		t.Fatal(err)
	}
	return team
}
