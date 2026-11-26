package api

import (
	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// registerNotificationChannelRoutes mounts the notification channel API and
// the org setting that controls who may change channels. r must already
// carry the auth middleware.
func registerNotificationChannelRoutes(r chi.Router, s store.Store, notifChanH *handlers.NotificationChannelHandler, opt ServerOptions) {
	permH := handlers.NewNotificationPermissionsHandler(s)

	// Which roles may manage and test channels. Any member may read it so
	// the SPA can show the right controls; owners and admins change it.
	r.Get("/org/notification-permissions", permH.Get)
	r.With(middleware.RequireRole("owner", "admin")).Put("/org/notification-permissions", permH.Update)

	// How many failed deliveries in a row make the notifier email the
	// org owners about a channel.
	failH := handlers.NewChannelFailureAlertsHandler(s)
	failH.SetEmailEnabled(notifChanH.PlatformEmail())
	r.Get("/org/channel-failure-alerts", failH.Get)
	r.With(middleware.RequireRole("owner", "admin")).Put("/org/channel-failure-alerts", failH.Update)

	r.Group(func(r chi.Router) {
		r.Use(middleware.RequireScope("notification_channels"))
		r.Get("/notification-channels", notifChanH.List)
		r.Get("/notification-channels/capabilities", notifChanH.Capabilities)
		r.Get("/notification-channels/{id}", notifChanH.Get)

		// Channel config carries delivery targets and credentials. The org
		// decides which roles may change it or send a test (see
		// domain.DefaultChannelPermissions). API keys pass on scope.
		manage := middleware.RequireChannelPermission(s, domain.ChannelActionManage)
		r.With(manage).Post("/notification-channels", notifChanH.Create)
		r.With(manage).Patch("/notification-channels/{id}", notifChanH.Update)
		r.With(manage).Delete("/notification-channels/{id}", notifChanH.Delete)
		r.With(middleware.RequireChannelPermission(s, domain.ChannelActionTest)).Post("/notification-channels/{id}/test", notifChanH.Test)
		r.With(manage).Post("/notification-channels/{id}/rotate-secret", notifChanH.RotateSecret)
		registerPhoneVerifyRoutes(r, s, opt.PhoneVerifySender)
		// Telegram chats are linked with a one-time code (notif-16). Any
		// member may link their own contact method, so the handler applies
		// the channel permission to channel codes itself.
		r.Post("/telegram/link-codes", notifChanH.CreateTelegramLinkCode)
		r.Get("/telegram/link-codes/{id}", notifChanH.GetTelegramLinkCode)
	})
}
