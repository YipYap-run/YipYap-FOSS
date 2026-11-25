package handlers_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

type conflictBody struct {
	Error         string `json:"error"`
	Code          string `json:"code"`
	Status        string `json:"status"`
	AlertID       string `json:"alert_id"`
	MonitorID     string `json:"monitor_id"`
	ActorID       string `json:"actor_id"`
	ActorName     string `json:"actor_name"`
	MonitorStatus string `json:"monitor_status"`
}

func decodeConflict(t *testing.T, w *httptest.ResponseRecorder) conflictBody {
	t.Helper()
	if w.Code != http.StatusConflict {
		t.Fatalf("status %d, want 409; body=%s", w.Code, w.Body.String())
	}
	var b conflictBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode 409 body: %v", err)
	}
	return b
}

func addCheck(t *testing.T, s *sqlite.SQLiteStore, monitorID string, status domain.CheckStatus, at time.Time) {
	t.Helper()
	c := &domain.MonitorCheck{ID: uuid.New().String(), MonitorID: monitorID, Status: status, CheckedAt: at}
	if err := s.Checks().Create(context.Background(), c); err != nil {
		t.Fatal(err)
	}
}

func doAlertAction(t *testing.T, h *handlers.AlertHandler, action string, alert *domain.Alert, userID string) *httptest.ResponseRecorder {
	t.Helper()
	w, _, ctx := requestWithAlertID(t, alert.ID, userID, alert.OrgID)
	r := httptest.NewRequest("POST", "/alerts/"+alert.ID+"/"+action, nil).WithContext(ctx)
	switch action {
	case "ack":
		h.Ack(w, r)
	case "resolve":
		h.Resolve(w, r)
	default:
		t.Fatalf("unknown action %q", action)
	}
	return w
}

func setUserName(t *testing.T, s *sqlite.SQLiteStore, u *domain.User, name string) {
	t.Helper()
	u.Name = name
	if err := s.Users().Update(context.Background(), u); err != nil {
		t.Fatal(err)
	}
}

// mon-10: a stale tab acking an alert that someone already resolved must
// not reopen it.
func TestAlertAck_ResolvedAlertReturns409AndStaysResolved(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	setUserName(t, s, user, "Riley")
	h := handlers.NewAlertHandler(s)

	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}

	b := decodeConflict(t, doAlertAction(t, h, "ack", alert, user.ID))
	if b.Code != "alert_resolved" || b.Status != "resolved" {
		t.Fatalf("code=%q status=%q", b.Code, b.Status)
	}
	if b.ActorID != user.ID || b.ActorName != "Riley" {
		t.Fatalf("actor = %q/%q, want %q/Riley", b.ActorID, b.ActorName, user.ID)
	}
	if b.Error != "This alert was already resolved by Riley." {
		t.Fatalf("error = %q", b.Error)
	}

	got, err := s.Alerts().GetByID(context.Background(), alert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.AlertResolved || got.AcknowledgedAt != nil || got.AcknowledgedBy != "" {
		t.Fatalf("alert changed by rejected ack: status=%s acked_at=%v by=%q", got.Status, got.AcknowledgedAt, got.AcknowledgedBy)
	}
}

func TestAlertAck_AcknowledgedAlertReturns409(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)

	if w := doAlertAction(t, h, "ack", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("first ack: %d %s", w.Code, w.Body.String())
	}
	b := decodeConflict(t, doAlertAction(t, h, "ack", alert, user.ID))
	if b.Code != "alert_acknowledged" || b.ActorID != user.ID {
		t.Fatalf("code=%q actor=%q", b.Code, b.ActorID)
	}
	// Name falls back to email.
	if b.ActorName != user.Email {
		t.Fatalf("actor_name = %q, want %q", b.ActorName, user.Email)
	}
}

// An alert resolved by recovery has a resolved event with no user.
func TestAlertAck_AutoResolvedAlertSaysAutomatic(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)
	ctx := context.Background()
	now := time.Now().UTC()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now
	if err := s.Alerts().Update(ctx, alert); err != nil {
		t.Fatal(err)
	}
	if err := s.Alerts().CreateEvent(ctx, &domain.AlertEvent{ID: uuid.New().String(), AlertID: alert.ID, EventType: domain.EventResolved, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	b := decodeConflict(t, doAlertAction(t, h, "ack", alert, user.ID))
	if b.Error != "This alert was already resolved automatically when the monitor recovered." {
		t.Fatalf("error = %q", b.Error)
	}
}

func TestAlertResolve_AlreadyResolvedReturns409(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)
	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	before, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	b := decodeConflict(t, doAlertAction(t, h, "resolve", alert, user.ID))
	if b.Code != "alert_resolved" {
		t.Fatalf("code = %q", b.Code)
	}
	after, _ := s.Alerts().GetByID(context.Background(), alert.ID)
	if !after.ResolvedAt.Equal(*before.ResolvedAt) {
		t.Fatalf("resolved_at moved: %v -> %v", before.ResolvedAt, after.ResolvedAt)
	}
}

