package handlers

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ResetPassword allows an owner or admin to set a temporary password for another user,
// forcing them to change it on next login.
func (h *UserHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims.Role != string(domain.RoleOwner) && claims.Role != string(domain.RoleAdmin) {
		errorResponse(w, http.StatusForbidden, "insufficient permissions")
		return
	}

	id := chi.URLParam(r, "id")
	target, err := h.store.Users().GetByID(r.Context(), id)
	if err != nil || target.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	if target.Role == domain.RoleOwner {
		errorResponse(w, http.StatusForbidden, "cannot reset the owner's password")
		return
	}

	var req struct {
		TemporaryPassword string `json:"temporary_password"`
	}
	if err := decodeBody(r, &req); err != nil || req.TemporaryPassword == "" {
		errorResponse(w, http.StatusBadRequest, "temporary_password is required")
		return
	}

	hash, err := auth.HashPassword(req.TemporaryPassword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	target.PasswordHash = hash
	target.ForcePasswordChange = true
	target.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Users().Update(r.Context(), target); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to reset password")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{
		"message": "password reset, user must change on next login",
	})
}
