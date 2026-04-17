package handlers

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// AccountDeleteHandler handles account deletion requests and confirmations.
type AccountDeleteHandler struct {
	store   store.Store
	jwt     *auth.JWTIssuer
	mailer  *mailer.Mailer
	baseURL string
}

// NewAccountDeleteHandler creates a new AccountDeleteHandler.
func NewAccountDeleteHandler(s store.Store, jwt *auth.JWTIssuer, m *mailer.Mailer, baseURL string) *AccountDeleteHandler {
	return &AccountDeleteHandler{store: s, jwt: jwt, mailer: m, baseURL: baseURL}
}

type deletionRequest struct {
	Password      string `json:"password"`
	MFACode       string `json:"mfa_code"`
	ConfirmPhrase string `json:"confirm_phrase"`
}

const requiredConfirmPhrase = "I am sure I wish to delete my account"

// RequestDeletion handles POST /api/v1/auth/delete-account.
// The authenticated user provides their password, MFA code, and a typed
// confirmation phrase. On success a confirmation email is sent.
func (h *AccountDeleteHandler) RequestDeletion(w http.ResponseWriter, r *http.Request) {
	if h.mailer == nil {
		errorResponse(w, http.StatusServiceUnavailable, "email service not configured")
		return
	}

	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	var req deletionRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.ConfirmPhrase != requiredConfirmPhrase {
		errorResponse(w, http.StatusBadRequest, "confirm_phrase must be exactly: "+requiredConfirmPhrase)
		return
	}

	ctx := r.Context()

	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid password")
		return
	}

	if err := verifyMFAIfEnabled(ctx, h.store, user, req.MFACode); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid MFA code")
		return
	}

	// Owner guard: owners must transfer ownership or remove all members first.
	// NOTE: There is a small TOCTOU window between this check and
	// ConfirmDeletion, which re-checks the guard at confirmation time (H4).
	if user.Role == domain.RoleOwner {
		members, err := h.store.Users().ListByOrg(ctx, user.OrgID, store.ListParams{Limit: 10})
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to check org membership")
			return
		}
		activeCount := 0
		for _, m := range members {
			if m.DisabledAt == nil {
				activeCount++
			}
		}
		if activeCount > 1 {
			errorResponse(w, http.StatusConflict, "you must transfer ownership or remove all other members before deleting your account")
			return
		}
	}

	token, err := h.jwt.IssueAccountDeletion(user.ID, user.OrgID, user.Email, user.PasswordHash)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue deletion token")
		return
	}

	confirmURL := fmt.Sprintf("%s/account/confirm-delete?token=%s", h.baseURL, url.QueryEscape(token))
	subject := "Confirm Account Deletion"
	body := "You requested to delete your YipYap account.\n\n" +
		"Click the link below to confirm:\n\n" +
		confirmURL + "\n\n" +
		"After confirmation your account will be disabled and permanently deleted after a 96-hour grace period.\n\n" +
		"If you did not request this, you can safely ignore this email."

	go func() {
		if err := h.mailer.Send(user.Email, subject, body); err != nil {
			slog.Error("account-delete: send confirmation email failed", "user", user.ID, "error", err)
		}
	}()

	jsonResponse(w, http.StatusOK, map[string]string{"status": "confirmation email sent"})
}

// ConfirmDeletion handles POST /api/v1/auth/confirm-delete.
// The token from the confirmation email is validated and the account is disabled.
func (h *AccountDeleteHandler) ConfirmDeletion(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil || req.Token == "" {
		errorResponse(w, http.StatusBadRequest, "token is required")
		return
	}
	token := req.Token

	claims, err := h.jwt.ValidateAccountDeletion(token)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid or expired deletion token")
		return
	}

	ctx := r.Context()

	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	// Idempotent: if already disabled, return success.
	if user.DisabledAt != nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"status": "already_disabled"})
		return
	}

	// Verify email hasn't changed since the token was issued.
	if user.Email != claims.Role {
		errorResponse(w, http.StatusUnauthorized, "deletion token is no longer valid")
		return
	}

	// Verify password hasn't changed (single-use enforcement).
	if auth.PasswordResetNonce(user.PasswordHash) != claims.Nonce {
		errorResponse(w, http.StatusUnauthorized, "link already used or password changed")
		return
	}

	// Re-check owner guard at confirmation time to close the TOCTOU window.
	if user.Role == domain.RoleOwner {
		members, err := h.store.Users().ListByOrg(ctx, user.OrgID, store.ListParams{Limit: 2})
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to verify membership")
			return
		}
		activeCount := 0
		for _, m := range members {
			if m.DisabledAt == nil {
				activeCount++
			}
		}
		if activeCount > 1 {
			errorResponse(w, http.StatusConflict, "transfer ownership before deleting your account")
			return
		}
	}

	if err := h.store.Users().Disable(ctx, user.ID, time.Now().UTC()); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to disable account")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "account disabled, will be permanently deleted in 96 hours"})
}

type recoveryRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// RequestRecovery handles POST /api/v1/auth/recover-account.
// A disabled user provides their email and password to receive a recovery link.
func (h *AccountDeleteHandler) RequestRecovery(w http.ResponseWriter, r *http.Request) {
	if h.mailer == nil {
		errorResponse(w, http.StatusServiceUnavailable, "email service not configured")
		return
	}

	var req recoveryRequest
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" || req.Password == "" {
		errorResponse(w, http.StatusBadRequest, "email and password are required")
		return
	}

	ctx := r.Context()

	user, err := h.store.Users().GetByEmail(ctx, req.Email)
	if err != nil {
		// User not found - run dummy bcrypt to prevent timing leak.
		_ = auth.VerifyPassword("$2a$10$0000000000000000000000uKMCRsMNJOq3hGBEExGSm3LpaFGvleq", req.Password)
		errorResponse(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if err := auth.VerifyPassword(user.PasswordHash, req.Password); err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	if user.DisabledAt == nil {
		// Return same error as invalid credentials to avoid leaking account status.
		errorResponse(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	// Per-account rate limiting: the existing IP rate limit (10/min) provides
	// reasonable protection against recovery email spam. A dedicated per-user
	// cooldown would require an additional DB field or cache, which is not
	// justified given the IP-level constraint already in place.

	token, err := h.jwt.IssueAccountRecovery(user.ID, user.OrgID, user.Email, user.PasswordHash)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to issue recovery token")
		return
	}

	confirmURL := fmt.Sprintf("%s/account/confirm-recover?token=%s", h.baseURL, url.QueryEscape(token))
	subject := "Recover Your Account"
	body := "You requested to recover your YipYap account.\n\n" +
		"Click the link below to re-enable your account:\n\n" +
		confirmURL + "\n\n" +
		"If you did not request this, you can safely ignore this email."

	go func() {
		if err := h.mailer.Send(user.Email, subject, body); err != nil {
			slog.Error("account-recovery: send recovery email failed", "user", user.ID, "error", err)
		}
	}()

	jsonResponse(w, http.StatusOK, map[string]string{"status": "recovery email sent"})
}

// ConfirmRecovery handles POST /api/v1/auth/confirm-recover.
// The token from the recovery email is validated and the account is re-enabled.
func (h *AccountDeleteHandler) ConfirmRecovery(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeBody(r, &req); err != nil || req.Token == "" {
		errorResponse(w, http.StatusBadRequest, "token is required")
		return
	}
	token := req.Token

	claims, err := h.jwt.ValidateAccountRecovery(token)
	if err != nil {
		errorResponse(w, http.StatusUnauthorized, "invalid or expired recovery token")
		return
	}

	ctx := r.Context()

	user, err := h.store.Users().GetByID(ctx, claims.UserID)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "user not found")
		return
	}

	// Idempotent: if already active, return success.
	if user.DisabledAt == nil {
		jsonResponse(w, http.StatusOK, map[string]interface{}{"status": "already_active"})
		return
	}

	if user.Email != claims.Role {
		errorResponse(w, http.StatusUnauthorized, "recovery token is no longer valid")
		return
	}

	if auth.PasswordResetNonce(user.PasswordHash) != claims.Nonce {
		errorResponse(w, http.StatusUnauthorized, "link already used or password changed")
		return
	}

	if err := h.store.Users().Enable(ctx, user.ID); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to re-enable account")
		return
	}

	jsonResponse(w, http.StatusOK, map[string]string{"status": "account re-enabled"})
}
