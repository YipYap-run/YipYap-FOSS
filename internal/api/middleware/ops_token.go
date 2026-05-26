package middleware

import "net/http"

func RequireOpsToken(_ string) func(http.Handler) http.Handler {
	return func(_ http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "not found")
		})
	}
}
