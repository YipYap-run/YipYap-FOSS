package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestChannelDeliveryHealth(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "slack", Name: "ops", Config: "{}", Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	got, err := s.NotificationChannels().GetByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h := got.Health(); h.Status != domain.ChannelHealthUnknown || h.ConsecutiveFailures != 0 {
		t.Fatalf("new channel health = %+v, want unknown", h)
	}

	t1 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 2; i++ {
		n, err := s.NotificationChannels().RecordDeliveryFailure(ctx, ch.ID, t1, "Delivery failed: the destination returned HTTP 404.")
		if err != nil {
			t.Fatal(err)
		}
		if n != i {
			t.Fatalf("failure %d returned count %d", i, n)
		}
	}
	list, err := s.NotificationChannels().ListByOrg(ctx, org.ID, store.ListParams{})
	if err != nil {
		t.Fatal(err)
	}
	h := list[0].Health()
	if h.Status != domain.ChannelHealthFailing || h.ConsecutiveFailures != 2 ||
		!strings.Contains(h.LastError, "HTTP 404") || h.LastFailureAt == nil || !h.LastFailureAt.Equal(t1) {
		t.Fatalf("health after failures = %+v", h)
	}

	t2 := t1.Add(time.Minute)
	if _, err := s.NotificationChannels().RecordDeliverySuccess(ctx, ch.ID, t2); err != nil {
		t.Fatal(err)
	}
	got, err = s.NotificationChannels().GetByID(ctx, ch.ID)
	if err != nil {
		t.Fatal(err)
	}
	h = got.Health()
	if h.Status != domain.ChannelHealthOK || h.ConsecutiveFailures != 0 || h.LastSuccessAt == nil || !h.LastSuccessAt.Equal(t2) {
		t.Fatalf("health after success = %+v", h)
	}
	if h.LastError != "" {
		t.Fatalf("ok health should not surface the old error, got %q", h.LastError)
	}

	// Updating the channel config does not clear the health columns.
	got.Name = "ops-renamed"
	if err := s.NotificationChannels().Update(ctx, got); err != nil {
		t.Fatal(err)
	}
	again, _ := s.NotificationChannels().GetByID(ctx, ch.ID)
	if again.LastSuccessAt == nil {
		t.Fatal("update cleared last_success_at")
	}
}

func TestChannelDeliveryFailureCapsReason(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: org.ID, Type: "webhook", Name: "w", Config: "{}", Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("é", domain.MaxChannelErrorLen)
	if _, err := s.NotificationChannels().RecordDeliveryFailure(ctx, ch.ID, time.Now(), long); err != nil {
		t.Fatal(err)
	}
	got, _ := s.NotificationChannels().GetByID(ctx, ch.ID)
	if len(got.LastError) > domain.MaxChannelErrorLen {
		t.Fatalf("last_error is %d bytes, cap is %d", len(got.LastError), domain.MaxChannelErrorLen)
	}
	if !strings.HasPrefix(long, got.LastError) || strings.ContainsRune(got.LastError, '�') {
		t.Fatal("cap split a UTF-8 sequence")
	}

	// A channel deleted since the job was queued is not an error.
	n, err := s.NotificationChannels().RecordDeliveryFailure(ctx, "missing", time.Now(), "x")
	if err != nil || n != 0 {
		t.Fatalf("missing channel: n=%d err=%v", n, err)
	}
}

func TestAlertCreateEventOnce(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()
	org := createTestOrg(t, s)
	mon := createTestMonitor(t, s, org.ID)
	alert := createTestAlert(t, s, org.ID, mon.ID)

	evt := &domain.AlertEvent{ID: uuid.New().String(), AlertID: alert.ID, EventType: domain.EventNotificationFailed, CreatedAt: time.Now().UTC()}
	first, err := s.Alerts().CreateEventOnce(ctx, evt)
	if err != nil || !first {
		t.Fatalf("first insert: inserted=%v err=%v", first, err)
	}
	again, err := s.Alerts().CreateEventOnce(ctx, evt)
	if err != nil || again {
		t.Fatalf("second insert: inserted=%v err=%v", again, err)
	}
	events, _ := s.Alerts().ListEvents(ctx, alert.ID)
	if len(events) != 1 {
		t.Fatalf("got %d events, want 1", len(events))
	}
}
