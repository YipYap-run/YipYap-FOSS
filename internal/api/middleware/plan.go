package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// requirePaidPlan is a no-op in the FOSS build: self-hosted operators have
// no billing concept and every paid-only feature is unlocked.
func requirePaidPlan(_ store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
		})
	}
}
