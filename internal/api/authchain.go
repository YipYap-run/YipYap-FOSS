package api

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// authedChain composes the middleware every authenticated user route runs
// behind, in order: authentication, the disabled-account guard, SaaS extras
// (staff read-only guard), the write-access check, forced password change,
// org MFA enforcement and promo expiry. The core group and every group in
// registerProRoutes use this one chain so no route group can drift.
func authedChain(s store.Store, authMW func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return authedChainWithWriteAccess(s, authMW, true)
}

// authedChainAllowReadOnlyWrite is authedChain without the write-access
// check, for the one write-shaped route that only moves a read position (the
// CloudEvents ack). Every other gate still applies.
func authedChainAllowReadOnlyWrite(s store.Store, authMW func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return authedChainWithWriteAccess(s, authMW, false)
}

func authedChainWithWriteAccess(s store.Store, authMW func(http.Handler) http.Handler, writeAccess bool) func(http.Handler) http.Handler {
	mws := []func(http.Handler) http.Handler{authMW, middleware.DisabledAccountGuard(s)}
	mws = append(mws, saasAuthExtras()...)
	mws = append(mws, middleware.APIKeyScopeGuard())
	if writeAccess {
		mws = append(mws, middleware.RequireWriteAccess())
	}
	mws = append(mws,
		middleware.ForcePasswordChange(s),
		middleware.MFAEnforce(s),
		middleware.PromoExpiry(s),
	)
	return func(next http.Handler) http.Handler {
		for i := len(mws) - 1; i >= 0; i-- {
			next = mws[i](next)
		}
		return next
	}
}
