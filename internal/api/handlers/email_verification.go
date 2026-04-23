package handlers

import (
	"net/http"
	"time"
)

// VerifyEmail handles POST /api/v1/auth/verify-email.
// Body: {"token": "..."}. On success, marks the user verified.
func (h *AuthHandler) VerifyEmail(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil || req.Token == "" {
		errorResponse(w, http.StatusBadRequest, "token is required")
		return
	}

	claims, err := h.jwt.ValidateEmailVerification(req.Token)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid or expired verification token")
		return
	}

	user, err := h.store.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	// Email must match what the token was issued for (prevents reuse after email change).
	if user.Email != claims.Role {
		errorResponse(w, http.StatusUnauthorized, "verification token is no longer valid")
		return
	}

	if user.EmailVerifiedAt != nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "already_verified"})
		return
	}

	if err := h.store.Users().MarkEmailVerified(r.Context(), user.ID, time.Now().UTC()); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to mark email verified")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "verified"})
}

// ResendVerification handles POST /api/v1/auth/resend-verification.
// Body: {"email": "..."}. Always returns 200 to prevent enumeration, but
// enforces 30s cooldown and max 5 sends per rolling 1h window per user.
func (h *AuthHandler) ResendVerification(w http.ResponseWriter, r *http.Request) {
	if h.mailer == nil {
		errorResponse(w, http.StatusServiceUnavailable, "email service not configured")
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil || req.Email == "" {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	user, err := h.store.Users().GetByEmail(r.Context(), req.Email)
	if err != nil || user == nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	if user.EmailVerifiedAt != nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "already_verified"})
		return
	}

	now := time.Now().UTC()

	// 30s cooldown since last send.
	if user.EmailVerificationSentAt != nil && now.Sub(*user.EmailVerificationSentAt) < 30*time.Second {
		retry := int((30*time.Second - now.Sub(*user.EmailVerificationSentAt)).Seconds())
		if retry < 1 {
			retry = 1
		}
		jsonResponse(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":       "please wait before requesting another email",
			"retry_after": retry,
		})
		return
	}

	// Max 5 sends per 1h rolling window.
	if user.EmailVerificationResendWindowStarted != nil &&
		now.Sub(*user.EmailVerificationResendWindowStarted) < time.Hour &&
		user.EmailVerificationResendCount >= 5 {
		retry := int((time.Hour - now.Sub(*user.EmailVerificationResendWindowStarted)).Seconds())
		if retry < 1 {
			retry = 1
		}
		jsonResponse(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":       "too many verification emails requested; try again later",
			"retry_after": retry,
		})
		return
	}

	h.sendVerificationEmail(r.Context(), user)
	jsonResponse(w, http.StatusOK, map[string]string{"status": "sent"})
}