// core-monitoring decision 3: resolve is blocked while the latest check is
// failing, and allowed once the org turns on allow_resolve_while_down.
func TestAlertResolve_BlockedWhileMonitorDown(t *testing.T) {
	for _, st := range []domain.CheckStatus{domain.StatusDown, domain.StatusDegraded} {
		t.Run(string(st), func(t *testing.T) {
			s, alert, user := setupAlertHandlerTest(t)
			h := handlers.NewAlertHandler(s)
			now := time.Now().UTC()
			addCheck(t, s, alert.MonitorID, domain.StatusUp, now.Add(-2*time.Minute))
			addCheck(t, s, alert.MonitorID, st, now.Add(-time.Minute))

			b := decodeConflict(t, doAlertAction(t, h, "resolve", alert, user.ID))
			if b.Code != "monitor_still_down" || b.MonitorStatus != string(st) || b.MonitorID != alert.MonitorID {
				t.Fatalf("body = %+v", b)
			}
			if !strings.Contains(b.Error, "Acknowledge it to stop paging") {
				t.Fatalf("firing alert should be pointed to Acknowledge: %q", b.Error)
			}
			got, _ := s.Alerts().GetByID(context.Background(), alert.ID)
			if got.Status != domain.AlertFiring {
				t.Fatalf("status = %s, want firing", got.Status)
			}

			// Acknowledge is still allowed and is the suggested path.
			if w := doAlertAction(t, h, "ack", alert, user.ID); w.Code != http.StatusOK {
				t.Fatalf("ack while down: %d %s", w.Code, w.Body.String())
			}
			// An acknowledged alert is blocked the same way.
			b = decodeConflict(t, doAlertAction(t, h, "resolve", alert, user.ID))
			if b.Code != "monitor_still_down" || b.Status != "acknowledged" {
				t.Fatalf("body = %+v", b)
			}
			if strings.Contains(b.Error, "Acknowledge it") || !strings.Contains(b.Error, "Paging already stopped") {
				t.Fatalf("acknowledged alert should not be told to acknowledge again: %q", b.Error)
			}

			// Org opt-in lets it through.
			if err := s.OrgSettings().Set(context.Background(), alert.OrgID, handlers.OrgSettingAllowResolveWhileDown, "true"); err != nil {
				t.Fatal(err)
			}
			if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
				t.Fatalf("resolve with toggle on: %d %s", w.Code, w.Body.String())
			}
			got, _ = s.Alerts().GetByID(context.Background(), alert.ID)
			if got.Status != domain.AlertResolved {
				t.Fatalf("status = %s, want resolved", got.Status)
			}
		})
	}
}

func TestAlertResolve_AllowedOnceMonitorRecovers(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)
	now := time.Now().UTC()
	addCheck(t, s, alert.MonitorID, domain.StatusDown, now.Add(-2*time.Minute))
	addCheck(t, s, alert.MonitorID, domain.StatusUp, now.Add(-time.Minute))
	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
}

// The setting is scoped to its org: another org opting in does not unblock
// this one.
func TestAlertResolve_ToggleIsPerOrg(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)
	addCheck(t, s, alert.MonitorID, domain.StatusDown, time.Now().UTC())
	now := time.Now().UTC()
	other := &domain.Org{ID: uuid.New().String(), Name: "Other", Slug: "x-" + uuid.New().String()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(context.Background(), other); err != nil {
		t.Fatal(err)
	}
	if err := s.OrgSettings().Set(context.Background(), other.ID, handlers.OrgSettingAllowResolveWhileDown, "true"); err != nil {
		t.Fatal(err)
	}
	decodeConflict(t, doAlertAction(t, h, "resolve", alert, user.ID))
}

// Two acks racing on the same firing alert: the guarded update lets exactly
// one through, the store reports the loser as a state change.
func TestAlertStore_UpdateIfStatusGuards(t *testing.T) {
	s, alert, _ := setupAlertHandlerTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	resolved := *alert
	resolved.Status = domain.AlertResolved
	resolved.ResolvedAt = &now
	if err := s.Alerts().UpdateIfStatus(ctx, &resolved, domain.AlertFiring, domain.AlertAcknowledged); err != nil {
		t.Fatalf("first transition: %v", err)
	}

	acked := *alert
	acked.Status = domain.AlertAcknowledged
	acked.AcknowledgedAt = &now
	err := s.Alerts().UpdateIfStatus(ctx, &acked, domain.AlertFiring)
	if !errors.Is(err, store.ErrAlertStateChanged) {
		t.Fatalf("stale ack err = %v, want ErrAlertStateChanged", err)
	}
	got, _ := s.Alerts().GetByID(ctx, alert.ID)
	if got.Status != domain.AlertResolved {
		t.Fatalf("status = %s, want resolved", got.Status)
	}

	missing := *alert
	missing.ID = uuid.New().String()
	if err := s.Alerts().UpdateIfStatus(ctx, &missing, domain.AlertFiring); err == nil {
		t.Fatal("missing alert: want error")
	}
}
