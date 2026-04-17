package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func createTestAlert(t *testing.T, s *SQLiteStore, orgID, monitorID string) *domain.Alert {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	a := &domain.Alert{
		ID:        uuid.New().String(),
		MonitorID: monitorID,
		OrgID:     orgID,
		Status:    domain.AlertFiring,
		Severity:  domain.SeverityCritical,
		StartedAt: now,
	}
	if err := s.Alerts().Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAlertCreateAndGet(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)

	got, err := s.Alerts().GetByID(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.AlertFiring || got.Severity != domain.SeverityCritical {
		t.Fatalf("unexpected: %+v", got)
	}
}

func TestAlertListByOrg(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	createTestAlert(t, s, org.ID, mon.ID)

	alerts, err := s.Alerts().ListByOrg(ctx, org.ID, store.AlertFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 {
		t.Fatalf("expected 1, got %d", len(alerts))
	}
}

func TestAlertGetActiveByMonitor(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)

	got, err := s.Alerts().GetActiveByMonitor(ctx, mon.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != alert.ID {
		t.Fatalf("id mismatch")
	}
}

func TestAlertListFiring(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	createTestAlert(t, s, org.ID, mon.ID)

	firing, err := s.Alerts().ListFiring(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(firing) != 1 {
		t.Fatalf("expected 1 firing, got %d", len(firing))
	}
}

func TestAlertEvents(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)

	now := time.Now().UTC().Truncate(time.Second)
	ev := &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alert.ID,
		EventType: domain.EventTriggered,
		Detail:    json.RawMessage(`{"msg":"test"}`),
		CreatedAt: now,
	}
	if err := s.Alerts().CreateEvent(ctx, ev); err != nil {
		t.Fatal(err)
	}

	events, err := s.Alerts().ListEvents(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].EventType != domain.EventTriggered {
		t.Fatalf("unexpected events: %+v", events)
	}
}

func TestAlertEscalationState(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)

	now := time.Now().UTC().Truncate(time.Second)
	state := &domain.AlertEscalationState{
		AlertID:       alert.ID,
		CurrentStepID: "step-1",
		StepEnteredAt: now,
		RetryCount:    0,
	}
	if err := s.Alerts().UpsertEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}

	got, err := s.Alerts().GetEscalationState(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.CurrentStepID != "step-1" {
		t.Fatalf("unexpected step: %s", got.CurrentStepID)
	}

	// Update via upsert.
	state.RetryCount = 2
	if err := s.Alerts().UpsertEscalationState(ctx, state); err != nil {
		t.Fatal(err)
	}
	got, err = s.Alerts().GetEscalationState(ctx, alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.RetryCount != 2 {
		t.Fatalf("expected retry_count 2, got %d", got.RetryCount)
	}
}
