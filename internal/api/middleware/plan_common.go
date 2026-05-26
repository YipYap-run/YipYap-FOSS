package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// RequirePaidPlan rejects requests from orgs on the free plan. The FOSS
// build's variant is a no-op so self-hosted operators retain full access
// (FOSS has no billing concept).
func RequirePaidPlan(s store.Store) func(http.Handler) http.Handler {
	return requirePaidPlan(s)
}

// RequireEnterprisePlan rejects requests from orgs not on the enterprise plan.
func RequireEnterprisePlan(s store.Store) func(http.Handler) http.Handler {
	return requireEnterprisePlan(s)
}
