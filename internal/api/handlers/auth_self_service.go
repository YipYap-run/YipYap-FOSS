package handlers

import (
	"crypto/subtle"
	"fmt"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
)

type changePasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
	mfaProof
}

type changeEmailRequest struct {
	CurrentPassword string `json:"current_password"`
	NewEmail        string `json:"new_email"`
	mfaProof
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
	if !checkPasswordPolicy(w, req.NewPassword) {
		return
	}
	// A forced change after an admin reset must not keep the temporary
	// password, and reusing the current one is never a change.
	if req.NewPassword == req.CurrentPassword {
		errorResponse(w, http.StatusBadRequest, "new password must be different from the current one")
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

	if err := verifyMFAIfEnabled(r.Context(), h.store, user, req.mfaProof); err != nil {
		writeStepUpError(w, http.StatusForbidden, err)
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

	// A password change signs out every other session. The current one
	// stays so the user is not logged out of the tab they changed it in.
	if err := session.RevokeAll(r.Context(), h.store, user.ID, claims.SID); err != nil {
		slog.Error("change password: revoke other sessions", "user_id", user.ID, "error", err)
		errorResponse(w, http.StatusInternalServerError, "password changed, but signing out other sessions failed")
		return
	}

	token, err := session.Renew(r.Context(), h.store, h.jwt, claims, string(user.Role))
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue token")
		return
	}

	h.setSessionCookie(w, r, token)
	jsonResponse(w, http.StatusOK, authResponse{Token: token, User: user})
}

// emailChangeCooldown limits how often a user can mail a new confirmation
// link, so the endpoint cannot be used to spam an address.
const emailChangeCooldown = time.Minute

// ChangeEmail handles PUT /api/v1/auth/email. It re-checks the password (and
// MFA when enrolled), then stores the new address as pending, mails a
// confirmation link to it and a notice with a cancel link to the current
// address. The account email changes only when the link is used (auth-13).
//
// Without a configured mailer (self-hosted, no SMTP) nothing can confirm the
// address, so the change applies at once as before and the response says so.
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
	if req.CurrentPassword == "" || strings.TrimSpace(req.NewEmail) == "" {
		errorResponse(w, http.StatusBadRequest, "current_password and new_email are required")
		return
	}
	req.NewEmail = strings.TrimSpace(req.NewEmail)
	if len(req.NewEmail) > 254 {
		errorResponse(w, http.StatusBadRequest, "email address too long")
		return
	}
	newEmail, ok := normalizedEmail(w, req.NewEmail)
	if !ok {
		return
	}
	// Accept a bare address only ("a@b.c", not "Name <a@b.c>").
	if addr, err := mail.ParseAddress(newEmail); err != nil || addr.Address != newEmail {
		errorResponse(w, http.StatusBadRequest, "invalid email address")
		return
	}

	ctx := r.Context()
	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to load user")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.CurrentPassword); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid current password")
		return
	}

	if err := verifyMFAIfEnabled(ctx, h.store, user, req.mfaProof); err != nil {
		writeStepUpError(w, http.StatusForbidden, err)
		return
	}

	if newEmail == user.Email {
		jsonResponse(w, http.StatusBadRequest, map[string]string{
			"error": "That is already your email address.",
			"code":  codeEmailUnchanged,
		})
		return
	}
	if emailInUse(ctx, h.store, newEmail, user.ID) {
		jsonResponse(w, http.StatusConflict, map[string]string{
			"error": "Another account already uses this email address.",
			"code":  codeEmailTaken,
		})
		return
	}

	now := time.Now().UTC().Truncate(time.Second)

	if h.mailer == nil {
		user.Email = newEmail
		user.UpdatedAt = now
		if err := h.store.Users().Update(ctx, user); err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to update user")
			return
		}
		token, err := session.Renew(ctx, h.store, h.jwt, claims, string(user.Role))
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to issue token")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"status": "updated",
			"token":  token,
			"user":   user,
		})
		return
	}

	if user.PendingEmailRequestedAt != nil && now.Sub(*user.PendingEmailRequestedAt) < emailChangeCooldown {
		retry := int((emailChangeCooldown - now.Sub(*user.PendingEmailRequestedAt)).Seconds())
		if retry < 1 {
			retry = 1
		}
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		jsonResponse(w, http.StatusTooManyRequests, map[string]interface{}{
			"error":       "Wait a minute before requesting another confirmation email.",
			"code":        codeEmailChangeCooldown,
			"retry_after": retry,
		})
		return
	}

	nonce, err := randomHex(16)

	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to start email change")
		return
	}
	confirmToken, err := h.jwt.IssueEmailChange(auth.EmailChangeConfirmAudience, user.ID, user.OrgID, newEmail, nonce)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to start email change")
		return
	}
	cancelToken, err := h.jwt.IssueEmailChange(auth.EmailChangeCancelAudience, user.ID, user.OrgID, newEmail, nonce)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to start email change")
		return
	}
	if err := h.store.Users().SetPendingEmail(ctx, user.ID, newEmail, nonce, now); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to start email change")
		return
	}

	h.sendEmailChangeMail(user, newEmail, confirmToken, cancelToken)

	jsonResponse(w, http.StatusAccepted, map[string]interface{}{
		"status":                     "confirmation_sent",
		"pending_email":              newEmail,
		"pending_email_requested_at": now,
	})
}

