package providers

import (
	"net/url"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// alertConsoleLink returns the URL of the alert in the YipYap web app. Test
// notifications carry a synthetic alert ID, so they link to the alert list.
// When no console URL is configured the job's AckURL is used, if any.
func alertConsoleLink(consoleURL string, job domain.NotificationJob) string {
	if consoleURL == "" {
		return job.AckURL
	}
	if job.AlertID == "" || strings.HasPrefix(job.AlertID, "test-") {
		return consoleURL + "/alerts"
	}
	return consoleURL + "/alerts/" + url.PathEscape(job.AlertID)
}
