package api

import (
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/httprate"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// registerAckLinkRoutes mounts the public signed acknowledge link page.
// The token in the path is redacted from request logs by
// middleware.RedactSecretPaths.
func registerAckLinkRoutes(r chi.Router, s store.Store, jwt *auth.JWTIssuer, b bus.Bus, publicBaseURL string) {
	h := handlers.NewAckLinkHandler(s, jwt, b, publicBaseURL)
	r.Group(func(r chi.Router) {
		r.Use(httprate.LimitBy(30, time.Minute, handlers.RateLimitKeyByClientIP))
		r.Get("/ack/{token}", h.Show)
		r.Post("/ack/{token}", h.Confirm)
	})
}