// sendEmailChangeMail mails the confirmation link to the new address and a
// notice with a cancel link to the current one. Best effort; failures are
// logged without the addresses.
func (h *AuthHandler) sendEmailChangeMail(user *domain.User, newEmail, confirmToken, cancelToken string) {
	confirmURL := h.publicBaseURL + "/confirm-email-change?token=" + confirmToken
	cancelURL := h.publicBaseURL + "/cancel-email-change?token=" + cancelToken
	hours := int(auth.EmailChangeTTL.Hours())

	confirmBody := "Someone asked to use this address for a YipYap account.\n\n" +
		"Confirm the change with the link below:\n\n" +
		confirmURL + "\n\n" +
		fmt.Sprintf("This link expires in %d hours. Until you confirm, the account keeps its current email.\n\n", hours) +
		"If you did not ask for this, ignore this email and nothing changes."
	noticeBody := "A request was made to change the email on your YipYap account to " + maskEmail(newEmail) + ".\n\n" +
		"Nothing changes until the new address is confirmed.\n\n" +
		"If this was not you, cancel the change with the link below, then change your password:\n\n" +
		cancelURL + "\n\n" +
		fmt.Sprintf("This link expires in %d hours.", hours)

	oldEmail, id := user.Email, user.ID
	go func() {
		if err := h.send(newEmail, "YipYap: Confirm your new email address", confirmBody); err != nil {
			slog.Error("email change: send confirmation", "user_id", id, "error", err)
		}
		if err := h.send(oldEmail, "YipYap: Email change requested", noticeBody); err != nil {
			slog.Error("email change: send notice to current address", "user_id", id, "error", err)
		}
	}()
}

// maskEmail hides most of the local part: "jane.doe@example.com" becomes
// "j***@example.com". The notice to the old address names the domain so the
// owner can recognize the request without the mail exposing the address.
func maskEmail(email string) string {
	at := strings.LastIndexByte(email, '@')
	if at <= 0 {
		return "***"
	}
	return email[:1] + "***" + email[at:]
}

type emailChangeTokenRequest struct {
	Token string `json:"token"`
}

