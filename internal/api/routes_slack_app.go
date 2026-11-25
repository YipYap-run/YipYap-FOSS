package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// The platform Slack app is a hosted service; self-hosted builds use
// bring-your-own Slack bots only. The interactivity endpoint keeps the
// signing-secret handler for operators who point their own app at it.
type slackAppRoutes struct {
	actions *handlers.SlackInteractivityHandler
}

func newSlackAppRoutes(s store.Store, msgBus bus.Bus, _ *auth.JWTIssuer, opt ServerOptions, _ string) slackAppRoutes {
	return slackAppRoutes{actions: handlers.NewSlackInteractivityHandler(s, msgBus, opt.SlackSigningSecret)}
}

func (sr slackAppRoutes) public(r chi.Router) {
	r.Post("/integrations/slack/actions", sr.actions.Handle)
}

func (slackAppRoutes) authed(chi.Router) {}
