package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api"
	"github.com/YipYap-run/YipYap-FOSS/internal/auth"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/escalation"
	"github.com/YipYap-run/YipYap-FOSS/internal/jobs"
	"github.com/YipYap-run/YipYap-FOSS/internal/mailer"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/seed"
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
	if cfg.AllowPrivateTargets {
		log.Println("SSRF protection: private/internal targets ALLOWED (self-hosted mode)")
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
	apiKeyHasher := auth.NewAPIKeyHasher([]byte(cfg.JWTSecret))

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

	// 6. Escalation engine.
	engine := escalation.NewEngine(db, msgBus, envelope, nil)

	// 7. Notification dispatcher.
	var decrypt notify.DecryptFunc
	if envelope != nil {
		decrypt = envelope.Decrypt
	}
	dispatcher := setupDispatcher(msgBus, db, decrypt, cfg)

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
		TrustedProxyNets:      api.ParseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs),
	})
	httpSrv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      srv,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// 9. Start everything.
	runCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := sched.Start(runCtx); err != nil {
		return fmt.Errorf("scheduler start: %w", err)
	}
	engine.Start(runCtx)
	if err := dispatcher.Start(runCtx); err != nil {
		return fmt.Errorf("dispatcher start: %w", err)
	}

	jobs.StartRetentionPruner(runCtx, db)

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
