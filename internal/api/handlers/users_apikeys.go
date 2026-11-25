package handlers

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
)

// ListAPIKeys returns the API keys a member created, so the UI can ask
// "This person created 2 keys. Revoke them?" before removing them. Keys are
// org-owned and keep working after removal unless revoked.
func (h *UserHandler) ListAPIKeys(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	id := chi.URLParam(r, "id")
	user, err := h.store.Users().GetByID(r.Context(), id)
	if err != nil || user.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}
	keys, err := h.store.APIKeys().ListByCreator(r.Context(), claims.OrgID, user.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list api keys")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"count": len(keys),
		"keys":  apiKeyViews(r.Context(), h.store, claims.OrgID, keys),
	})
}

// parseRevokeAPIKeys reads the revoke_api_keys query flag of DELETE
// /users/{id}. Absent means false.
func parseRevokeAPIKeys(r *http.Request) (bool, error) {
	v := r.URL.Query().Get("revoke_api_keys")
	if v == "" {
		return false, nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("revoke_api_keys must be true or false")
	}
	return b, nil
}
