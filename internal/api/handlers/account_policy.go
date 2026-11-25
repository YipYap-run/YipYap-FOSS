package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Error codes the SPA branches on for account input problems.
const (
	codeInvalidEmail   = "invalid_email"
	codeEmailTaken     = "email_taken"
	codePasswordPolicy = "password_policy"
	// codeEmailUnchanged: the requested address is already the account's.
	codeEmailUnchanged = "email_unchanged"
	// codeEmailChangeCooldown: a confirmation link was sent less than
	// emailChangeCooldown ago.
	codeEmailChangeCooldown = "email_change_cooldown"
)

// emailTakenMessage is the copy for a sign-up with an address that already
// has an account (auth-7).
const emailTakenMessage = "An account with this email already exists. Sign in or reset your password."

// normalizedEmail normalizes s and validates it. On failure it writes a 400
// and returns ok=false.
func normalizedEmail(w http.ResponseWriter, s string) (string, bool) {
	email := domain.NormalizeEmail(s)
	if err := domain.ValidateEmail(email); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]string{
			"error": err.Error(),
			"code":  codeInvalidEmail,
		})
		return "", false
	}
	return email, true
}

// checkPasswordPolicy applies auth.ValidatePassword. On failure it writes a
// 400 and returns false.
func checkPasswordPolicy(w http.ResponseWriter, pw string) bool {
	if err := auth.ValidatePassword(pw); err != nil {
		jsonResponse(w, http.StatusBadRequest, map[string]interface{}{
			"error":      err.Error(),
			"code":       codePasswordPolicy,
			"min_length": auth.MinPasswordLength,
			"max_length": auth.MaxPasswordBytes,
		})
		return false
	}
	return true
}

// emailInUse reports whether any user other than exceptID owns email.
func emailInUse(ctx context.Context, s store.Store, email, exceptID string) bool {
	u, err := s.Users().GetByEmail(ctx, email)
	return err == nil && u != nil && u.ID != exceptID
}

// randomHex returns n random bytes hex encoded.
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
