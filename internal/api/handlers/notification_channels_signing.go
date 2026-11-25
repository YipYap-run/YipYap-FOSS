package handlers

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
)

// webhookSecretKey is the config key that holds a webhook channel's signing
// secret. The server owns it: clients cannot set it on create or update,
// and no read path returns it. It is shown once, in the create or
// rotate-secret response.
const webhookSecretKey = "signing_secret"

var errConfigNotObject = errors.New("config must be a JSON object")

// configObject parses a channel config into a map. An empty config is an
// empty object.
func configObject(config string) (map[string]any, error) {
	m := map[string]any{}
	if config == "" {
		return m, nil
	}
	if err := json.Unmarshal([]byte(config), &m); err != nil || m == nil {
		return nil, errConfigNotObject
	}
	return m, nil
}

// webhookSecretOf returns the stored signing secret, or "" when the channel
// has none (webhook channels created before signing existed).
func webhookSecretOf(config string) string {
	m, err := configObject(config)
	if err != nil {
		return ""
	}
	s, _ := m[webhookSecretKey].(string)
	return s
}

// withWebhookSecret returns config with the signing secret replaced by
// secret, dropping any value the client sent. An empty secret removes the
// key.
func withWebhookSecret(config, secret string) (string, error) {
	m, err := configObject(config)
	if err != nil {
		return "", err
	}
	delete(m, webhookSecretKey)
	if secret != "" {
		m[webhookSecretKey] = secret
	}
	out, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// stripWebhookSecret removes the signing secret from a config before it is
// returned to a client. A config that is not an object is returned as is,
// since it cannot hold the key.
func stripWebhookSecret(config string) string {
	m, err := configObject(config)
	if err != nil {
		return config
	}
	if _, ok := m[webhookSecretKey]; !ok {
		return config
	}
	delete(m, webhookSecretKey)
	out, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(out)
}

// sanitizeImportedWebhookConfig drops any client-supplied signing secret
// from a bulk-imported webhook config. Imported channels start unsigned and
// get a secret through rotate-secret.
func sanitizeImportedWebhookConfig(channelType, config string) string {
	if channelType != "webhook" {
		return config
	}
	return stripWebhookSecret(config)
}

// RotateSecret issues a new signing secret for a webhook channel and returns
// it once. The previous secret stops working immediately. A webhook channel
// created before signing existed gets its first secret this way.
func (h *NotificationChannelHandler) RotateSecret(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	ch, err := h.store.NotificationChannels().GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil || ch.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	if ch.Type != "webhook" {
		errorResponse(w, http.StatusBadRequest, "only webhook channels have a signing secret")
		return
	}
	secret, err := providers.NewWebhookSecret()
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to generate signing secret")
		return
	}
	cfg, err := withWebhookSecret(ch.Config, secret)
	if err != nil {
		errorResponse(w, http.StatusConflict, "channel config is not a JSON object; save the channel again before rotating")
		return
	}
	ch.Config = cfg
	if err := h.store.NotificationChannels().Update(r.Context(), ch); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to rotate signing secret")
		return
	}
	resp := toChannelResponse(ch)
	resp.SigningSecret = secret
	jsonResponse(w, http.StatusOK, resp)
}

// webhookSigningInfo reports whether a webhook channel signs deliveries.
func webhookSigningInfo(ch *domain.NotificationChannel) *bool {
	if ch.Type != "webhook" {
		return nil
	}
	set := webhookSecretOf(ch.Config) != ""
	return &set
}
