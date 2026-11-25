package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

func TestRequireWriterRole(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	h := middleware.RequireWriterRole()(ok)

	cases := []struct {
		role, method string
		want         int
	}{
		{"viewer", http.MethodGet, http.StatusNoContent},
		{"viewer", http.MethodHead, http.StatusNoContent},
		{"viewer", http.MethodPost, http.StatusForbidden},
		{"viewer", http.MethodPatch, http.StatusForbidden},
		{"viewer", http.MethodPut, http.StatusForbidden},
		{"viewer", http.MethodDelete, http.StatusForbidden},
		{"", http.MethodPost, http.StatusForbidden},
		{"readonly", http.MethodPost, http.StatusForbidden},
		{"superuser", http.MethodPost, http.StatusForbidden},
		{"member", http.MethodPost, http.StatusNoContent},
		{"admin", http.MethodDelete, http.StatusNoContent},
		{"owner", http.MethodPatch, http.StatusNoContent},
		{"api_key", http.MethodPost, http.StatusNoContent},
	}
	for _, c := range cases {
		req := httptest.NewRequest(c.method, "/x", nil)
		req = req.WithContext(middleware.SetClaims(req.Context(), &auth.Claims{UserID: "u", OrgID: "o", Role: c.role}))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != c.want {
			t.Errorf("role %q %s: got %d, want %d", c.role, c.method, rec.Code, c.want)
		}
	}

	// No claims at all on a write is unauthenticated.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/x", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("no claims POST: got %d, want 401", rec.Code)
	}
}
