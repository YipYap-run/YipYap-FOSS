package main

import (
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/config"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func setupDispatcher(msgBus bus.Bus, _ store.Store, decrypt notify.DecryptFunc, _ *config.Config) *notify.Dispatcher {
	// FOSS: no dedup, no outbox, no paid notification providers.
	d := notify.NewDispatcher(msgBus, nil)

	d.Register(providers.NewWebhook(providers.WebhookConfig{}, decrypt), 200)
	d.Register(providers.NewSlack(providers.SlackConfig{}, decrypt), 100)
	d.Register(providers.NewDiscord(providers.DiscordConfig{}, decrypt), 100)
	d.Register(providers.NewTelegram(providers.TelegramConfig{}, decrypt), 100)
	d.Register(providers.NewSMTP(providers.SMTPConfig{}, decrypt), 200)
	d.Register(providers.NewNtfy(providers.NtfyConfig{}, decrypt), 100)
	d.Register(providers.NewPushover(providers.PushoverConfig{}, decrypt), 100)

	return d
}
