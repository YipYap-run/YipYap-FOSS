package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	replyhandlers "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
	"github.com/YipYap-run/YipYap-FOSS/internal/jobs"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/seed"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "yipyap: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	// Validate required config.
	if cfg.JWTSecret == "" {
		log.Fatal("YIPYAP_JWT_SECRET is required")
	}

	if cfg.RegistrationEnabled {
		log.Println("WARNING: Public registration is enabled. Set YIPYAP_REGISTRATION_ENABLED=false to disable.")
	}

	checker.AllowPrivateTargets = cfg.AllowPrivateTargets
	checker.AllowPrivateControlPlaneTargets = cfg.AllowPrivateControlPlaneTargets
	if cfg.AllowPrivateTargets {
		log.Println("SSRF protection: private/internal MONITOR targets ALLOWED (self-hosted mode)")
	}
	if cfg.AllowPrivateControlPlaneTargets {
		log.Println("SSRF protection: private/internal CONTROL-PLANE targets ALLOWED (sink/OIDC URLs accept localhost + RFC1918)")
	}

	ctx := context.Background()

	// 1. Database.
	db, err := openStore(cfg)
	if err != nil {
		return fmt.Errorf("database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.Migrate(ctx); err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	log.Printf("database ready (driver: %s)", cfg.DBDriver)

	// Dev seed.
	if cfg.DevSeed {
		if err := seed.Run(ctx, db); err != nil {
			return fmt.Errorf("dev seed: %w", err)
		}
	}

	// 2. Auth.
	jwt := auth.NewJWTIssuer([]byte(cfg.JWTSecret), cfg.JWTExpiry)
	apiKeySecret := cfg.APIKeySecret
	if apiKeySecret == "" {
		apiKeySecret = cfg.JWTSecret
		log.Println("WARNING: YIPYAP_API_KEY_SECRET is not set; falling back to JWT secret for API key HMAC. Set a dedicated secret for better key isolation.")
	}
	apiKeyHasher := auth.NewAPIKeyHasher([]byte(apiKeySecret))

	// 3. Message bus.
	msgBus, err := openBus(cfg)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}
	log.Println("message bus ready")
	defer func() { _ = msgBus.Close() }()

	// 4. Check scheduler.
	sched := checker.NewScheduler(db, msgBus, checker.CheckerConfig{
		Workers:           cfg.CheckerWorkers,
		ChannelSize:       cfg.CheckerChannelSize,
		PriorityThreshold: cfg.CheckerPriorityThreshold,
		BatchSize:         cfg.CheckerBatchSize,
		BatchWriters:      cfg.CheckerBatchWriters,
		FlushConcurrency:  cfg.CheckerFlushConcurrency,
	})

	// 5. Envelope encryption for notification configs.
	var envelope *crypto.Envelope
	if cfg.NotificationKey != "" {
		envelope, err = crypto.NewEnvelopeFromHex(cfg.NotificationKey)
		if err != nil {
			return fmt.Errorf("notification key: %w", err)
		}
		log.Println("notification config encryption enabled")
	}

	if envelope == nil {
		log.Println("WARNING: YIPYAP_NOTIFICATION_KEY is not set. Notification channel secrets (Slack tokens, SMTP passwords, etc.) will be stored as PLAINTEXT in the database. Set YIPYAP_NOTIFICATION_KEY to a 32-byte hex key for AES-256 encryption.")
	}

	// 6. Escalation engine.
	engine := escalation.NewEngine(db, msgBus, envelope, nil)

	// 7. Notification dispatcher.
	var decrypt notify.DecryptFunc
	if envelope != nil {
		decrypt = envelope.Decrypt
	}
	dispatcher, cehttpProvider := setupDispatcher(msgBus, db, decrypt, cfg)

	// Phase-2 reply pipeline: outbound registry → dispatcher → handlers.
	// The cehttp provider records every outbound event into the registry
	// and routes sink-replies through the dispatcher for persistence.
	replyRegistry := reply.NewMemoryRegistry(10000, 24*time.Hour)
	// R2-H3: surface dispatcher-level rejections (org/channel mismatch,
	// trust-flag missing, rate-limited, unknown alertref) on the admin
	// reply-activity dashboard. Without this audit hook those denials
	// only emit slog warnings and the dashboard query silently drops
	// the most security-relevant attempts.
	replyDispatcher := reply.NewDispatcher(replyRegistry,
		reply.WithRejectAudit(func(ctx context.Context, a reply.RejectAudit) {
			entry := &store.CloudEventReplyAuditEntry{
				ReplyID:     a.ReplyID,
				ReplyType:   a.ReplyType,
				ReplySource: a.ReplySource,
				AlertID:     a.AlertID,
				MonitorID:   a.MonitorID,
				OrgID:       a.OrgID,
				ChannelID:   a.ChannelID,
				Outcome:     a.Outcome,
				Before:      []byte(`{}`),
				After:       []byte(`{}`),
				ReceivedAt:  time.Now().UTC(),
			}
			if err := db.CloudEventReplyAudit().Write(ctx, entry); err != nil {
				slog.Warn("reply audit write failed", "outcome", a.Outcome, "error", err)
			}
		}),
	)
	replyhandlers.RegisterAll(replyDispatcher, db)
	cehttpProvider.SetOutboundRegistry(replyRegistry)
	cehttpProvider.SetReplyDispatcher(replyDispatcher)

	// 8. WebSocket hub.
	wsHub := api.NewHub(cfg.PublicBaseURL)
	if err := wsHub.SubscribeToBus(msgBus); err != nil {
		return fmt.Errorf("ws hub subscribe: %w", err)
	}

	// 9. HTTP server.
	mail := mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPass,
		From:     cfg.SMTPFrom,
	})
	billingClient, billingWebhookSecret := initBilling(cfg)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	srv := api.NewServer(db, jwt, msgBus, wsHub, billingClient, billingWebhookSecret, cfg.PublicBaseURL, api.ServerOptions{
		TestSender:            dispatcher,
		RegistrationEnabled:   cfg.RegistrationEnabled,
		Envelope:              envelope,
		OpsToken:              cfg.OpsToken,
		Mailer:                mail,
		APIKeyHasher:          apiKeyHasher,
		DiscordPublicKey:      cfg.DiscordPublicKey,
		SlackSigningSecret:    cfg.SlackSigningSecret,
		TelegramWebhookSecret: cfg.TelegramWebhookSecret,
		TelegramBotToken:      cfg.TelegramBotToken,
		TrustedProxyNets:      api.ParseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs),
		LifecycleCtx:          runCtx,
	})
	httpSrv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 9. Start everything (runCtx defined above when constructing the
	// HTTP server so the API layer can wire long-lived components into
	// the same lifecycle).

	if err := sched.Start(runCtx); err != nil {
		return fmt.Errorf("scheduler start: %w", err)
	}
	engine.Start(runCtx)
	if err := dispatcher.Start(runCtx); err != nil {
		return fmt.Errorf("dispatcher start: %w", err)
	}

	// CloudEvent router: bridges events.cloudevents.out.> onto notify.request
	// for each cloudevent_http channel so the dispatcher can fan them out.
	ceRouter := cloudevents.NewRouter(msgBus, db, db.Outbox(), envelope)
	if err := ceRouter.Start(runCtx); err != nil {
		return fmt.Errorf("cloudevent router start: %w", err)
	}

	jobs.StartRetentionPruner(runCtx, db)
	jobs.StartAccountCleanup(runCtx, db)
	jobs.StartRollupAggregator(runCtx, db)

	// 10. Graceful shutdown.
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		// A4-N-2: tear down the long-lived dispatcher sweepers so they
		// don't outlive the parent process. ceConsumerCache.Close is
		// invoked via the LifecycleCtx wiring in api.NewServer.
		replyDispatcher.Close()
	}()

	log.Printf("yipyap listening on %s", cfg.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}

	// Wait for background services to stop.
	sched.Stop()
	engine.Stop()
	dispatcher.Stop()

	log.Println("yipyap stopped")
	return nil
}
