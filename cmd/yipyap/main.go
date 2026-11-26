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
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
	"github.com/YipYap-run/YipYap-FOSS/internal/jobs"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/maintenance"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/promo"
	"github.com/YipYap-run/YipYap-FOSS/internal/seed"
	"github.com/YipYap-run/YipYap-FOSS/internal/support"
	"github.com/YipYap-run/YipYap-FOSS/internal/wiring"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "yipyap: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg := config.Load()

	// Resolve the JWT signing secret. When YIPYAP_JWT_SECRET is unset, a
	// random one is generated once and kept in the data dir. Placeholder or
	// short values are refused. The secret itself is never logged.
	jwtSecret, jwtSrc, jwtPath, err := config.ResolveJWTSecret(config.JWTSecretOptions{
		Configured:    cfg.JWTSecret,
		DataDir:       cfg.ResolveDataDir(),
		AllowGenerate: true,
	})
	if err != nil {
		return err
	}
	cfg.JWTSecret = jwtSecret
	switch jwtSrc {
	case config.JWTSecretFromGenerated:
		log.Printf("%s is not set; generated a JWT secret and stored it in %s. Back up this file: deleting it signs everyone out.", config.JWTSecretEnv, jwtPath)
	case config.JWTSecretFromFile:
		log.Printf("%s is not set; using the JWT secret stored in %s", config.JWTSecretEnv, jwtPath)
	}

	if err := setupOperatorCaps(); err != nil {
		return err
	}

	if cfg.RegistrationEnabled {
		log.Println("WARNING: Public registration is enabled. Set YIPYAP_REGISTRATION_ENABLED=false to disable.")
	}

	applyOperatorLimits(cfg)

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
	db, err := wiring.OpenStore(cfg)
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
	apiKeySecret, apiKeyFallback := cfg.APIKeyHMACSecret()
	if apiKeyFallback {
		log.Println("WARNING: YIPYAP_API_KEY_SECRET is not set; falling back to JWT secret for API key HMAC. Set a dedicated secret for better key isolation.")
	}
	apiKeyHasher := auth.NewAPIKeyHasher([]byte(apiKeySecret))
	setupMFA([]byte(apiKeySecret))

	// 3. Message bus.
	msgBus, err := wiring.OpenBus(cfg)
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

	// Event emitter for monitor, alert and maintenance state egress. Shared
	// by the scheduler, the escalation engine and the maintenance ticker.
	// Nil (a no-op) in the FOSS build.
	evt := newEventing(cfg, msgBus)
	emitter := evt.Emitter()
	sched.SetEventEmitter(emitter)

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
	if envelope != nil {
		// Seal contact method secrets saved in plaintext before sealing
		// existed or while no key was set. Conditional per row, so every
		// replica can run it.
		go func() {
			n, err := contactmethod.SealExisting(ctx, db, envelope.Encrypt)
			if err != nil {
				slog.Error("contact method seal backfill", "sealed", n, "error", err)
			} else if n > 0 {
				slog.Info("contact method seal backfill", "sealed", n)
			}
		}()
	}

	// 5a. Per-org OTLP export (SaaS build only; a no-op in FOSS).
	orgRec, otelSync, stopOrgOTEL := startOrgOTEL(db, envelope)
	defer stopOrgOTEL()
	if orgRec != nil {
		sched.SetOrgRecorder(orgRec)
	}

	// 6. Escalation engine.
	engine := escalation.NewEngine(db, msgBus, envelope, nil)
	engine.SetEventEmitter(emitter)
	if orgRec != nil {
		engine.SetOrgRecorder(orgRec)
	}
	// Signed acknowledge links in SMS, email, Pushover, ntfy and webhook
	// payloads. Needs YIPYAP_PUBLIC_BASE_URL; without it no link is sent.
	if ackLinks := escalation.SignedAckLinks(jwt, cfg.PublicBaseURL); ackLinks != nil {
		engine.SetAckLinker(ackLinks)
	} else {
		log.Println("YIPYAP_PUBLIC_BASE_URL is not set: notifications will not carry acknowledge links")
	}

	// 7. Notification dispatcher.
	var decrypt notify.DecryptFunc
	if envelope != nil {
		decrypt = envelope.Decrypt
	}
	mail := mailer.New(mailer.Config{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPass,
		From:     cfg.SMTPFrom,
	})
	// One recorder for sent and failed outcomes. It emails org owners when
	// a channel keeps failing (a no-op without SMTP).
	recorder := notify.NewRecorder(db,
		notify.WithChannelAlerts(notify.NewChannelAlerts(db, mail, cfg.PublicBaseURL)))
	dispatcher := setupDispatcher(msgBus, db, decrypt, cfg, recorder)
	if orgRec != nil {
		dispatcher.SetOrgRecorder(orgRec)
	}

	evt.RegisterDelivery(dispatcher, db, decrypt, envelope)

	// 8. WebSocket hub.
	wsHub := api.NewHub(cfg.PublicBaseURL)
	if err := wsHub.SubscribeToBus(msgBus); err != nil {
		return fmt.Errorf("ws hub subscribe: %w", err)
	}

	// 9. HTTP server.
	if mail != nil {
		engine.SetQuotaMailer(mail, cfg.PublicBaseURL)
	}
	billingClient, billingWebhookSecret := wiring.InitBilling(cfg)
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	maintenanceTicker := maintenance.NewTicker(db, emitter, maintenance.DefaultInterval)
	go maintenanceTicker.Run(runCtx)
	// Periodic billing seat reconcile. A no-op without billing keys.
	wiring.StartBilling(runCtx, billingClient, db)
	support.StartEscalation(runCtx, db, msgBus, mail, cfg.PublicBaseURL, jwt)
	support.StartSurveys(runCtx, db, msgBus, mail, cfg.PublicBaseURL, jwt)
	// Promo lifecycle: expiry warning emails and the downgrade to Free.
	promo.Start(runCtx, db, mail, cfg.PublicBaseURL)
	// Owner emails when paid channels start and end their grace after a
	// downgrade. A no-op without SMTP or in the FOSS build.
	notify.StartPaidChannelNotices(runCtx, db, mail, cfg.PublicBaseURL)
	srv := api.NewServer(db, jwt, msgBus, wsHub, billingClient, billingWebhookSecret, cfg.PublicBaseURL, api.ServerOptions{
		TestSender:            dispatcher,
		RegistrationEnabled:   cfg.RegistrationEnabled,
		Envelope:              envelope,
		OpsToken:              cfg.OpsToken,
		OpsActorKey:           cfg.OpsActorKey,
		Mailer:                mail,
		APIKeyHasher:          apiKeyHasher,
		APIKeyMaxTTL:          cfg.APIKeyMaxTTL,
		DiscordPublicKey:      cfg.DiscordPublicKey,
		SlackSigningSecret:    cfg.SlackSigningSecret,
		SlackClientID:         cfg.SlackClientID,
		SlackClientSecret:     cfg.SlackClientSecret,
		DiscordClientID:       cfg.DiscordClientID,
		DiscordClientSecret:   cfg.DiscordClientSecret,
		DiscordBotToken:       cfg.DiscordBotToken,
		TelegramWebhookSecret: cfg.TelegramWebhookSecret,
		TelegramBotToken:      cfg.TelegramBotToken,
		TelegramBotUsername:   cfg.TelegramBotUsername,
		TwilioAuthToken:       cfg.TwilioAuthToken,
		TelnyxWebhookSecret:   cfg.TelnyxWebhookSecret,
		PhoneVerifySender:     phoneVerifySender(cfg),
		TrustedProxyNets:      api.ParseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs),
		LifecycleCtx:          runCtx,
		OTELExportSync:        otelSync,
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
	if err := engine.Start(runCtx); err != nil {
		return fmt.Errorf("escalation engine start: %w", err)
	}
	if err := dispatcher.Start(runCtx); err != nil {
		return fmt.Errorf("dispatcher start: %w", err)
	}
	// Record dead-lettered notifications on the alert timeline and on
	// the channel's delivery health.
	if err := notify.StartDeadLetterConsumer(msgBus, recorder); err != nil {
		return fmt.Errorf("dead letter consumer: %w", err)
	}

	if err := evt.StartRouter(runCtx, db, envelope); err != nil {
		return err
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
		// Drain the notification pools before cancelling runCtx so queued
		// jobs are sent rather than abandoned. Anything left at the
		// deadline stays in the outbox for the next start.
		bus.StopConsuming(msgBus)
		drainCtx, drainCancel := context.WithTimeout(context.Background(), 20*time.Second)
		if err := dispatcher.Stop(drainCtx); err != nil {
			log.Printf("notification drain: %v", err)
		}
		drainCancel()
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
		}
		// A4-N-2: tear down long-lived event sweepers so they don't
		// outlive the parent process.
		evt.Close()
	}()

	log.Printf("yipyap listening on %s", cfg.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}

	// Wait for background services to stop.
	sched.Stop()
	engine.Stop()

	log.Println("yipyap stopped")
	return nil
}
