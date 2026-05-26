package jwks

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// JWKSHandler serves the JWK Set at /.well-known/jwks.json. It returns
// the union of active + retiring keys so that verifiers have a
// rotation-overlap window during which either key set is acceptable.
type JWKSHandler struct {
	mgr *Manager
}

// NewJWKSHandler constructs a handler backed by mgr.
func NewJWKSHandler(mgr *Manager) *JWKSHandler {
	return &JWKSHandler{mgr: mgr}
}

// ServeHTTP implements http.Handler.
func (h *JWKSHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	keys, err := h.mgr.ListPublicJWKs(r.Context())
	if err != nil {
		slog.Warn("jwks: ListPublicJWKs failed", "err", err.Error())
		http.Error(w, "JWKS unavailable", http.StatusServiceUnavailable)
		return
	}
	if len(keys) == 0 {
		// No active key yet: tell the caller to retry rather than
		// returning an empty-but-200 set that could be cached and
		// poison verification downstream.
		http.Error(w, "no active signing key", http.StatusServiceUnavailable)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": keys})
}
