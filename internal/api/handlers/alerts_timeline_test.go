package handlers_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// The alert timeline names the person paged and the channel posted to, so
// the SPA can show "Paged Alex by SMS" without loading every user. Names from
// another org are never returned.
func TestAlertTimeline_LabelsPersonAndChannel(t *testing.T) {
	s, alert, user := setupAlertHandlerTest(t)
	ctx := context.Background()
	now := time.Now().UTC()

	user.Name = "Alex Rivera"
	if err := s.Users().Update(ctx, user); err != nil {
		t.Fatal(err)
	}
	ch := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: alert.OrgID, Type: "webhook", Name: "Ops hook",
		Config: `{"url":"https://example.com"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, ch); err != nil {
		t.Fatal(err)
	}

	other := &domain.Org{ID: uuid.New().String(), Name: "Other", Slug: "x-" + uuid.New().String()[:8], Plan: domain.PlanFree, CreatedAt: now, UpdatedAt: now}
	if err := s.Orgs().Create(ctx, other); err != nil {
		t.Fatal(err)
	}
	stranger := &domain.User{ID: uuid.New().String(), OrgID: other.ID, Email: "s@x.com", Name: "Stranger", PasswordHash: "h", Role: domain.RoleMember, CreatedAt: now, UpdatedAt: now}
	if err := s.Users().Create(ctx, stranger); err != nil {
		t.Fatal(err)
	}
	foreignCh := &domain.NotificationChannel{ID: uuid.New().String(), OrgID: other.ID, Type: "webhook", Name: "Secret hook",
		Config: `{"url":"https://example.com"}`, Enabled: true}
	if err := s.NotificationChannels().Create(ctx, foreignCh); err != nil {
		t.Fatal(err)
	}

	add := func(evtType domain.AlertEventType, channel, userID, detail string, at time.Time) {
		t.Helper()
		evt := &domain.AlertEvent{ID: uuid.New().String(), AlertID: alert.ID, EventType: evtType,
			Channel: channel, TargetUserID: userID, Detail: json.RawMessage(detail), CreatedAt: at}
		if err := s.Alerts().CreateEvent(ctx, evt); err != nil {
			t.Fatal(err)
		}
	}
	add(domain.EventNotified, "telnyx_sms", user.ID, `{"contact_method_type":"sms","step_position":0}`, now)
	add(domain.EventNotified, "webhook", "", `{"channel_id":"`+ch.ID+`","step_position":0}`, now.Add(time.Second))
	add(domain.EventNotifyFailed, "", stranger.ID, `{"channel_id":"`+foreignCh.ID+`","reason":"x"}`, now.Add(2*time.Second))

	h := handlers.NewAlertHandler(s)
	w, _, rctx := requestWithAlertID(t, alert.ID, user.ID, alert.OrgID)
	h.Timeline(w, httptest.NewRequest("GET", "/alerts/"+alert.ID+"/timeline", nil).WithContext(rctx))
	if w.Code != 200 {
		t.Fatalf("status %d body=%s", w.Code, w.Body.String())
	}

	var got []struct {
		EventType      string          `json:"event_type"`
		TargetUserID   string          `json:"target_user_id"`
		TargetUserName string          `json:"target_user_name"`
		ChannelName    string          `json:"channel_name"`
		Channel        string          `json:"channel"`
		Detail         json.RawMessage `json:"detail"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	byUser := map[string]int{}
	for i, e := range got {
		byUser[e.TargetUserID+"|"+e.Channel] = i
	}
	sms := got[byUser[user.ID+"|telnyx_sms"]]
	if sms.TargetUserName != "Alex Rivera" || len(sms.Detail) == 0 {
		t.Errorf("person page: name %q detail %s", sms.TargetUserName, sms.Detail)
	}
	hook := got[byUser["|webhook"]]
	if hook.ChannelName != "Ops hook" {
		t.Errorf("channel message: channel_name %q, want Ops hook", hook.ChannelName)
	}
	foreign := got[byUser[stranger.ID+"|"]]
	if foreign.TargetUserName != "" || foreign.ChannelName != "" {
		t.Errorf("names from another org leaked: user %q channel %q", foreign.TargetUserName, foreign.ChannelName)
	}
}
