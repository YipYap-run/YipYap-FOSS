package api

import (
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
	"github.com/go-chi/httprate"
	"go.opentelemetry.io/otel/metric"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/web"
)

// ServerOptions holds optional configuration for the HTTP server.
type ServerOptions struct {
	TestSender            handlers.TestSender
	RequestLatencyMS      metric.Float64Histogram
	OnMonitorChange       func(kind handlers.MonitorChangeKind, monitorID string)
	RegistrationEnabled   bool
	Envelope              *crypto.Envelope
	OpsToken              string
	Mailer                *mailer.Mailer
	// APIKeyHasher is used to hash API key tokens for storage and lookup.
	// When nil, plain SHA-256 is used for backward compatibility.
	APIKeyHasher          *auth.APIKeyHasher
	DiscordPublicKey      string // hex-encoded Ed25519 public key for Discord webhook verification
	SlackSigningSecret    string // Slack app signing secret for request verification
	TelegramWebhookSecret string // secret token for Telegram webhook verification
	// TrustedProxyNets lists CIDRs from which proxy headers (CF-Connecting-IP,
	// X-Real-IP, X-Forwarded-For) are trusted. When empty, RemoteAddr is used
	// as-is and proxy headers are ignored.
	TrustedProxyNets      []*net.IPNet
}

