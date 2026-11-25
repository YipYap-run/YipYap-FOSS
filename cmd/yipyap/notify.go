package main

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/api/handlers"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func setupDispatcher(msgBus bus.Bus, _ store.Store, decrypt notify.DecryptFunc, cfg *config.Config, rec *notify.Recorder) (*notify.Dispatcher, *providers.CloudEventHTTP) {
	// FOSS: no dedup, no outbox, no paid notification providers.
	d := notify.NewDispatcher(msgBus, nil, notify.WithRecorder(rec))

	d.Register(providers.NewWebhook(providers.WebhookConfig{}, decrypt), 200)
	d.Register(providers.NewSlack(providers.SlackConfig{}, decrypt).WithConsoleURL(cfg.PublicBaseURL), 100)
	d.Register(providers.NewDiscord(providers.DiscordConfig{}, decrypt).WithConsoleURL(cfg.PublicBaseURL), 100)
	d.Register(providers.NewTelegram(providers.TelegramConfig{BotToken: cfg.TelegramBotToken}, decrypt), 100)
	d.Register(providers.NewSMTP(providers.SMTPConfig{
		Host:     cfg.SMTPHost,
		Port:     cfg.SMTPPort,
		Username: cfg.SMTPUser,
		Password: cfg.SMTPPass,
		From:     cfg.SMTPFrom,
	}, decrypt), 200)
	d.Register(providers.NewNtfy(providers.NtfyConfig{}, decrypt), 100)
	d.Register(providers.NewPushover(providers.PushoverConfig{}, decrypt), 100)
	d.Register(providers.NewPagerDuty(providers.PagerDutyConfig{}, decrypt), 100)

	cehttp := providers.NewCloudEventHTTP(decrypt)
	d.Register(cehttp, 100)

	return d, cehttp
}

// phoneVerifySender is nil in FOSS: there are no SMS or voice channels.
func phoneVerifySender(_ *config.Config) handlers.PhoneCodeSender { return nil }
