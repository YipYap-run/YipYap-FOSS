package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

func TestRequestLogOmitsPathParamsAndQuery(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	r := chi.NewRouter()
	r.Use(RequestLog(logger))
	r.Route("/api/v1", func(r chi.Router) {
		r.Post("/cloudevents/ingest/{token}", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		})
	})

	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/cloudevents/ingest/ingest-secret-abc?token=query-secret-xyz", nil)
	req.RemoteAddr = "203.0.113.7:51234"
	r.ServeHTTP(httptest.NewRecorder(), req)

	line := buf.String()
	for _, secret := range []string{"ingest-secret-abc", "query-secret-xyz"} {
		if strings.Contains(line, secret) {
			t.Fatalf("log line leaks %q: %s", secret, line)
		}
	}
	for _, want := range []string{
		`"route":"/api/v1/cloudevents/ingest/{token}"`,
		`"method":"POST"`,
		`"status":202`,
		`"client_ip":"203.0.113.7"`,
		`"duration_ms":`,
	} {
		if !strings.Contains(line, want) {
			t.Errorf("log line missing %s: %s", want, line)
		}
	}
}

func TestRequestLogUnmatchedRouteAndPanicStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	r := chi.NewRouter()
	r.Use(RequestLog(logger))
	r.Get("/boom", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "x", http.StatusInternalServerError)
	})

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/no/such/secret-path?k=v", nil))
	if line := buf.String(); strings.Contains(line, "secret-path") || !strings.Contains(line, `"route":"unmatched"`) {
		t.Errorf("unmatched request logged wrong: %s", line)
	}

	buf.Reset()
	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/boom", nil))
	if line := buf.String(); !strings.Contains(line, `"level":"ERROR"`) || !strings.Contains(line, `"status":500`) {
		t.Errorf("5xx should log at error level: %s", line)
	}
}
