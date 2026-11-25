package handlers

import (
	"log"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type PasswordResetHandler struct {
	store         store.Store
	jwt           *auth.JWTIssuer
	mailer        *mailer.Mailer
	publicBaseURL string
	// sendMail replaces mailer.Send in tests that inspect outgoing mail.
	sendMail func(to, subject, body string) error
}

func (h *PasswordResetHandler) send(to, subject, body string) error {
	if h.sendMail != nil {
		return h.sendMail(to, subject, body)
	}
	return h.mailer.Send(to, subject, body)
}

func NewPasswordResetHandler(s store.Store, jwt *auth.JWTIssuer, m *mailer.Mailer, publicBaseURL string) *PasswordResetHandler {
	return &PasswordResetHandler{store: s, jwt: jwt, mailer: m, publicBaseURL: publicBaseURL}
}

// ForgotPassword handles POST /api/v1/auth/forgot-password.
// When SMTP is not configured the route appears unmounted (404), see the
// indistinguishable-from-unmounted note in ResendVerification. When SMTP
// IS configured, always returns 200 to prevent email enumeration.
func (h *PasswordResetHandler) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if h.mailer == nil {
		http.NotFound(w, r)
		return
	}

	var req struct {
		Email string `json:"email"`
	}
	if err := decodeBody(r, &req); err != nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	req.Email = domain.NormalizeEmail(req.Email)
	if req.Email == "" {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	user, err := h.store.Users().GetByEmail(r.Context(), req.Email)
	if err != nil || user == nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Members of an org that enforces SSO have no password to reset. The
	// response stays the same so it does not reveal the account exists.
	if ssoResetBlocked(r.Context(), h.store, user) {
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
		if err := h.send(user.Email, subject, body); err != nil {
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
	if !checkPasswordPolicy(w, req.Password) {
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
	if user.Email != domain.NormalizeEmail(claims.Role) {
		errorResponse(w, http.StatusUnauthorized, "reset token is no longer valid")
		return
	}

	// Verify password hasn't already been reset (single-use enforcement).
	if auth.PasswordResetNonce(user.PasswordHash) != claims.Nonce {
		errorResponse(w, http.StatusUnauthorized, "reset token is no longer valid")
		return
	}

	// A link sent before the org turned on Enforce SSO must not set a
	// password afterwards.
	if ssoResetBlocked(r.Context(), h.store, user) {
		jsonResponse(w, http.StatusForbidden, map[string]string{
			"error": "your organization requires single sign-on, so this account has no password to reset. Use Sign in with SSO.",
			"code":  "sso_required",
		})
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
	// A reset means the old password may be known to someone else: sign
	// out every session.
	if err := session.RevokeAll(r.Context(), h.store, user.ID, ""); err != nil {
		log.Printf("reset password: revoke sessions for %s: %v", user.ID, err)
		errorResponse(w, http.StatusInternalServerError, "password updated, but signing out existing sessions failed")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "password updated"})
}
