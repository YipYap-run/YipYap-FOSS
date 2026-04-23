package handlers

import "net/http"

// checkChannelPlanGate is a no-op in the FOSS build.
func checkChannelPlanGate(_ *NotificationChannelHandler, _ http.ResponseWriter, _ *http.Request, _ string) bool {
	return true
}
