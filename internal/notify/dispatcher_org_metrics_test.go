package notify

import (
	"context"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

type fakeOrgRecorder struct {
	notes []string
}

func (f *fakeOrgRecorder) Exporting(string) bool { return true }
func (f *fakeOrgRecorder) RecordCheck(context.Context, string, telemetry.CheckSample) {
}
func (f *fakeOrgRecorder) RecordEscalationStep(context.Context, string, int) {}
func (f *fakeOrgRecorder) RecordNotification(_ context.Context, orgID, channelType string, sent bool) {
	outcome := "fail"
	if sent {
		outcome = "sent"
	}
	f.notes = append(f.notes, orgID+"/"+channelType+"/"+outcome)
}

// Only jobs from the org's own channels count toward its export. Support
// and staff jobs name an org but go to platform channels.
func TestRecordOrgOutcomeCountsOnlyOrgChannels(t *testing.T) {
	rec := &fakeOrgRecorder{}
	d := NewDispatcher(nil, nil, WithOrgRecorder(rec))

	d.recordOrgOutcome(domain.NotificationJob{OrgID: "org-a", ChannelID: "ch1", Channel: "slack"}, true)
	d.recordOrgOutcome(domain.NotificationJob{OrgID: "org-a", ChannelID: "ch2", Channel: "email"}, false)
	d.recordOrgOutcome(domain.NotificationJob{OrgID: "org-a", Channel: "slack"}, true)   // support job
	d.recordOrgOutcome(domain.NotificationJob{ChannelID: "ch3", Channel: "slack"}, true) // no org

	want := []string{"org-a/slack/sent", "org-a/email/fail"}
	if len(rec.notes) != len(want) || rec.notes[0] != want[0] || rec.notes[1] != want[1] {
		t.Fatalf("recorded %v, want %v", rec.notes, want)
	}
}
