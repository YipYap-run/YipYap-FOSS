package handlers

import "github.com/YipYap-run/YipYap-FOSS/internal/domain"

// retentionDaysForPlan returns the data retention window in days.
// FOSS edition: fixed 30-day retention.
func retentionDaysForPlan(_ domain.OrgPlan) int {
	return 30
}
