package api

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ux-5: alert.changed hints reach clients of the alert's org only, as an
// alert.changed frame carrying the alert ID and status.
func TestHubRelaysAlertChangedToOrg(t *testing.T) {
	h := NewHub("")
	b := bus.NewChannel()
	t.Cleanup(func() { _ = b.Close() })
	if err := h.SubscribeToBus(b); err != nil {
		t.Fatal(err)
	}
	mine, other := newClient("org-1"), newClient("org-2")
	h.register(mine)
	h.register(other)

	data, _ := json.Marshal(domain.AlertChanged{OrgID: "org-1", AlertID: "a-1", MonitorID: "m-1", Status: domain.AlertAcknowledged})
	if err := b.Publish(context.Background(), domain.SubjectAlertChanged, data); err != nil {
		t.Fatal(err)
	}

	select {
	case frame := <-mine.send:
		var msg wsMessage
		if err := json.Unmarshal(frame, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Type != "alert.changed" || msg.AlertID != "a-1" || msg.MonitorID != "m-1" || msg.Status != "acknowledged" {
			t.Fatalf("frame %+v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("org-1 client got no frame")
	}
	select {
	case frame := <-other.send:
		t.Fatalf("org-2 client got another org's frame: %s", frame)
	case <-time.After(100 * time.Millisecond):
	}
}
