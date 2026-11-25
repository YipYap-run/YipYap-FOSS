package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
)

// TestHubReceivesPublishAlertOnBroadcastSubject checks that the hub listens on
// the ws.alert.* broadcast subjects that PublishAlert writes, and that each
// alert reaches a client exactly once. (core-16)
func TestHubReceivesPublishAlertOnBroadcastSubject(t *testing.T) {
	b := bus.NewChannel()
	h := NewHub("")
	if err := h.SubscribeToBus(b); err != nil {
		t.Fatal(err)
	}
	c := newClient("org-1")
	h.register(c)

	evt, _ := json.Marshal(map[string]string{"org_id": "org-1", "monitor_id": "m1", "monitor_name": "api", "status": "down"})
	if err := b.PublishAlert(context.Background(), "alert.trigger", evt, ""); err != nil {
		t.Fatal(err)
	}

	select {
	case frame := <-c.send:
		var msg wsMessage
		if err := json.Unmarshal(frame, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "alert.fired" || msg.MonitorID != "m1" {
			t.Fatalf("frame = %s", frame)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("hub did not forward the alert")
	}
	select {
	case frame := <-c.send:
		t.Fatalf("alert delivered twice: %s", frame)
	case <-time.After(100 * time.Millisecond):
	}

	// A plain publish on the durable subject (what JetStream stores) is not
	// a broadcast and must not reach the hub.
	if err := b.Publish(context.Background(), "alert.recover", evt); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-c.send:
		t.Fatalf("hub forwarded a durable-subject publish: %s", frame)
	case <-time.After(100 * time.Millisecond):
	}
}
