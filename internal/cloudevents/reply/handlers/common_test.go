package handlers

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestWriteTimelineEvent_PersistsJSON(t *testing.T) {
	s := newTestStore(t)
	alert := seedAlert(t, s)

	payload := map[string]any{
		"who":  "alice",
		"note": "on it",
	}
	if err := writeTimelineEvent(context.Background(), s, alert.ID, domain.EventReplyClaimed, payload); err != nil {
		t.Fatalf("writeTimelineEvent: %v", err)
	}

	events, err := s.Alerts().ListEvents(context.Background(), alert.ID)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events=%d, want 1", len(events))
	}
	got := events[0]
	if got.EventType != domain.EventReplyClaimed {
		t.Errorf("EventType=%q, want %q", got.EventType, domain.EventReplyClaimed)
	}
	if got.AlertID != alert.ID {
		t.Errorf("AlertID=%q, want %q", got.AlertID, alert.ID)
	}
	var round map[string]any
	if err := json.Unmarshal(got.Detail, &round); err != nil {
		t.Fatalf("unmarshal detail: %v (raw=%s)", err, string(got.Detail))
	}
	if round["who"] != "alice" || round["note"] != "on it" {
		t.Errorf("detail round-trip mismatch: %+v", round)
	}
}

func TestWriteTimelineEvent_EmptyAlertID(t *testing.T) {
	s := newTestStore(t)
	err := writeTimelineEvent(context.Background(), s, "", domain.EventReplyClaimed, map[string]any{})
	if err == nil {
		t.Fatal("expected error for empty alert id, got nil")
	}
}

func TestWriteTimelineEvent_NilStore(t *testing.T) {
	err := writeTimelineEvent(context.Background(), nil, "a1", domain.EventReplyClaimed, map[string]any{})
	if err == nil {
		t.Fatal("expected error for nil store, got nil")
	}
}
