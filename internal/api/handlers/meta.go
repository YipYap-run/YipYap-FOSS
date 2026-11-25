package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func MetaGet(registrationEnabled bool, s store.Store, telegramBotUsername string, _ bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"edition":               "foss",
			"billing_enabled":       false,
			"registration_enabled":  registrationEnabled,
			"retention_days":        30,
			"telegram_bot_username": telegramBotUsername,
			"limits":                operatorLimitsMeta(),
			// Operator-set caps per org. 0 means unlimited.
			"operator_limits": limits.Current(),
		})
	}
}

// operatorLimitsMeta tells the SPA the operator caps, so forms can show
// the interval floor and caps without an upgrade prompt. 0 means no cap.
func operatorLimitsMeta() map[string]int {
	l := CurrentOperatorLimits()
	return map[string]int{
		"max_monitors_per_org":              l.MaxMonitorsPerOrg,
		"max_notification_channels_per_org": l.MaxNotificationChannelsPerOrg,
		"min_check_interval_seconds":        l.MinCheckIntervalSeconds,
	}
}
