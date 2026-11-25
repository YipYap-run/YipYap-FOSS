package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
)

// timelineRow is the shape the SPA reads from GET /alerts/{id}/timeline
// (web/src/pages/alerts/timeline-logic.mjs describeEvent).
type timelineRow struct {
	EventType string `json:"event_type"`
	Channel   string `json:"channel"`
	Detail    struct {
		JobID       string `json:"job_id"`
		ChannelID   string `json:"channel_id"`
		ChannelName string `json:"channel_name"`
		Reason      string `json:"reason"`
	} `json:"detail"`
}

// The alert timeline API carries delivery outcomes with the fields the
// timeline renders: event type, channel type, channel id and name, and for
// failures the sanitized reason. The channel API reflects the same failure
// in its health fields.
func TestAlertTimeline_DeliveryOutcomeContract(t *testing.T) {
	f := setupAckLink(t, domain.AlertFiring)
	ctx := context.Background()
	rec := notify.NewRecorder(f.store)

	job := domain.NotificationJob{ID: "job-sent-1", AlertID: f.alert.ID, OrgID: f.alert.OrgID,
		Channel: "smtp", ChannelID: f.channel.ID, ChannelName: f.channel.Name}
	if err := rec.RecordSent(ctx, job); err != nil {
		t.Fatal(err)
	}
	reason := "Delivery failed: the mail server rejected the login."
	if _, err := rec.RecordFailed(ctx, notify.DeadLetter{JobID: "job-failed-1", AlertID: f.alert.ID,
		OrgID: f.alert.OrgID, ChannelID: f.channel.ID, ChannelName: f.channel.Name, ChannelType: "smtp",
		Reason: reason, FailedAt: time.Now().UTC().Add(time.Second)}); err != nil {
		t.Fatal(err)
	}

	session := sessionFor(t, f.store, ackTestIssuer, f.user)
	resp := doJSONReq(t, http.MethodGet, f.baseURL+"/api/v1/alerts/"+f.alert.ID+"/timeline", session, nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("timeline status = %d", resp.StatusCode)
	}
	var rows []timelineRow
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		t.Fatal(err)
	}
	byType := map[string]timelineRow{}
	for _, r := range rows {
		byType[r.EventType] = r
	}

	sent, ok := byType[string(domain.EventNotificationSent)]
	if !ok {
		t.Fatalf("no notification_sent row in %+v", rows)
	}
	if sent.Channel != "smtp" || sent.Detail.ChannelID != f.channel.ID || sent.Detail.ChannelName != f.channel.Name || sent.Detail.JobID != "job-sent-1" || sent.Detail.Reason != "" {
		t.Errorf("sent row = %+v", sent)
	}
	failed, ok := byType[string(domain.EventNotificationFailed)]
	if !ok {
		t.Fatalf("no notification_failed row in %+v", rows)
	}
	if failed.Channel != "smtp" || failed.Detail.ChannelID != f.channel.ID || failed.Detail.ChannelName != f.channel.Name || failed.Detail.Reason != reason {
		t.Errorf("failed row = %+v", failed)
	}

	got := getChannelHealth(t, f.baseURL+"/api/v1/notification-channels/"+f.channel.ID, session)
	if got.Health.Status != "failing" || got.Health.LastError != reason || got.Health.ConsecutiveFailures != 1 || got.Health.LastFailureAt == nil {
		t.Errorf("channel health = %+v", got.Health)
	}
}
