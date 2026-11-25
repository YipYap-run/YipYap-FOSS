package handlers

import (
	"context"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The FOSS edition has no SSO, so no sign-in is ever refused for it.

func ssoLoginGate(_ http.ResponseWriter, _ *http.Request, _ store.Store, _ *auth.JWTIssuer, _ *domain.User, _ bool) bool {
	return false
}

func ssoResetBlocked(_ context.Context, _ store.Store, _ *domain.User) bool { return false }

func ssoSessionBlocked(_ context.Context, _ store.Store, _ *domain.User, _ string) bool {
	return false
}
