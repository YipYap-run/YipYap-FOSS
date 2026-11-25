package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The platform Discord app is a hosted service; self-hosted builds use
// bring-your-own Discord bots only. The interactions endpoint keeps the
// public-key handler for operators who point their own app at it.
type discordAppRoutes struct {
	interactions *handlers.DiscordInteractivityHandler
}

func newDiscordAppRoutes(s store.Store, msgBus bus.Bus, _ *auth.JWTIssuer, opt ServerOptions, _ string, _ *handlers.NotificationChannelHandler) discordAppRoutes {
	return discordAppRoutes{interactions: handlers.NewDiscordInteractivityHandler(s, msgBus, opt.DiscordPublicKey)}
}

func (dr discordAppRoutes) public(r chi.Router) {
	r.Post("/webhooks/discord", dr.interactions.Handle)
}

func (discordAppRoutes) authed(chi.Router) {}
