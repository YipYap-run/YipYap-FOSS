package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// TestReplyAuditList exercises the GET /api/v1/admin/cloudevents/replies
// endpoint end-to-end: it seeds the audit log with three entries across
// two channels and two reply types, hits the route as the (owner) caller
// from the registration flow, and verifies both the unfiltered list and
// the in-memory channel / type / alert filters.
func TestReplyAuditList(t *testing.T) {
	ts, s := setupTestServer(t)
	token := selfServiceRegisterAndGetToken(t, ts, "AuditOrg", "admin@audit.com", "passwordA12")

	user, err := s.Users().GetByEmail(context.Background(), "admin@audit.com")
	if err != nil {
		t.Fatalf("GetByEmail: %v", err)
	}
	orgID := user.OrgID

	// Seed three audit entries. We space the timestamps so the
	// reverse-chronological ordering from ListByOrg is deterministic.
	now := time.Now().UTC().Truncate(time.Second)
	seed := []*store.CloudEventReplyAuditEntry{
		{
			ReplyID:     "evt-A",
			ReplyType:   "run.yipyap.reply.alert.suppressed.v1",
			ReplySource: "https://consumer.example.com/a",
			ReplySub:    "alice@example.com",
			AlertID:     "alert-111",
			OrgID:       orgID,
			ChannelID:   "ch-primary",
			Outcome:     "accepted",
			Before:      json.RawMessage(`{"status":"firing"}`),
			After:       json.RawMessage(`{"status":"firing","suppressed":true}`),
			ReceivedAt:  now.Add(-5 * time.Minute),
		},
		{
			ReplyID:     "evt-B",
			ReplyType:   "run.yipyap.reply.alert.escalated.v1",
			ReplySource: "https://consumer.example.com/b",
			ReplySub:    "bob@example.com",
			AlertID:     "alert-222",
			OrgID:       orgID,
			ChannelID:   "ch-secondary",
			Outcome:     "rejected:trust_required",
			ReceivedAt:  now.Add(-3 * time.Minute),
		},
		{
			ReplyID:     "evt-C",
			ReplyType:   "run.yipyap.reply.alert.suppressed.v1",
			ReplySource: "https://consumer.example.com/c",
			ReplySub:    "carol@example.com",
			AlertID:     "alert-111",
			OrgID:       orgID,
			ChannelID:   "ch-primary",
			Outcome:     "accepted",
			ReceivedAt:  now.Add(-1 * time.Minute),
		},
	}

	ctx := context.Background()
	for _, e := range seed {
		if err := s.CloudEventReplyAudit().Write(ctx, e); err != nil {
			t.Fatalf("seed Write: %v", err)
		}
	}

	// Helper: issue the GET with the given query string and decode the body.
	call := func(t *testing.T, q url.Values, wantStatus int) (map[string]any, []any) {
		t.Helper()
		u := ts.URL + "/api/v1/admin/cloudevents/replies"
		if enc := q.Encode(); enc != "" {
			u += "?" + enc
		}
		req, _ := http.NewRequest(http.MethodGet, u, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != wantStatus {
			t.Fatalf("status: want %d, got %d", wantStatus, resp.StatusCode)
		}
		if wantStatus != http.StatusOK {
			return nil, nil
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode: %v", err)
		}
		rawEntries, _ := body["entries"].([]any)
		return body, rawEntries
	}

	t.Run("unfiltered lists all three reverse-chrono", func(t *testing.T) {
		body, entries := call(t, url.Values{}, http.StatusOK)
		if len(entries) != 3 {
			t.Fatalf("entries: want 3, got %d", len(entries))
		}
		if got := body["has_more"]; got != false {
			t.Errorf("has_more: want false, got %v", got)
		}
		// Verify reverse-chronological ordering, evt-C (now-1m) is first.
		first := entries[0].(map[string]any)
		if first["reply_id"] != "evt-C" {
			t.Errorf("first entry: want evt-C, got %v", first["reply_id"])
		}
	})

	t.Run("filter by channel", func(t *testing.T) {
		q := url.Values{}
		q.Set("channel", "ch-primary")
		_, entries := call(t, q, http.StatusOK)
		if len(entries) != 2 {
			t.Fatalf("entries: want 2, got %d", len(entries))
		}
		for _, e := range entries {
			if m := e.(map[string]any); m["channel_id"] != "ch-primary" {
				t.Errorf("channel_id: want ch-primary, got %v", m["channel_id"])
			}
		}
	})

	t.Run("filter by alert", func(t *testing.T) {
		q := url.Values{}
		q.Set("alert", "alert-222")
		_, entries := call(t, q, http.StatusOK)
		if len(entries) != 1 {
			t.Fatalf("entries: want 1, got %d", len(entries))
		}
		m := entries[0].(map[string]any)
		if m["reply_id"] != "evt-B" {
			t.Errorf("reply_id: want evt-B, got %v", m["reply_id"])
		}
	})

	t.Run("filter by type", func(t *testing.T) {
		q := url.Values{}
		q.Set("type", "run.yipyap.reply.alert.suppressed.v1")
		_, entries := call(t, q, http.StatusOK)
		if len(entries) != 2 {
			t.Fatalf("entries: want 2, got %d", len(entries))
		}
	})

	t.Run("filter by since excludes older", func(t *testing.T) {
		q := url.Values{}
		q.Set("since", now.Add(-2*time.Minute).Format(time.RFC3339))
		_, entries := call(t, q, http.StatusOK)
		if len(entries) != 1 {
			t.Fatalf("entries: want 1, got %d", len(entries))
		}
		m := entries[0].(map[string]any)
		if m["reply_id"] != "evt-C" {
			t.Errorf("reply_id: want evt-C, got %v", m["reply_id"])
		}
	})

	t.Run("bad since rejected", func(t *testing.T) {
		q := url.Values{}
		q.Set("since", "not-a-timestamp")
		call(t, q, http.StatusBadRequest)
	})

	t.Run("bad limit rejected", func(t *testing.T) {
		q := url.Values{}
		q.Set("limit", "-3")
		call(t, q, http.StatusBadRequest)
	})

	t.Run("unauthenticated rejected", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/v1/admin/cloudevents/replies", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("want 401 without bearer, got %d", resp.StatusCode)
		}
	})
}
