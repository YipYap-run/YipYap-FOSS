package providers

import (
	"strings"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

func TestMessageText_OmitsEmptyAckLink(t *testing.T) {
	job := domain.NotificationJob{AlertID: "a1", MonitorName: "api", Severity: "critical", Message: "Alert: api is firing"}
	if got := smsBody(job); strings.Contains(got, "Ack:") || !strings.HasSuffix(got, "Reply STOP to opt out.") {
		t.Fatalf("sms body without ack link = %q", got)
	}
	if got := emailBody(job); strings.Contains(got, "Acknowledge") {
		t.Fatalf("email body without ack link = %q", got)
	}

	job.AckURL = "https://yipyap.example/ack/abc"
	if got := smsBody(job); !strings.Contains(got, "\nAck: https://yipyap.example/ack/abc\n") {
		t.Fatalf("sms body with ack link = %q", got)
	}
	if got := emailBody(job); !strings.HasSuffix(got, "Acknowledge: https://yipyap.example/ack/abc") {
		t.Fatalf("email body with ack link = %q", got)
	}
}
