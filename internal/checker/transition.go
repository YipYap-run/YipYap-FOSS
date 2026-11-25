package checker

import "github.com/YipYap-run/YipYap-FOSS/internal/domain"

// Alert lifecycle subjects published on a monitor status transition.
const (
	SubjectAlertTrigger = "alert.trigger"
	SubjectAlertRecover = "alert.recover"
)

// AlertTransition returns the alert subject a status change implies, or ""
// when there is nothing to publish. prev is the last persisted status, or ""
// when the monitor has never been checked: a first check that is down or
// degraded is a transition to down and must alert (mon-13). A first check
// that is up has nothing to recover from.
//
// Both the monolith scheduler and the split check-result writer use this so
// the two deployments page on exactly the same transitions.
func AlertTransition(prev, cur domain.CheckStatus) string {
	switch cur {
	case domain.StatusDown:
		if prev == "" || prev == domain.StatusUp || prev == domain.StatusDegraded {
			return SubjectAlertTrigger
		}
	case domain.StatusDegraded:
		if prev == "" || prev == domain.StatusUp {
			return SubjectAlertTrigger
		}
	case domain.StatusUp:
		if prev == domain.StatusDown || prev == domain.StatusDegraded {
			return SubjectAlertRecover
		}
	}
	return ""
}