// ConfirmEmailChange handles POST /api/v1/auth/confirm-email-change. The
// token from the link mailed to the new address is the proof; no session
// is needed, so the link works in another browser.
func (h *AuthHandler) ConfirmEmailChange(w http.ResponseWriter, r *http.Request) {
	var req emailChangeTokenRequest
	if err := decodeBody(r, &req); err != nil || req.Token == "" {
		errorResponse(w, http.StatusBadRequest, "token is required")
		return
	}
	claims, err := h.jwt.ValidateEmailChange(auth.EmailChangeConfirmAudience, req.Token)
	if err != nil {
		writeEmailChangeLinkInvalid(w)
		return
	}
	ctx := r.Context()
	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil || user.DisabledAt != nil || !pendingMatches(user, claims) {
		writeEmailChangeLinkInvalid(w)
		return
	}
	newEmail := user.PendingEmail
	if emailInUse(ctx, h.store, newEmail, user.ID) {
		jsonResponse(w, http.StatusConflict, map[string]string{
			"error": "Another account now uses this email address. Request the change again with a different address.",
			"code":  codeEmailTaken,
		})
		return
	}
	oldEmail := user.Email
	if err := h.store.Users().ConfirmPendingEmail(ctx, user.ID, newEmail, claims.Nonce, time.Now().UTC().Truncate(time.Second)); err != nil {
		// A concurrent confirm or a unique-index hit lands here.
		if emailInUse(ctx, h.store, newEmail, user.ID) {
			jsonResponse(w, http.StatusConflict, map[string]string{
				"error": "Another account now uses this email address. Request the change again with a different address.",
				"code":  codeEmailTaken,
			})
			return
		}
		writeEmailChangeLinkInvalid(w)
		return
	}

	if h.mailer != nil {
		body := "The email on your YipYap account was changed to " + maskEmail(newEmail) + ".\n\n" +
			"This address no longer signs in or receives password resets for the account.\n\n" +
			"If you did not make this change, contact support right away."
		id := user.ID
		go func() {
			if err := h.send(oldEmail, "YipYap: Your email address was changed", body); err != nil {
				slog.Error("email change: send completion notice", "user_id", id, "error", err)
			}
		}()
	}

	jsonResponse(w, http.StatusOK, map[string]string{
		"status": "email_changed",
		"email":  newEmail,
	})
}

// CancelEmailChange handles POST /api/v1/auth/cancel-email-change with the
// token from the notice mailed to the current address. It works without a
// session so the owner can stop a change even if signed out.
func (h *AuthHandler) CancelEmailChange(w http.ResponseWriter, r *http.Request) {
	var req emailChangeTokenRequest
	if err := decodeBody(r, &req); err != nil || req.Token == "" {
		errorResponse(w, http.StatusBadRequest, "token is required")
		return
	}
	claims, err := h.jwt.ValidateEmailChange(auth.EmailChangeCancelAudience, req.Token)
	if err != nil {
		writeEmailChangeLinkInvalid(w)
		return
	}
	ctx := r.Context()
	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil || !pendingMatches(user, claims) {
		writeEmailChangeLinkInvalid(w)
		return
	}
	if err := h.store.Users().ClearPendingEmail(ctx, user.ID, claims.Nonce); err != nil {
		writeEmailChangeLinkInvalid(w)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

// CancelPendingEmail handles DELETE /api/v1/auth/email/pending for the
// signed-in user.
func (h *AuthHandler) CancelPendingEmail(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err := h.store.Users().ClearPendingEmail(r.Context(), claims.UserID, ""); err != nil {
		errorResponse(w, http.StatusNotFound, "no email change is pending")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pendingMatches reports whether an email change token belongs to the
// user's current pending request.
func pendingMatches(user *domain.User, claims *auth.Claims) bool {
	if user.PendingEmail == "" || user.PendingEmailNonce == "" {
		return false
	}
	if subtle.ConstantTimeCompare([]byte(user.PendingEmailNonce), []byte(claims.Nonce)) != 1 {
		return false
	}
	return user.PendingEmail == domain.NormalizeEmail(claims.Role)
}

func writeEmailChangeLinkInvalid(w http.ResponseWriter) {
	jsonResponse(w, http.StatusBadRequest, map[string]string{
		"error": "This link has expired or was already used. Request the change again from Account settings.",
		"code":  "email_change_link_invalid",
	})

}
