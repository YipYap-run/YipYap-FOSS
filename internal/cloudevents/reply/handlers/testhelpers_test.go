package handlers

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// newTestStore spins up an in-memory SQLite store with all migrations
// applied and returns it. The store is closed automatically when the test
// ends.
func newTestStore(t *testing.T) *sqlite.SQLiteStore {
	t.Helper()
	s, err := sqlite.New(":memory:")
	if err != nil {
		t.Fatalf("sqlite.New: %v", err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// seedUser creates a user in the given org and returns it.
func seedUser(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.User {
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
		t.Fatalf("Users.Create: %v", err)
	}
	return u
}

// seedTeam creates a team in the given org and returns it.
func seedTeam(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.Team {
	t.Helper()
	team := &domain.Team{
		ID:    uuid.New().String(),
		OrgID: orgID,
		Name:  "Team " + uuid.New().String()[:6],
	}
	if err := s.Teams().Create(context.Background(), team); err != nil {
		t.Fatalf("Teams.Create: %v", err)
	}
	return team
}

// seedChannel creates a notification channel in the given org.
func seedChannel(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.NotificationChannel {
	t.Helper()
	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   orgID,
		Type:    "email",
		Name:    "Ops Mailer",
		Config:  `{"to":"ops@example.com"}`,
		Enabled: true,
	}
	if err := s.NotificationChannels().Create(context.Background(), ch); err != nil {
		t.Fatalf("NotificationChannels.Create: %v", err)
	}
	return ch
}

// seedAlert creates an Org, Monitor, and firing Alert and returns the alert.
// Each call generates unique IDs so tests that need multiple distinct alerts
// can call seedAlert repeatedly without collisions.
func seedAlert(t *testing.T, s *sqlite.SQLiteStore) *domain.Alert {
	t.Helper()
	return seedAlertInOrg(t, s, "")
}

// seedAlertInOrg creates a firing Alert (and monitor) in the given org,
// creating a new org when orgID is empty. Use this to seed multiple alerts
// that share an org (the Alerts().Update path does NOT mutate org_id, so
// "create in org A then reassign to org B" is not a valid pattern).
func seedAlertInOrg(t *testing.T, s *sqlite.SQLiteStore, orgID string) *domain.Alert {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if orgID == "" {
		org := &domain.Org{
			ID:        uuid.New().String(),
			Name:      "Test Org",
			Slug:      "org-" + uuid.New().String()[:8],
			Plan:      domain.PlanFree,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.Orgs().Create(ctx, org); err != nil {
			t.Fatalf("Orgs.Create: %v", err)
		}
		orgID = org.ID
	}

	mon := &domain.Monitor{
		ID:              uuid.New().String(),
		OrgID:           orgID,
		Name:            "Monitor",
		Type:            domain.MonitorHTTP,
		Config:          []byte(`{"url":"https://example.com"}`),
		IntervalSeconds: 60,
		TimeoutSeconds:  10,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.Monitors().Create(ctx, mon); err != nil {
		t.Fatalf("Monitors.Create: %v", err)
	}

	alert := &domain.Alert{
		ID:        uuid.New().String(),
		MonitorID: mon.ID,
		OrgID:     orgID,
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(ctx, alert); err != nil {
		t.Fatalf("Alerts.Create: %v", err)
	}
	return alert
}
