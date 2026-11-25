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

func TestRedactRequestURI(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/api/v1/support/survey", "/api/v1/support/survey"},
		{"/api/v1/support/survey?token=abc.def", "/api/v1/support/survey?token=REDACTED"},
		{"/x?a=1&access_token=s3cret&b=2", "/x?a=1&access_token=REDACTED&b=2"},
		{"/x?Token=abc", "/x?Token=REDACTED"},
		{"/x?%74oken=abc", "/x?%74oken=REDACTED"},
		{"/x?id_token=a&refresh_token=b", "/x?id_token=REDACTED&refresh_token=REDACTED"},
		{"/x?tokens=keep&mytoken=keep", "/x?tokens=keep&mytoken=keep"},
	}
	for _, c := range cases {
		if got, _ := RedactRequestURI(c.in); got != c.want {
			t.Errorf("RedactRequestURI(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRedactLogQueryHidesTokenFromLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := chimw.RequestLogger(&chimw.DefaultLogFormatter{Logger: log.New(&buf, "", 0), NoColor: true})

	var seenURI, seenToken string
	h := RedactLogQuery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenURI = r.RequestURI
		seenToken = r.URL.Query().Get("token")
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/support/survey?token=eyJsecret.value&x=1", nil)
	h.ServeHTTP(httptest.NewRecorder(), req)

	out := buf.String()
	if strings.Contains(out, "eyJsecret") {
		t.Fatalf("log contains the token: %s", out)
	}
	if !strings.Contains(out, "token=REDACTED&x=1") {
		t.Errorf("log missing redacted URI: %s", out)
	}
	if seenURI != "/api/v1/support/survey?token=eyJsecret.value&x=1" {
		t.Errorf("handler RequestURI = %q, want original", seenURI)
	}
	if seenToken != "eyJsecret.value" {
		t.Errorf("handler token = %q", seenToken)
	}
}
