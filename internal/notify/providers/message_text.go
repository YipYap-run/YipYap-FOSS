package providers

import (
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// smsBody is the text for SMS providers. The Ack line appears only when
// the job carries an ack link, so a page never ends with an empty "Ack: ".
// "Reply STOP to opt out" is required by US carriers (CTIA / A2P 10DLC) on
// non-2FA traffic and is always appended.
func smsBody(job domain.NotificationJob) string {
	body := fmt.Sprintf("[%s] %s: %s", job.Severity, job.MonitorName, job.Message)
	if job.AckURL != "" {
		body += "\nAck: " + job.AckURL
	}
	return body + "\nReply STOP to opt out."
}

// emailBody is the plain-text body for email channels. Like smsBody, it
// leaves out the Acknowledge line when there is no ack link.
func emailBody(job domain.NotificationJob) string {
	body := fmt.Sprintf("Alert ID: %s\nMonitor: %s\nSeverity: %s\n\n%s",
		job.AlertID, job.MonitorName, job.Severity, job.Message)
	if job.AckURL != "" {
		body += "\n\nAcknowledge: " + job.AckURL
	}
	return body
}
