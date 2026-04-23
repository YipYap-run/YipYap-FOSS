package handlers

import (
	"log"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type PasswordResetHandler struct {
	store         store.Store
	jwt           *auth.JWTIssuer
	mailer        *mailer.Mailer
	publicBaseURL string
}

func NewPasswordResetHandler(s store.Store, jwt *auth.JWTIssuer, m *mailer.Mailer, publicBaseURL string) *PasswordResetHandler {
	return &PasswordResetHandler{store: s, jwt: jwt, mailer: m, publicBaseURL: publicBaseURL}
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
// Returns 503 if SMTP is not configured. Otherwise always returns 200 to prevent email enumeration.
func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
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

	token, err := h.jwt.IssuePasswordReset(user.ID, user.OrgID, user.Email, user.PasswordHash)
	if err != nil {
		log.Printf("password-reset: failed to issue token for user %s: %v", user.ID, err)
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	resetURL := h.publicBaseURL + "/reset-password?token=" + token
	subject := "YipYap: Password Reset"
	body := "You requested a password reset for your YipYap account.\n\n" +
		"Click the link below to set a new password:\n\n" +
		resetURL + "\n\n" +
		"This link expires in 1 hour.\n\n" +
		"If you did not request this, you can safely ignore this email."

	go func() {
		if err := h.mailer.Send(user.Email, subject, body); err != nil {
			log.Printf("password-reset: failed to send email to user %s: %v", user.ID, err)
		}
	}()

	jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ResetPassword handles POST /api/v1/auth/reset-password.
func (h *PasswordResetHandler) ResetPassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Token == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "token and password are required")
		return
	}
	if len(req.Password) < 8 {
		errorResponse(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	claims, err := h.jwt.ValidatePasswordReset(req.Token)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid or expired reset token")
		return
	}

	user, err := h.store.Users().GetByID(r.Context(), claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	// Verify email hasn't changed since the token was issued.
	if user.Email != claims.Role {
		errorResponse(w, http.StatusUnauthorized, "reset token is no longer valid")
		return
	}

	// Verify password hasn't already been reset (single-use enforcement).
	if auth.PasswordResetNonce(user.PasswordHash) != claims.Nonce {
		errorResponse(w, http.StatusUnauthorized, "reset token is no longer valid")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to hash password")
		return
	}

	user.PasswordHash = hash
	user.ForcePasswordChange = false
	if err := h.store.Users().Update(r.Context(), user); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update password")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "password updated"})
}
