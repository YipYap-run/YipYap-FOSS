package api

import (
	"context"
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
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/session"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
	"github.com/YipYap-run/YipYap-FOSS/web"
)

// opsAuth carries the internal ops API credentials: the bearer token and
// the optional key for signed actor assertions.
type opsAuth struct {
	Token    string
	ActorKey string
}

// ServerOptions holds optional configuration for the HTTP server.
type ServerOptions struct {
	TestSender          handlers.TestSender
	RequestLatencyMS    metric.Float64Histogram
	OnMonitorChange     func(kind handlers.MonitorChangeKind, monitorID string)
	RegistrationEnabled bool
	Envelope            *crypto.Envelope
	OpsToken            string
	// OpsActorKey is the shared HMAC key for signed ops actor assertions.
	OpsActorKey string
	Mailer      *mailer.Mailer
	// APIKeyHasher is used to hash API key tokens for storage and lookup.
	// When nil, plain SHA-256 is used for backward compatibility.
	APIKeyHasher *auth.APIKeyHasher
	// APIKeyMaxTTL bounds expires_at on new API keys. Zero uses
	// handlers.DefaultAPIKeyMaxTTL.
	APIKeyMaxTTL          time.Duration
	DiscordPublicKey      string // hex-encoded Ed25519 public key for Discord webhook verification
	SlackSigningSecret    string // Slack app signing secret for request verification
	SlackClientID         string // platform Slack app OAuth client ID (SaaS)
	SlackClientSecret     string // platform Slack app OAuth client secret (SaaS)
	DiscordClientID       string // platform Discord app application ID (SaaS)
	DiscordClientSecret   string // platform Discord app OAuth client secret (SaaS)
	DiscordBotToken       string // platform Discord bot token (SaaS)
	TelegramWebhookSecret string // secret token for Telegram webhook verification
	TelegramBotToken      string // platform Telegram bot API token for editing messages on ack/resolve
	TelegramBotUsername   string // public bot username for UI help
	TwilioAuthToken       string // Twilio account auth token for voice callback signature verification
	TelnyxWebhookSecret   string // HMAC secret for Telnyx TeXML callback URLs
	// PhoneVerifySender sends OTP codes that activate SMS and voice
	// channels. Nil disables verification (the endpoints answer 503).
	PhoneVerifySender handlers.PhoneCodeSender
	// TrustedProxyNets lists CIDRs from which proxy headers (X-Forwarded-For,
	// X-Real-IP) are trusted. CF-Connecting-IP is additionally limited to
	// peers in Cloudflare's published ranges. When empty, RemoteAddr is used
	// as-is and proxy headers are ignored.
	TrustedProxyNets []*net.IPNet
	// Metrics is the centralised OTEL metrics bundle. When non-nil it is
	// wired into handlers that support SetMetrics.
	Metrics *telemetry.Metrics
	// JWKSHandler, when non-nil, is mounted at /.well-known/jwks.json in
	// non-FOSS builds. FOSS builds ignore it.
	JWKSHandler http.Handler
	// LifecycleCtx, when non-nil, is observed by long-lived components
	// so they can release resources
	// when the parent process is shutting down. main.go passes the same
	// context it cancels in its SIGTERM handler. A4-N-2.
	LifecycleCtx context.Context
	// OTELExportSync applies an org's OTLP export settings on this
	// instance right after they change. Nil means per-org export is not
	// running here, and the settings API reports export_active=false.
	OTELExportSync func(ctx context.Context, orgID string) error
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
	handlers.SetAPIKeyMaxTTL(opt.APIKeyMaxTTL)

	// Derive a safe CORS origin from publicBaseURL. Never default to "*".
	corsOrigin := publicBaseURL
	if u, err := url.Parse(publicBaseURL); err == nil && u.Host != "" {
		corsOrigin = u.Scheme + "://" + u.Host
	}

	r := chi.NewRouter()
	root := r // capture root router so registerProRoutes can add top-level routes

	// Global middleware.
	r.Use(trustedProxyRealIP(opt.TrustedProxyNets))
	// Structured access log by route pattern. chimw.Logger wrote the full
	// RequestURI, which leaked ingest, heartbeat and survey tokens. (core-5)
	r.Use(middleware.RequestLog(nil))
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
	authH := handlers.NewAuthHandlerFull(s, jwt, opt.Mailer, publicBaseURL)
	resetH := handlers.NewPasswordResetHandler(s, jwt, opt.Mailer, publicBaseURL)
	orgH := handlers.NewOrgHandlerWithHasher(s, opt.APIKeyHasher)
	userH := handlers.NewUserHandler(s)
	var inviteMailer handlers.InviteMailer
	if opt.Mailer != nil {
		inviteMailer = opt.Mailer
	}
	userH.SetInviteDelivery(inviteMailer, publicBaseURL)
	userH.SetSessionStarter(authH.StartSession)
	monitorH := handlers.NewMonitorHandler(s)
	if opt.OnMonitorChange != nil {
		monitorH.SetOnChange(opt.OnMonitorChange)
	}
	monitorGroupH := handlers.NewMonitorGroupHandler(s)
	monitorStateH := handlers.NewMonitorStateHandler(s)
	alertH := handlers.NewAlertHandler(s)
	alertH.SetBus(msgBus)
	if opt.Envelope != nil {
		alertH.SetContactMethodCipher(contactmethod.Cipher{Enc: opt.Envelope.Encrypt, Dec: opt.Envelope.Decrypt})
	}
	escalationH := handlers.NewEscalationHandler(s)
	notifChanH := handlers.NewNotificationChannelHandler(s, opt.TestSender)
	notifChanH.SetTelegramBot(opt.TelegramBotUsername, opt.TelegramWebhookSecret != "" && opt.TelegramBotToken != "")
	notifChanH.SetPlatformEmail(opt.Mailer != nil)
	if opt.Mailer != nil {
		notifChanH.SetChannelAlerts(notify.NewChannelAlerts(s, opt.Mailer, publicBaseURL))
	}
	maintenanceH := handlers.NewMaintenanceHandler(s)
	publicH := handlers.NewPublicHandler(s)
	bulkImportH := handlers.NewBulkImportHandler(s, opt.Mailer != nil)
	if opt.OnMonitorChange != nil {
		bulkImportH.SetOnChange(opt.OnMonitorChange)
	}
	accountDeleteH := handlers.NewAccountDeleteHandler(s, jwt, opt.Mailer, publicBaseURL)
	sessionH := handlers.NewSessionHandler(s)
	contactH := newContactMethodHandler(s, opt)

	authMiddleware := middleware.Auth(jwt, s.APIKeys(), opt.APIKeyHasher, session.Checker(s))
	// Full chain for user-facing authenticated routes (auth-2).
	authed := authedChain(s, authMiddleware)
	authedAllowReadOnlyWrite := authedChainAllowReadOnlyWrite(s, authMiddleware)

	// WebSocket endpoint: session cookie auth plus the account gates, inside
	// ServeWS, with the server-side session re-checked while open.
	if wsHub != nil {
		r.Get("/ws", wsHub.ServeWS(jwt, s, session.Checker(s)))
	}

	r.Route("/api/v1", func(r chi.Router) {
		// Public route with optional auth: anonymous callers get the
		// public fields, signed-in callers also get plan, promo and
		// retention. PromoExpiry runs so an org past its promo grace sees
		// its Free plan here on the same request.
		r.With(
			middleware.OptionalAuth(jwt, s.APIKeys(), opt.APIKeyHasher, s.Users()),
			middleware.PromoExpiry(s),
		).Get("/meta", handlers.MetaGet(opt.RegistrationEnabled, s, opt.TelegramBotUsername, billingClient != nil && billingWebhookSecret != ""))

		// Rate-limited public auth routes.
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitBy(10, time.Minute, handlers.RateLimitKeyByClientIP))
			if opt.RegistrationEnabled {
				r.Post("/auth/register", authH.Register)
			}
			r.Post("/auth/login", authH.Login)
			r.Post("/auth/verify-email", authH.VerifyEmail)
			r.Post("/auth/resend-verification", authH.ResendVerification)
			r.Post("/auth/forgot-password", resetH.ForgotPassword)
			r.Post("/auth/reset-password", resetH.ResetPassword)
			r.Post("/auth/confirm-delete", accountDeleteH.ConfirmDeletion)
			r.Post("/auth/recover-account", accountDeleteH.RequestRecovery)
			r.Post("/auth/confirm-recover", accountDeleteH.ConfirmRecovery)
			r.Post("/auth/confirm-email-change", authH.ConfirmEmailChange)
			r.Post("/auth/cancel-email-change", authH.CancelEmailChange)
		})

		// Member invite accept (public, the token is in the link). Its own
		// rate-limit bucket, so opening the accept page does not use up the
		// sign-in allowance.
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitBy(20, time.Minute, handlers.RateLimitKeyByClientIP))
			r.Get("/invites/accept", userH.GetInviteForAccept)
			r.Post("/invites/accept", userH.AcceptInvite)
		})

		registerSaaSPreAuthRoutes(r, authH)

		// Logout revokes the session and clears the cookie. It requires no
		// valid token.
		r.Post("/auth/logout", authH.Logout)

		r.Get("/public/{orgSlug}/maintenance", publicH.Maintenance)

		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitBy(60, time.Minute, handlers.RateLimitKeyByClientIP))
			r.Post("/heartbeat/{monitorID}", monitorH.Heartbeat)
		})

		// Inbound Events API (authenticated by integration key in body).
		eventsH := handlers.NewEventsHandler(s, msgBus)
		r.Group(func(r chi.Router) {
			r.Use(httprate.LimitBy(100, time.Minute, handlers.RateLimitKeyByClientIP))
			r.Post("/events", eventsH.Ingest)
		})

		// Event streaming routes (registered by build-tagged helpers).
		registerEventingRoutes(r, s, msgBus, authed, authedAllowReadOnlyWrite, opt)

		// Discord: OAuth callback and button clicks. SaaS serves the
		// platform app; FOSS keeps the public-key click handler.
		discordApp := newDiscordAppRoutes(s, msgBus, jwt, opt, publicBaseURL, notifChanH)
		discordApp.public(r)

		// Pro-only voice callback routes (registered by build-tagged helpers).
		registerTwilioRoutes(r, s, msgBus, publicBaseURL, opt.TwilioAuthToken)
		registerTelnyxRoutes(r, s, msgBus, publicBaseURL, opt.TelnyxWebhookSecret)

		telegramInteractivityH := handlers.NewTelegramInteractivityHandler(s, msgBus, opt.TelegramWebhookSecret, opt.TelegramBotToken)
		telegramInteractivityH.SetBotUsername(opt.TelegramBotUsername)
		r.Post("/webhooks/telegram", telegramInteractivityH.Handle)

		// Slack: OAuth callback, Events API and button clicks. SaaS serves
		// the platform app; FOSS keeps the signing-secret click handler.
		slackApp := newSlackAppRoutes(s, msgBus, jwt, opt, publicBaseURL)
		slackApp.public(r)

		// Authenticated routes.
		r.Group(func(r chi.Router) {
			r.Use(authed)

			registerSaaSAuthRoutes(r, s)
			slackApp.authed(r)
			discordApp.authed(r)

			// Auth. Session only: an API key must never mint a session or
			// change its creator's credentials.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireSession())
				r.Post("/auth/refresh", authH.Refresh)
				r.Put("/auth/password", authH.ChangePassword)
				r.Put("/auth/email", authH.ChangeEmail)
				r.Delete("/auth/email/pending", authH.CancelPendingEmail)
				r.Post("/auth/delete-account", accountDeleteH.RequestDeletion)
				r.Get("/auth/sessions", sessionH.List)
				r.Delete("/auth/sessions/{id}", sessionH.Revoke)
				r.Post("/auth/sessions/revoke-others", sessionH.RevokeOthers)
			})

			// Org.
			r.With(middleware.RequireScope("org")).Get("/org", orgH.Get)
			r.With(middleware.RequireScope("org")).Get("/org/settings", orgH.GetSettings)
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner", "admin"))
				r.Patch("/org", orgH.Update)
				r.Put("/org/settings", orgH.SetSettings)
			})

			// Users (read - any authenticated user).
			r.With(middleware.RequireScope("users")).Get("/users", userH.List)
			r.With(middleware.RequireScope("users")).Get("/users/{id}", userH.Get)

			// Users (write - owner/admin only).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner", "admin"))
				r.Post("/users", userH.LegacyCreate)
				r.Get("/users/invites", userH.ListInvites)
				r.Post("/users/invites", userH.CreateInvite)
				r.Post("/users/invites/{id}/resend", userH.ResendInvite)
				r.Delete("/users/invites/{id}", userH.RevokeInvite)
				r.Patch("/users/{id}", userH.Update)
				r.Delete("/users/{id}", userH.Delete)
				r.Get("/users/{id}/api-keys", userH.ListAPIKeys)
				r.Post("/users/{id}/reset-password", userH.ResetPassword)
			})

			// Personal contact methods and notification rules.
			contactH.Mount(r)

			// Ownership transfer (owner only).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner"))
				r.Post("/org/transfer-ownership", userH.TransferOwnership)
			})

			// Monitors.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("monitors"))
				r.Use(middleware.RequireWriterRole())
				r.Get("/monitors", monitorH.List)
				r.Post("/monitors", monitorH.Create)
				r.Get("/monitors/check-stats", monitorH.CheckStats)
				r.Get("/monitors/{id}", monitorH.Get)
				r.Patch("/monitors/{id}", monitorH.Update)
				r.Delete("/monitors/{id}", monitorH.Delete)
				r.Post("/monitors/{id}/pause", monitorH.Pause)
				r.Post("/monitors/{id}/resume", monitorH.Resume)
				r.Post("/monitors/{id}/mute", monitorH.Mute)
				r.Post("/monitors/{id}/unmute", monitorH.Unmute)
				r.Get("/monitors/{id}/checks", monitorH.ListChecks)
				r.Get("/monitors/{id}/uptime", monitorH.Uptime)
				r.Get("/monitors/{id}/latency", monitorH.Latency)
				r.Put("/monitors/{id}/labels", monitorH.SetLabels)
				r.Delete("/monitors/{id}/labels/{key}", monitorH.DeleteLabel)
			})

			// Bulk import (owner/admin/member; it only creates resources a
			// member can create one by one).
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireRole("owner", "admin", "member"))
				r.Post("/import", bulkImportH.Import)
			})

			registerEventingAdminRoutes(r, s)

			// Monitor Groups.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("monitors"))
				r.Get("/monitor-groups", monitorGroupH.List)
				r.Get("/monitor-groups/{id}", monitorGroupH.Get)
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("owner", "admin", "member"))
					r.Post("/monitor-groups", monitorGroupH.Create)
					r.Patch("/monitor-groups/{id}", monitorGroupH.Update)
					r.Delete("/monitor-groups/{id}", monitorGroupH.Delete)
				})
			})

			// Monitor States.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("monitors"))
				r.Get("/monitor-states", monitorStateH.List)
				r.Get("/monitors/{id}/rules", monitorStateH.ListRules)
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("owner", "admin", "member"))
					r.Post("/monitor-states", monitorStateH.Create)
					r.Patch("/monitor-states/{id}", monitorStateH.Update)
					r.Delete("/monitor-states/{id}", monitorStateH.Delete)
				})
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("owner", "admin", "member"))
					r.Put("/monitors/{id}/rules", monitorStateH.ReplaceRules)
				})
			})

			// Alerts.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("alerts"))
				r.Get("/alerts", alertH.List)
				r.Get("/alerts/{id}", alertH.Get)
				// Viewers may ack alerts paged to them; the handler checks.
				r.Post("/alerts/{id}/ack", alertH.Ack)
				r.Get("/alerts/{id}/timeline", alertH.Timeline)
				r.With(middleware.RequireWriterRole()).Post("/alerts/{id}/resolve", alertH.Resolve)
			})

			// Escalation Policies.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("escalation_policies"))
				r.Use(middleware.RequireWriterRole())
				r.Get("/escalation-policies", escalationH.List)
				r.Get("/escalation-policies/{id}", escalationH.Get)
				// Policies decide who gets paged, so viewers and members
				// only read them.
				r.Group(func(r chi.Router) {
					r.Use(middleware.RequireRole("owner", "admin", "api_key"))
					r.Post("/escalation-policies", escalationH.Create)
					r.Patch("/escalation-policies/{id}", escalationH.Update)
					r.Delete("/escalation-policies/{id}", escalationH.Delete)
					r.Put("/escalation-policies/{id}/steps", escalationH.ReplaceSteps)
				})
			})

			registerNotificationChannelRoutes(r, s, notifChanH, opt)

			// Maintenance Windows.
			r.Group(func(r chi.Router) {
				r.Use(middleware.RequireScope("maintenance_windows"))
				r.Use(middleware.RequireWriterRole())
				r.Get("/maintenance-windows", maintenanceH.List)
				r.Post("/maintenance-windows", maintenanceH.Create)
				r.Get("/maintenance-windows/{id}", maintenanceH.Get)
				r.Patch("/maintenance-windows/{id}", maintenanceH.Update)
				r.Delete("/maintenance-windows/{id}", maintenanceH.Delete)
			})

		})

		// Pro-only routes (teams, schedules, OIDC, API keys, billing).
		registerProRoutes(r, s, jwt, authed, msgBus, billingClient, billingWebhookSecret, publicBaseURL, userH, opt.Envelope, opsAuth{Token: opt.OpsToken, ActorKey: opt.OpsActorKey}, root, opt.APIKeyHasher, opt.Mailer, opt.TestSender, opt.OTELExportSync)
	})

	// JWKS endpoint, mounted before the SPA catch-all so that
	// /.well-known/jwks.json is routable. FOSS's MountJWKS is a no-op.
	MountJWKS(r, opt.JWKSHandler)

	// Signed acknowledge links from notifications (no login).
	registerAckLinkRoutes(r, s, jwt, msgBus, publicBaseURL)

	// Public status pages (vanity URLs, no auth).
	r.Get("/{orgSlug}/status/{statusSlug}", publicH.StatusPage)

	// Serve embedded SPA. Static assets are served from web/dist and any
	// non-API, non-asset route falls back to index.html for client-side routing.
	distFS, err := fs.Sub(web.Dist, "dist")
	if err == nil {
		staticHandler := http.FileServer(http.FS(distFS))
		r.Get("/*", func(w http.ResponseWriter, req *http.Request) {
			if target, ok := handlers.LegacySurveyRedirect(req); ok {
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Referrer-Policy", "no-referrer")
				http.Redirect(w, req, target, http.StatusFound)
				return
			}
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
