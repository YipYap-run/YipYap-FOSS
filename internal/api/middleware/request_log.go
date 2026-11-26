package middleware

import (
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

// RequestLog returns access-log middleware that writes one structured line
// per request. It logs the chi route pattern (for example /ack/{token}),
// never the raw path or query, because several routes carry credentials
// there: ingest tokens, signed ack links, heartbeat and survey tokens.
// (core-5)
//
// Mount it after the trusted-proxy middleware so client_ip is the real
// client, and before the panic recoverer so recovered panics log as 500.
// A nil logger means slog.Default() at request time.
func RequestLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := chimw.NewWrapResponseWriter(w, r.ProtoMajor)
			defer func() {
				status := ww.Status()
				if status == 0 {
					// Nothing written: net/http sends 200.
					status = http.StatusOK
				}
				route := "unmatched"
				if rctx := chi.RouteContext(r.Context()); rctx != nil {
					if p := rctx.RoutePattern(); p != "" {
						route = p
					}
				}
				level := slog.LevelInfo
				if status >= 500 {
					level = slog.LevelError
				}
				l := logger
				if l == nil {
					l = slog.Default()
				}
				// r.Context() carries the trace span for log correlation;
				// handlers do not check it for cancellation.
				l.LogAttrs(r.Context(), level, "http request",
					slog.String("method", r.Method),
					slog.String("route", route),
					slog.Int("status", status),
					slog.Int("bytes", ww.BytesWritten()),
					slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
					slog.String("client_ip", clientIP(r.RemoteAddr)),
				)
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// clientIP strips the port from RemoteAddr. The trusted-proxy middleware may
// already have replaced RemoteAddr with a bare IP.
func clientIP(remoteAddr string) string {
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		return host
	}
	return remoteAddr
}
