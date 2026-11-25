package handlers

import (
	"context"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// beginSecondFactorLogin runs the password login through org SSO policy.
// FOSS has no personal second factor, so an accepted login always continues
// as a normal password session.
func (h *AuthHandler) beginSecondFactorLogin(w http.ResponseWriter, r *http.Request, user *domain.User) (bool, error) {
	return ssoLoginGate(w, r, h.store, h.jwt, user, true), nil
}

// verifyMFAIfEnabled is a no-op in FOSS builds.
func verifyMFAIfEnabled(_ context.Context, _ store.Store, _ *domain.User, _ mfaProof) error {
	return nil
}
