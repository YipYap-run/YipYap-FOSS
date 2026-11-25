package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlite"
)

// addAlert creates another alert on the fixture's monitor with the given
// status. Only one alert per monitor may be active, so active ones get a
// monitor of their own.
func addAlert(t *testing.T, s *sqlite.SQLiteStore, base *domain.Alert, status domain.AlertStatus, startedAt time.Time) *domain.Alert {
	t.Helper()
	ctx := context.Background()
	monitorID := base.MonitorID
	if status != domain.AlertResolved {
		m := &domain.Monitor{ID: uuid.New().String(), OrgID: base.OrgID, Name: "M-" + string(status), Type: domain.MonitorHTTP,
			Config: []byte(`{"url":"https://example.com"}`), IntervalSeconds: 60, TimeoutSeconds: 10, Enabled: true,
			CreatedAt: startedAt, UpdatedAt: startedAt}
		if err := s.Monitors().Create(ctx, m); err != nil {
			t.Fatal(err)
		}
		monitorID = m.ID
	}
	a := &domain.Alert{ID: uuid.New().String(), MonitorID: monitorID, OrgID: base.OrgID, Status: status,
		Severity: domain.SeverityCritical, StartedAt: startedAt}
	if status == domain.AlertResolved {
		a.ResolvedAt = &startedAt
	}
	if err := s.Alerts().Create(ctx, a); err != nil {
		t.Fatal(err)
	}
	return a
}

func listAlerts(t *testing.T, h *handlers.AlertHandler, orgID, userID, rawQuery string) (*httptest.ResponseRecorder, []string) {
	t.Helper()
	ctx := middleware.SetClaims(context.Background(), &auth.Claims{UserID: userID, OrgID: orgID, Role: "member"})
	r := httptest.NewRequest("GET", "/alerts?"+rawQuery, nil).WithContext(ctx)
	w := httptest.NewRecorder()
	h.List(w, r)
	if w.Code != http.StatusOK {
		return w, nil
	}
	var got []struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	statuses := make([]string, len(got))
	for i, a := range got {
		statuses[i] = a.Status
	}
	sort.Strings(statuses)
	return w, statuses
}

// ux-5: the dashboard asks for firing and acknowledged alerts together.
// List used q.Get("status") and dropped every value after the first.
func TestAlertList_MultipleStatuses(t *testing.T) {
	s, firing, user := setupAlertHandlerTest(t)
	now := time.Now().UTC()
	addAlert(t, s, firing, domain.AlertAcknowledged, now.Add(-time.Minute))
	addAlert(t, s, firing, domain.AlertResolved, now.Add(-2*time.Minute))
	h := handlers.NewAlertHandler(s)

	cases := map[string][]string{
		"status=firing&status=acknowledged":           {"acknowledged", "firing"},
		"status=firing,acknowledged":                  {"acknowledged", "firing"},
		"status=firing,%20acknowledged&status=firing": {"acknowledged", "firing"},
		"status=resolved":                             {"resolved"},
		"":                                            {"acknowledged", "firing", "resolved"},
	}
	for q, want := range cases {
		w, got := listAlerts(t, h, firing.OrgID, user.ID, q)
		if w.Code != http.StatusOK {
			t.Fatalf("%q: status %d body=%s", q, w.Code, w.Body.String())
		}
		if len(got) != len(want) {
			t.Errorf("%q: got %v, want %v", q, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%q: got %v, want %v", q, got, want)
				break
			}
		}
	}
}

func TestAlertList_UnknownStatusIs400(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	h := handlers.NewAlertHandler(s)
	w, _ := listAlerts(t, h, alert.OrgID, user.ID, "status=firing,fring")
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d, want 400; body=%s", w.Code, w.Body.String())
	}
}

type alertEscalationBody struct {
	EscalationPolicyID   string     `json:"escalation_policy_id"`
	EscalationPolicyName string     `json:"escalation_policy_name"`
	EscalationState      string     `json:"escalation_state"`
	CurrentStep          int        `json:"current_step"`
	CurrentStepPosition  int        `json:"current_step_position"`
	StepCount            int        `json:"step_count"`
	NextEscalationAction string     `json:"next_escalation_action"`
	NextEscalationAt     *time.Time `json:"next_escalation_at"`
	StartedAt            time.Time  `json:"started_at"`
}

func getAlert(t *testing.T, h *handlers.AlertHandler, alert *domain.Alert, userID string) alertEscalationBody {
	t.Helper()
	w, _, ctx := requestWithAlertID(t, alert.ID, userID, alert.OrgID)
	h.Get(w, httptest.NewRequest("GET", "/alerts/"+alert.ID, nil).WithContext(ctx))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}
	var b alertEscalationBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return b
}

