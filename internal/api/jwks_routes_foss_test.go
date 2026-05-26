package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
)

// TestMountJWKS_FOSS confirms that under the FOSS build tag the JWKS
// endpoint is not mounted.
func TestMountJWKS_FOSS(t *testing.T) {
	r := chi.NewRouter()
	MountJWKS(r, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/.well-known/jwks.json", nil)
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("FOSS: JWKS endpoint should return 404, got %d", rr.Code)
	}
}
