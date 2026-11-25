package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestHubBroadcastsAlertAcknowledgedToOrg(t *testing.T) {
	b := bus.NewChannel()
	defer func() { _ = b.Close() }()
	h := NewHub("")
	if err := h.SubscribeToBus(b); err != nil {
		t.Fatal(err)
	}
	mine := &client{orgID: "org-1", send: make(chan []byte, 4)}
	other := &client{orgID: "org-2", send: make(chan []byte, 4)}
	h.register(mine)
	h.register(other)

	data, _ := json.Marshal(domain.AlertAckedMessage{OrgID: "org-1", AlertID: "a1", MonitorID: "m1", MonitorName: "API", Status: "acknowledged"})
	if err := b.Publish(context.Background(), "alert.acknowledged", data); err != nil {
		t.Fatal(err)
	}
	select {
	case frame := <-mine.send:
		var m wsMessage
		if err := json.Unmarshal(frame, &m); err != nil {
			t.Fatal(err)
		}
		if m.Type != "alert.acknowledged" || m.AlertID != "a1" || m.MonitorID != "m1" || m.Status != "acknowledged" {
			t.Fatalf("frame = %+v", m)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame for the alert's org")
	}
	select {
	case frame := <-other.send:
		t.Fatalf("other org got %s", frame)
	case <-time.After(100 * time.Millisecond):
	}
}
