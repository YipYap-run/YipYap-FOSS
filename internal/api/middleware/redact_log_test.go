package middleware

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	chimw "github.com/go-chi/chi/v5/middleware"
)

func TestRedactSecretPathsHidesTokenFromRequestLog(t *testing.T) {
	var buf bytes.Buffer
	logger := chimw.RequestLogger(&chimw.DefaultLogFormatter{Logger: log.New(&buf, "", 0), NoColor: true})
	var sawPath string
	h := RedactSecretPaths(logger(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawPath = r.URL.Path
	})))

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/ack/secret.token.value", nil))
	if strings.Contains(buf.String(), "secret.token.value") {
		t.Fatalf("token logged: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "/ack/[redacted]") {
		t.Fatalf("log line = %s", buf.String())
	}
	if sawPath != "/ack/secret.token.value" {
		t.Fatalf("handler saw path %q, routing must be unchanged", sawPath)
	}

	buf.Reset()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/meta", nil))
	if !strings.Contains(buf.String(), "/api/v1/meta") {
		t.Fatalf("other paths must log as-is: %s", buf.String())
	}
}
