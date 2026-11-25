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

// contactIntegration is an org channel a personal DM can send through. Only
// the id, name and type are exposed, never its config.
type contactIntegration struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Enabled bool   `json:"enabled"`
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
			integrations = append(integrations, contactIntegration{ID: ch.ID, Name: ch.Name, Type: ch.Type, Enabled: ch.Enabled})
		}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"types":        types,
		"integrations": integrations,
		"limits": map[string]int{
			"max_methods":       contactmethod.MaxMethodsPerUser,
			"max_rules":         contactmethod.MaxRulesPerUser,
			"max_delay_minutes": contactmethod.MaxDelayMinutes,
			"max_label_length":  contactmethod.MaxLabelLength,
			"code_ttl_minutes":  int(contactCodeTTL.Minutes()),
		},
	})
}
