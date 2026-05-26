package middleware

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func requireEnterprisePlan(_ store.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// FOSS edition: no enterprise plan restriction.
			next.ServeHTTP(w, r)
		})
	}
}
