package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

const configSentinel = "••••••••"

var secretFields = map[string][]string{
	"webhook":  {},
	"slack":    {"bot_token"},
	"discord":  {"bot_token"},
	"telegram": {"bot_token"},
	"smtp":     {"password"},
	"ntfy":     {"token"},
	"pushover": {"api_token"},
}

func init() {
	for k, v := range proSecretFields {
		secretFields[k] = v
	}
}

type channelResponse struct {
	ID      string          `json:"id"`
	OrgID   string          `json:"org_id"`
	Type    string          `json:"type"`
	Name    string          `json:"name"`
	Config  json.RawMessage `json:"config"`
	Enabled bool            `json:"enabled"`
}

func toChannelResponse(ch *domain.NotificationChannel, redact bool) channelResponse {
	cfg := ch.Config
	if redact {
		cfg = redactConfig(ch.Type, cfg)
	}
	raw := json.RawMessage(cfg)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	return channelResponse{
		ID:      ch.ID,
		OrgID:   ch.OrgID,
		Type:    ch.Type,
		Name:    ch.Name,
		Config:  raw,
		Enabled: ch.Enabled,
	}
}

func redactConfig(channelType, config string) string {
	fields := secretFields[channelType]
	if len(fields) == 0 || config == "" {
		return config
	}
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(config), &m); err != nil {
		return config
	}
	for _, f := range fields {
		if _, exists := m[f]; exists {
			m[f] = configSentinel
		}
	}
	out, _ := json.Marshal(m)
	return string(out)
}

func mergeConfig(channelType, newConfig, existingConfig string) string {
	fields := secretFields[channelType]
	if len(fields) == 0 {
		return newConfig
	}
	var newM, existM map[string]interface{}
	if err := json.Unmarshal([]byte(newConfig), &newM); err != nil {
		return newConfig
	}
	if err := json.Unmarshal([]byte(existingConfig), &existM); err != nil {
		return newConfig
	}
	for _, f := range fields {
		if v, ok := newM[f]; ok {
			if s, ok := v.(string); ok && s == configSentinel {
				if existing, ok := existM[f]; ok {
					newM[f] = existing
				}
			}
		}
	}
	out, _ := json.Marshal(newM)
	return string(out)
}

// TestSender can synchronously send a notification job (used for test endpoint).
type TestSender interface {
	TestSend(ctx context.Context, job domain.NotificationJob) (string, error)
}

type NotificationChannelHandler struct {
	store      store.Store
	testSender TestSender
}

func NewNotificationChannelHandler(s store.Store, ts TestSender) *NotificationChannelHandler {
	return &NotificationChannelHandler{store: s, testSender: ts}
}

func (h *NotificationChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	channels, err := h.store.NotificationChannels().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list notification channels")
		return
	}
	resp := make([]channelResponse, len(channels))
	for i, ch := range channels {
		resp[i] = toChannelResponse(ch, true)
	}
	jsonResponse(w, http.StatusOK, resp)
}

func (h *NotificationChannelHandler) Get(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ch, err := h.store.NotificationChannels().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if ch.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	jsonResponse(w, http.StatusOK, toChannelResponse(ch, true))
}

func (h *NotificationChannelHandler) Create(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	var req struct {
		Name    string          `json:"name"`
		Type    string          `json:"type"`
		Config  json.RawMessage `json:"config"`
		Enabled bool            `json:"enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !checkChannelPlanGate(h, w, r, req.Type) {
		return
	}

	if err := checkChannelLimit(r.Context(), h.store, claims.OrgID); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	// SSRF protection: reject webhook channels targeting private/internal URLs.
	if req.Type == "webhook" && len(req.Config) > 0 {
		var cfg struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(req.Config, &cfg) == nil && cfg.URL != "" {
			if err := checker.ValidateHTTPTarget(cfg.URL); err != nil {
				errorResponse(w, http.StatusBadRequest, err.Error())
				return
			}
		}
	}

	// Phone number validation: only US/Canada numbers for SMS and voice.
	if err := validatePhoneIfNeeded(req.Type, req.Config); err != nil {
		errorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   claims.OrgID,
		Type:    req.Type,
		Name:    req.Name,
		Config:  string(req.Config),
		Enabled: req.Enabled,
	}
	if err := h.store.NotificationChannels().Create(r.Context(), ch); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create notification channel")
		return
	}
	jsonResponse(w, http.StatusCreated, toChannelResponse(ch, true))
}

func (h *NotificationChannelHandler) Update(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	claims := middleware.GetClaims(r.Context())

	ch, err := h.store.NotificationChannels().GetByID(r.Context(), id)
	if err != nil || ch.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}

	var req struct {
		Name    *string          `json:"name"`
		Config  *json.RawMessage `json:"config"`
		Enabled *bool            `json:"enabled"`
	}
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		ch.Name = *req.Name
	}
	if req.Config != nil {
		newConfig := string(*req.Config)
		// Note: mergeConfig uses ch.Type (existing type). The update handler does not
		// allow changing channel type, so this is always correct. If type changes are
		// ever added, this must use the new type and re-validate secret fields.

		// SSRF protection: reject webhook channels targeting private/internal URLs.
		if ch.Type == "webhook" {
			var cfg struct {
				URL string `json:"url"`
			}
			if json.Unmarshal([]byte(newConfig), &cfg) == nil && cfg.URL != "" {
				if err := checker.ValidateHTTPTarget(cfg.URL); err != nil {
					errorResponse(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}

		// Phone number validation: only US/Canada numbers for SMS and voice.
		if err := validatePhoneIfNeeded(ch.Type, json.RawMessage(newConfig)); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}

		ch.Config = mergeConfig(ch.Type, newConfig, ch.Config)
	}
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}

	if err := h.store.NotificationChannels().Update(r.Context(), ch); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update notification channel")
		return
	}
	jsonResponse(w, http.StatusOK, toChannelResponse(ch, true))
}

func (h *NotificationChannelHandler) Delete(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ch, err := h.store.NotificationChannels().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if ch.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	if err := h.store.NotificationChannels().Delete(r.Context(), id); err != nil {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *NotificationChannelHandler) Test(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	ch, err := h.store.NotificationChannels().GetByID(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	claims := middleware.GetClaims(r.Context())
	if ch.OrgID != claims.OrgID {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}

	if h.testSender == nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "test notification sent (dry run, no dispatcher configured)"})
		return
	}

	// Build a test job with the channel's config as plaintext (version byte 0x00).
	configBytes := append([]byte{0x00}, []byte(ch.Config)...)
	job := domain.NotificationJob{
		ID:           uuid.New().String(),
		AlertID:      "test-" + uuid.New().String(),
		OrgID:        ch.OrgID,
		MonitorName:  "Test Notification",
		Severity:     "info",
		Channel:      ch.Type,
		TargetConfig: base64Encode(configBytes),
		Message:      "This is a test notification from YipYap.",
		DedupeKey:    "test-" + uuid.New().String(),
	}

	if _, err := h.testSender.TestSend(r.Context(), job); err != nil {
		errorResponse(w, http.StatusBadGateway, "test notification failed: "+err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "test notification sent"})
}
