package sqlite

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func TestCloudEventReplyAudit_Write_RoundTrip(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	entry := &store.CloudEventReplyAuditEntry{
		ReplyID:     "evt-1",
		ReplyType:   "run.yipyap.reply.alert.suppressed.v1",
		ReplySource: "https://consumer.example.com/",
		ReplySub:    "alice@example.com",
		AlertID:     "alert-123",
		OrgID:       "org-1",
		ChannelID:   "ch-a",
		Outcome:     "accepted",
		Before:      json.RawMessage(`{"status":"firing"}`),
		After:       json.RawMessage(`{"status":"firing","suppressed":true}`),
	}
	if err := s.CloudEventReplyAudit().Write(ctx, entry); err != nil {
		t.Fatalf("Write: %v", err)
	}

	got, err := s.CloudEventReplyAudit().ListByAlert(ctx, "alert-123", 10)
	if err != nil {
		t.Fatalf("ListByAlert: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("ListByAlert: len=%d, want 1", len(got))
	}
	if got[0].ReplyID != entry.ReplyID || got[0].Outcome != entry.Outcome {
		t.Errorf("row mismatch: %+v", got[0])
	}
	if string(got[0].Before) != `{"status":"firing"}` {
		t.Errorf("Before=%q", string(got[0].Before))
	}
	if got[0].ID == 0 {
		t.Error("ID should be assigned by autoincrement")
	}
	if got[0].ReceivedAt.IsZero() {
		t.Error("ReceivedAt should be set by default")
	}
}

func TestCloudEventReplyAudit_ListByChannel(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	for i, ch := range []string{"ch-a", "ch-b", "ch-a"} {
		e := &store.CloudEventReplyAuditEntry{
			ReplyID:   uniqueID(i),
			ReplyType: "t",
			OrgID:     "org-1",
			ChannelID: ch,
			Outcome:   "accepted",
		}
		if err := s.CloudEventReplyAudit().Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}

	got, err := s.CloudEventReplyAudit().ListByChannel(ctx, "ch-a", 10)
	if err != nil {
		t.Fatalf("ListByChannel: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len=%d, want 2", len(got))
	}
}

func TestCloudEventReplyAudit_ListByOrg_SinceFilter(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-2 * time.Hour)
	e := &store.CloudEventReplyAuditEntry{
		ReplyID:    "old",
		ReplyType:  "t",
		OrgID:      "org-1",
		Outcome:    "accepted",
		ReceivedAt: old,
	}
	if err := s.CloudEventReplyAudit().Write(ctx, e); err != nil {
		t.Fatalf("Write old: %v", err)
	}
	fresh := &store.CloudEventReplyAuditEntry{
		ReplyID:   "fresh",
		ReplyType: "t",
		OrgID:     "org-1",
		Outcome:   "accepted",
	}
	if err := s.CloudEventReplyAudit().Write(ctx, fresh); err != nil {
		t.Fatalf("Write fresh: %v", err)
	}

	// since = 1h ago: should only return "fresh"
	got, err := s.CloudEventReplyAudit().ListByOrg(ctx, "org-1", time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d, want 1", len(got))
	}
	if got[0].ReplyID != "fresh" {
		t.Errorf("got ReplyID=%q, want fresh", got[0].ReplyID)
	}
}

func TestCloudEventReplyAudit_PruneBefore(t *testing.T) {
	s := setupTestDB(t)
	ctx := context.Background()

	old := time.Now().UTC().Add(-2 * time.Hour)
	for i, t0 := range []time.Time{old, old, {}} {
		e := &store.CloudEventReplyAuditEntry{
			ReplyID:    uniqueID(i),
			ReplyType:  "t",
			OrgID:      "org-1",
			Outcome:    "accepted",
			ReceivedAt: t0,
		}
		if err := s.CloudEventReplyAudit().Write(ctx, e); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	n, err := s.CloudEventReplyAudit().PruneBefore(ctx, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("PruneBefore: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned=%d, want 2", n)
	}
	// One fresh row should remain.
	got, err := s.CloudEventReplyAudit().ListByOrg(ctx, "org-1", time.Now().UTC().Add(-time.Hour), 10)
	if err != nil {
		t.Fatalf("ListByOrg: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("remaining len=%d, want 1", len(got))
	}
}

func uniqueID(i int) string {
	return "id-" + time.Now().Format("150405.000") + "-" + string(rune('a'+i))
}
