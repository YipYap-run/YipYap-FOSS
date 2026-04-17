package middleware

import (
	"net/http"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// Latency returns middleware that records HTTP request latency via the
// provided OTEL histogram. Pass nil to disable (no-op).
func Latency(h metric.Float64Histogram) func(http.Handler) http.Handler {
	if h == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			next.ServeHTTP(w, r)
			h.Record(r.Context(), float64(time.Since(start).Milliseconds()))
		})
	}
}