// ux-4: the alert detail page reads escalation_policy_id, current_step and
// next_escalation_at. Get returned none of them, so the card never showed.
func TestAlertGet_EscalationStatus(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	ctx := context.Background()
	h := handlers.NewAlertHandler(s)

	// No policy on the monitor: no escalation fields.
	if b := getAlert(t, h, alert, user.ID); b.EscalationPolicyID != "" || b.EscalationState != "" {
		t.Fatalf("monitor without a policy returned escalation %+v", b)
	}

	policy := &domain.EscalationPolicy{ID: uuid.New().String(), OrgID: alert.OrgID, Name: "Primary"}
	if err := s.EscalationPolicies().Create(ctx, policy); err != nil {
		t.Fatal(err)
	}
	step1, step2 := uuid.New().String(), uuid.New().String()
	tgt := func(stepID string) []domain.StepTarget {
		return []domain.StepTarget{{ID: uuid.New().String(), StepID: stepID, TargetType: domain.TargetUser, TargetID: user.ID}}
	}
	if err := s.EscalationPolicies().ReplaceSteps(ctx, policy.ID, []domain.EscalationStep{
		{ID: step1, PolicyID: policy.ID, Position: 0, WaitSeconds: 300},
		{ID: step2, PolicyID: policy.ID, Position: 1, WaitSeconds: 600},
	}, map[string][]domain.StepTarget{step1: tgt(step1), step2: tgt(step2)}); err != nil {
		t.Fatal(err)
	}
	mon, err := s.Monitors().GetByID(ctx, alert.MonitorID)
	if err != nil {
		t.Fatal(err)
	}
	mon.EscalationPolicyID = policy.ID
	if err := s.Monitors().Update(ctx, mon); err != nil {
		t.Fatal(err)
	}

	// Policy set but no state yet (attached after the alert opened).
	b := getAlert(t, h, alert, user.ID)
	if b.EscalationPolicyID != policy.ID || b.EscalationState != "none" || b.StepCount != 2 || b.NextEscalationAt != nil {
		t.Fatalf("policy without state: %+v", b)
	}

	// On step 1, paged at entered: next action is step 2 after the wait.
	entered := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	meta, _ := json.Marshal(map[string]any{"step_position": 0, "group": 1, "paged_at": entered})
	if err := s.Alerts().UpsertEscalationState(ctx, &domain.AlertEscalationState{
		AlertID: alert.ID, CurrentStepID: step1, StepEnteredAt: entered, NotificationsSent: meta,
	}); err != nil {
		t.Fatal(err)
	}
	b = getAlert(t, h, alert, user.ID)
	if b.EscalationPolicyName != "Primary" || b.EscalationState != "active" || b.CurrentStep != 1 || b.CurrentStepPosition != 0 {
		t.Fatalf("on step 1: %+v", b)
	}
	if b.NextEscalationAction != "next_step" || b.NextEscalationAt == nil || !b.NextEscalationAt.Equal(entered.Add(5*time.Minute)) {
		t.Fatalf("next escalation: action %q at %v, want next_step at %v", b.NextEscalationAction, b.NextEscalationAt, entered.Add(5*time.Minute))
	}

	// On the last step of a non-looping policy: nothing further.
	meta, _ = json.Marshal(map[string]any{"step_position": 1, "group": 1, "paged_at": entered})
	if err := s.Alerts().UpsertEscalationState(ctx, &domain.AlertEscalationState{
		AlertID: alert.ID, CurrentStepID: step2, StepEnteredAt: entered, NotificationsSent: meta,
	}); err != nil {
		t.Fatal(err)
	}
	b = getAlert(t, h, alert, user.ID)
	if b.CurrentStep != 2 || b.EscalationState != "exhausted" || b.NextEscalationAt != nil {
		t.Fatalf("last step: %+v", b)
	}

	// Acknowledged: escalation is over.
	alert.Status = domain.AlertAcknowledged
	if err := s.Alerts().Update(ctx, alert); err != nil {
		t.Fatal(err)
	}
	if b = getAlert(t, h, alert, user.ID); b.EscalationState != "acknowledged" || b.NextEscalationAt != nil {
		t.Fatalf("acknowledged: %+v", b)
	}
}

// recordingBus captures broadcast publishes.
type recordingBus struct {
	bus.Bus
	mu   sync.Mutex
	msgs map[string][][]byte
}

func (b *recordingBus) Publish(_ context.Context, subject string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.msgs == nil {
		b.msgs = map[string][][]byte{}
	}
	b.msgs[subject] = append(b.msgs[subject], data)
	return nil
}

// ux-5: an ack or resolve from the API tells other open dashboards to
// refresh through the alert.changed hint.
func TestAlertAckResolve_PublishAlertChanged(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	rb := &recordingBus{}
	h := handlers.NewAlertHandler(s)
	h.SetBus(rb)

	if w := doAlertAction(t, h, "ack", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("ack: %d %s", w.Code, w.Body.String())
	}
	if w := doAlertAction(t, h, "resolve", alert, user.ID); w.Code != http.StatusOK {
		t.Fatalf("resolve: %d %s", w.Code, w.Body.String())
	}
	got := rb.msgs[domain.SubjectAlertChanged]
	if len(got) != 2 {
		t.Fatalf("published %d alert.changed hints, want 2", len(got))
	}
	var last domain.AlertChanged
	if err := json.Unmarshal(got[1], &last); err != nil {
		t.Fatal(err)
	}
	if last.OrgID != alert.OrgID || last.AlertID != alert.ID || last.Status != domain.AlertResolved {
		t.Fatalf("hint %+v", last)
	}
}

// ux-10: the alerts page and dashboard label their rollups "last 200
// alerts" and ask for limit=200. The shared pagination helper reset any
// limit above 100 to 50, so they were really computed from 50.
func TestAlertList_LimitUpTo200(t *testing.T) {
	s, firing, user := setupAlertHandlerTest(t)
	base := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 210; i++ {
		addAlert(t, s, firing, domain.AlertResolved, base.Add(-time.Duration(i)*time.Second))
	}
	h := handlers.NewAlertHandler(s)
	cases := map[string]int{
		"limit=200":                 200,
		"limit=150":                 150,
		"limit=500":                 200,
		"limit=100":                 100,
		"":                          50,
		"status=resolved&limit=200": 200,
	}
	for q, want := range cases {
		w, got := listAlerts(t, h, firing.OrgID, user.ID, q)
		if w.Code != http.StatusOK {
			t.Fatalf("%q: status %d body=%s", q, w.Code, w.Body.String())
		}
		if len(got) != want {
			t.Errorf("%q: got %d alerts, want %d", q, len(got), want)
		}
	}
}
