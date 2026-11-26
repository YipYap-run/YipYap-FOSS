package handlers

import (
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type contactTypeOption struct {
	Type domain.ContactMethodType `json:"type"`
	// Supported is false when this build cannot deliver the type at all.
	Supported bool `json:"supported"`
	// PaidOnly marks types that need a paid plan on SaaS.
	PaidOnly bool `json:"paid_only"`
	// Available is true when the caller's org can add the type right now.
	Available bool `json:"available"`
	// Integration names the org channel type a DM borrows its bot from.
	Integration string `json:"integration,omitempty"`
}

// Where a personal DM sends from (contactIntegration.Source).
const (
	// integrationOwnBot is an org channel holding its own bot token. A
	// method picks it with via_channel_id.
	integrationOwnBot = "own_bot"
	// integrationPlatformBot is a Telegram channel on the YipYap bot.
	integrationPlatformBot = "platform_bot"
)

// contactIntegration is an org channel or app install a personal DM can
// send through. Only the id, name and type are exposed, never its config
// or token.
type contactIntegration struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
	// OwnBot marks a Telegram channel that sends through the org's own bot
	// rather than the YipYap bot.
	OwnBot bool `json:"own_bot,omitempty"`
	// Source is own_bot, platform_bot, or app for a platform Slack or
	// Discord app install (SaaS only, picked with via_install_id).
	Source string `json:"source"`
}

func integrationFor(t domain.ContactMethodType) string {
	switch t {
	case domain.ContactSlackDM:
		return "slack"
	case domain.ContactDiscordDM:
		return "discord"
	case domain.ContactTelegram:
		return "telegram"
	}
	return ""
}

// Options tells the settings page which contact method types the caller
// can add, which org integrations a DM can send through, and the limits the
// API enforces, so the form never offers a choice the server refuses.
func (h *ContactMethodHandler) Options(w http.ResponseWriter, r *http.Request) {
	claims, _, _, ok := h.personal(w, r)
	if !ok {
		return
	}
	types := make([]contactTypeOption, 0, len(domain.AllContactMethodTypes))
	for _, t := range domain.AllContactMethodTypes {
		supported := contactmethod.TypeSupported(t)
		available := supported && contactmethod.PlanError(r.Context(), h.store, claims.OrgID, t) == nil
		types = append(types, contactTypeOption{
			Type: t, Supported: supported, PaidOnly: contactmethod.PaidOnly(t),
			Available: available, Integration: integrationFor(t),
		})
	}

	channels, err := h.store.NotificationChannels().ListByOrg(r.Context(), claims.OrgID, store.ListParams{Limit: 500})
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list integrations")
		return
	}
	integrations := make([]contactIntegration, 0)
	for _, ch := range channels {
		switch ch.Type {
		case "slack", "discord", "telegram":
			in := contactIntegration{ID: ch.ID, Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled, Source: integrationOwnBot}
			if ch.Type == "telegram" {
				cfg, _ := parseTelegramConfig(ch.Config)
				in.OwnBot = !cfg.usesPlatformBot()
				if !in.OwnBot {
					in.Source = integrationPlatformBot
				}
			}
			integrations = append(integrations, in)
		}
	}
	apps, err := appContactIntegrations(r.Context(), h.store, claims.OrgID, h.slackAppDMs, h.discordAppDMs)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list integrations")
		return
	}
	// App installs first: they need no bot of the org's own.
	integrations = append(apps, integrations...)

	jsonResponse(w, http.StatusOK, map[string]any{
		"types":        types,
		"integrations": integrations,
		// The form offers Connect Telegram when this is true.
		"telegram_linking": h.telegramLinking,
		"limits": map[string]int{
			"max_methods":       contactmethod.MaxMethodsPerUser,
			"max_rules":         contactmethod.MaxRulesPerUser,
			"max_delay_minutes": contactmethod.MaxDelayMinutes,
			"max_label_length":  contactmethod.MaxLabelLength,
			"code_ttl_minutes":  int(contactCodeTTL.Minutes()),
		},
	})
}
