package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func registerProRoutes(r chi.Router, s store.Store, _ *auth.JWTIssuer, authMW func(http.Handler) http.Handler, _ bus.Bus, _ interface{}, _ string, _ string, _ interface{}, _ *crypto.Envelope, _ string, _ chi.Router, hasher *auth.APIKeyHasher) {
	// FOSS: teams, schedules, OIDC, and billing are not available.
	// API keys are shared across all editions.
	orgH := handlers.NewOrgHandlerWithHasher(s, hasher)
	r.Group(func(r chi.Router) {
		r.Use(authMW)
		r.Use(middleware.RequireWriteAccess())
		r.Get("/org/api-keys", orgH.ListAPIKeys)
		r.Post("/org/api-keys", orgH.CreateAPIKey)
		r.Delete("/org/api-keys/{id}", orgH.DeleteAPIKey)
	})
}
