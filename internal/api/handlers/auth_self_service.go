package handlers

import (
	"net/http"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	MFACode         string `json:"mfa_code"`
}

type changeEmailRequest struct {
	CurrentPassword string `json:"current_password"`
	NewEmail        string `json:"new_email"`
	MFACode         string `json:"mfa_code"`
}

func (h *AuthHandler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req changePasswordRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		errorResponse(w, http.StatusBadRequest, "current_password and new_password are required")
		return
	}

	user, err := h.store.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.CurrentPassword); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid current password")
		return
	}

	if err := verifyMFAIfEnabled(r.Context(), h.store, user, req.MFACode); err != nil {
		errorResponse(w, http.StatusForbidden, "MFA verification failed")
		return
	}

	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user.PasswordHash = hash
	user.ForcePasswordChange = false
	user.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Users().Update(r.Context(), user); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	token, err := h.jwt.Issue(user.ID, user.OrgID, string(user.Role))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}

func (h *AuthHandler) ChangeEmail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return
	}

	var req changeEmailRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.CurrentPassword == "" || req.NewEmail == "" {
		errorResponse(w, http.StatusBadRequest, "current_password and new_email are required")
		return
	}

	user, err := h.store.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.CurrentPassword); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid current password")
		return
	}

	if err := verifyMFAIfEnabled(r.Context(), h.store, user, req.MFACode); err != nil {
		errorResponse(w, http.StatusForbidden, "MFA verification failed")
		return
	}

	// Check new email isn't already taken.
	if existing, err := h.store.Users().GetByEmail(r.Context(), req.NewEmail); err == nil && existing != nil {
		errorResponse(w, http.StatusConflict, "email already in use")
		return
	}

	user.Email = req.NewEmail
	user.UpdatedAt = time.Now().UTC().Truncate(time.Second)

	if err := h.store.Users().Update(r.Context(), user); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update user")
		return
	}

	token, err := h.jwt.Issue(user.ID, user.OrgID, string(user.Role))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}