// NewServer creates a new HTTP handler with all API routes.
// billingClient is an optional *billing.Client passed as interface{} to avoid
// importing the billing package in FOSS builds. billingWebhookSecret is the
// billing webhook signing secret.
func NewServer(s store.Store, jwt *auth.JWTIssuer, msgBus bus.Bus, wsHub *Hub, billingClient interface{}, billingWebhookSecret string, publicBaseURL string, opts ...ServerOptions) http.Handler {
	var opt ServerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// Derive a safe CORS origin from publicBaseURL. Never default to "*".
	corsOrigin := publicBaseURL
	if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
		corsOrigin = u.Scheme + "://" + u.Host
	}

	r := chi.NewRouter()
	root := r // capture root router so registerProRoutes can add top-level routes

	// Global middleware.
	r.Use(trustedProxyRealIP(opt.TrustedProxyNets))
	r.Use(chimw.Logger)
	r.Use(chimw.Recoverer)
	r.Use(middleware.Latency(opt.RequestLatencyMS))
	r.Use(middleware.SecurityHeaders(publicBaseURL))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins: []string{corsOrigin},
		AllowedMethods: []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders: []string{"Accept", "Authorization", "Content-Type"},
		MaxAge:         300,
	}))

	// Handler instances.
	authH := handlers.NewAuthHandlerWithBaseURL(s, jwt, publicBaseURL)
	resetH := handlers.NewPasswordResetHandler(s, jwt, opt.Mailer, publicBaseURL)
	orgH := handlers.NewOrgHandlerWithHasher(s, opt.APIKeyHasher)
	userH := handlers.NewUserHandler(s)
	monitorH := handlers.NewMonitorHandler(s)
	if opt.OnMonitorChange != nil {
		monitorH.SetOnChange(opt.OnMonitorChange)
	}
	alertH := handlers.NewAlertHandler(s)
	escalationH := handlers.NewEscalationHandler(s)
	notifChanH := handlers.NewNotificationChannelHandler(s, opt.TestSender)
	maintenanceH := handlers.NewMaintenanceHandler(s)
	publicH := handlers.NewPublicHandler(s)
	accountDeleteH := handlers.NewAccountDeleteHandler(s, jwt, opt.Mailer, publicBaseURL)

	authMiddleware := middleware.Auth(jwt, s.APIKeys(), opt.APIKeyHasher)

	// WebSocket endpoint  - JWT auth via query param, not middleware.
	if wsHub != nil {
		r.Get("/ws", wsHub.ServeWS(jwt))
	}

	r.Route("/api/v1", func(r chi.Router) {
		// Public routes (no auth).
		r.Get("/meta", handlers.MetaGet(opt.RegistrationEnabled, s))

		// Rate-limited public auth routes.
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitByIP(10, time.Minute))
			if opt.RegistrationEnabled {
				r.Post("/auth/register", authH.Register)
			}
			r.Post("/auth/login", authH.Login)
			r.Post("/auth/forgot-password", resetH.ForgotPassword)
			r.Post("/auth/reset-password", resetH.ResetPassword)
			r.Post("/auth/confirm-delete", accountDeleteH.ConfirmDeletion)
			r.Post("/auth/recover-account", accountDeleteH.RequestRecovery)
			r.Post("/auth/confirm-recover", accountDeleteH.ConfirmRecovery)
		})

		// Logout clears the session cookie and requires no valid token.
		r.Post("/auth/logout", authH.Logout)

		r.Get("/public/{orgSlug}/maintenance", publicH.Maintenance)

		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitByIP(60, time.Minute))
			r.Post("/heartbeat/{monitorID}", monitorH.Heartbeat)
		})

		// Inbound Events API (authenticated by integration key in body).
		eventsH := handlers.NewEventsHandler(s, msgBus)
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitByIP(100, time.Minute))
			r.Post("/events", eventsH.Ingest)
		})

		// Discord interactivity callback (button clicks for ack/resolve).
		discordInteractivityH := handlers.NewDiscordInteractivityHandler(s, msgBus, opt.DiscordPublicKey)
		r.Post("/webhooks/discord", discordInteractivityH.Handle)

		// Twilio voice TwiML endpoints (Pro only, called by Twilio, no auth).
		registerTwilioRoutes(r, s)

		telegramInteractivityH := handlers.NewTelegramInteractivityHandler(s, msgBus, opt.TelegramWebhookSecret)
		r.Post("/webhooks/telegram", telegramInteractivityH.Handle)

		// Slack interactivity callback (button clicks for ack/resolve).
		slackInteractivityH := handlers.NewSlackInteractivityHandler(s, msgBus, opt.SlackSigningSecret)
		r.Post("/integrations/slack/actions", slackInteractivityH.Handle)

		// Authenticated routes.
		r.Group(func(r chi.Router) {
			r.Use(authMiddleware)
			r.Use(middleware.DisabledAccountGuard(s))
			r.Use(middleware.ReadOnlyStaffGuard())
			r.Use(middleware.RequireWriteAccess())
			r.Use(middleware.ForcePasswordChange(s))
			r.Use(middleware.MFAEnforce(s))
			r.Use(middleware.PromoExpiry(s))

			// Auth.
			r.Post("/auth/refresh", authH.Refresh)
			r.Put("/auth/password", authH.ChangePassword)
			r.Put("/auth/email", authH.ChangeEmail)
			r.Post("/auth/delete-account", accountDeleteH.RequestDeletion)

			// Org.
			r.Get("/org", orgH.Get)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner", "admin"))
				r.Patch("/org", orgH.Update)
			})

			// Users (read - any authenticated user).
			r.Get("/users", userH.List)
			r.Get("/users/{id}", userH.Get)

			// Users (write - owner/admin only).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner", "admin"))
				r.Post("/users", userH.Create)
				r.Patch("/users/{id}", userH.Update)
				r.Delete("/users/{id}", userH.Delete)
				r.Post("/users/{id}/reset-password", userH.ResetPassword)
			})

			// Ownership transfer (owner only).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner"))
				r.Post("/org/transfer-ownership", userH.TransferOwnership)
			})

			// Monitors.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("monitors"))
				r.Get("/monitors", monitorH.List)
				r.Post("/monitors", monitorH.Create)
				r.Get("/monitors/check-stats", monitorH.CheckStats)
				r.Get("/monitors/{id}", monitorH.Get)
				r.Patch("/monitors/{id}", monitorH.Update)
				r.Delete("/monitors/{id}", monitorH.Delete)
				r.Post("/monitors/{id}/pause", monitorH.Pause)
				r.Post("/monitors/{id}/resume", monitorH.Resume)
				r.Get("/monitors/{id}/checks", monitorH.ListChecks)
				r.Get("/monitors/{id}/uptime", monitorH.Uptime)
				r.Get("/monitors/{id}/latency", monitorH.Latency)
				r.Put("/monitors/{id}/labels", monitorH.SetLabels)
				r.Delete("/monitors/{id}/labels/{key}", monitorH.DeleteLabel)
			})

			// Alerts.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("alerts"))
				r.Get("/alerts", alertH.List)
				r.Get("/alerts/{id}", alertH.Get)
				r.Post("/alerts/{id}/ack", alertH.Ack)
				r.Post("/alerts/{id}/resolve", alertH.Resolve)
				r.Get("/alerts/{id}/timeline", alertH.Timeline)
			})

			// Escalation Policies.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("escalation_policies"))
				r.Get("/escalation-policies", escalationH.List)
				r.Post("/escalation-policies", escalationH.Create)
				r.Get("/escalation-policies/{id}", escalationH.Get)
				r.Patch("/escalation-policies/{id}", escalationH.Update)
				r.Delete("/escalation-policies/{id}", escalationH.Delete)
				r.Put("/escalation-policies/{id}/steps", escalationH.ReplaceSteps)
			})

			// Notification Channels.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("notification_channels"))
				r.Get("/notification-channels", notifChanH.List)
				r.Post("/notification-channels", notifChanH.Create)
				r.Get("/notification-channels/{id}", notifChanH.Get)
				r.Patch("/notification-channels/{id}", notifChanH.Update)
				r.Delete("/notification-channels/{id}", notifChanH.Delete)
				r.Post("/notification-channels/{id}/test", notifChanH.Test)
			})

			// Maintenance Windows.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("maintenance_windows"))
				r.Get("/maintenance-windows", maintenanceH.List)
				r.Post("/maintenance-windows", maintenanceH.Create)
				r.Get("/maintenance-windows/{id}", maintenanceH.Get)
				r.Patch("/maintenance-windows/{id}", maintenanceH.Update)
				r.Delete("/maintenance-windows/{id}", maintenanceH.Delete)
			})

		})

		// Pro-only routes (teams, schedules, OIDC, API keys, billing).
		registerProRoutes(r, s, jwt, authMiddleware, msgBus, billingClient, billingWebhookSecret, publicBaseURL, userH, opt.Envelope, opt.OpsToken, root, opt.APIKeyHasher)
	})

	// Public status pages (vanity URLs, no auth).
	r.Get("/{orgSlug}/status/{statusSlug}", publicH.StatusPage)

	// Serve embedded SPA. Static assets are served from web/dist and any
	// non-API, non-asset route falls back to index.html for client-side routing.
	distFS, err := fs.Sub(web.Dist, "dist")
	if err == nil {
		staticHandler := http.FileServer(http.FS(distFS))
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			// Serve static files directly if they exist.
			path := strings.TrimPrefix(req.URL.Path, "/")
			if path == "" {
				path = "index.html"
			}
			if f, err := distFS.Open(path); err == nil {
				_ = f.Close()
				staticHandler.ServeHTTP(w, req)
				return
			}
			// SPA fallback: serve index.html for all other routes.
			req.URL.Path = "/"
			staticHandler.ServeHTTP(w, req)
		})
	}

	return r
}

