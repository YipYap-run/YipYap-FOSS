package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	cevents "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	cetypes "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

const configSentinel = "••••••••"

// secretFields lists the JSON field paths (per channel type) whose values
// must be redacted on read and preserved on write when the client echoes
// back the sentinel. Paths may be flat (e.g. "bot_token") or dotted to
// traverse nested objects (e.g. "auth.oidc.client_secret").
var secretFields = map[string][]string{
	"webhook":         {},
	"slack":           {"bot_token"},
	"discord":         {"bot_token"},
	"telegram":        {"bot_token"},
	"smtp":            {"password"},
	"ntfy":            {"token"},
	"pushover":        {"api_token"},
	"cloudevent_http": {"auth.oidc.client_secret"},
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

// redactPath replaces the value at a dotted JSON path with configSentinel.
// Missing intermediate nodes are a no-op (so a partially-populated config
// is tolerated rather than having keys synthesised). A flat key (no dots)
// behaves exactly like the pre-existing redaction.
func redactPath(m map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	for i, p := range parts {
		if i == len(parts)-1 {
			if _, ok := m[p]; ok {
				m[p] = configSentinel
			}
			return
		}
		nested, ok := m[p].(map[string]interface{})
		if !ok {
			return
		}
		m = nested
	}
}

// mergePath preserves the value at path in newM whenever it is the sentinel
// and the existing map has a value there. For non-sentinel values the edit
// is kept as-is, which is what lets callers actually rotate a secret.
func mergePath(newM, existM map[string]interface{}, path string) {
	parts := strings.Split(path, ".")
	// Walk parallel maps, bailing if the path doesn't exist on either side.
	for i, p := range parts {
		if i == len(parts)-1 {
			nv, ok := newM[p]
			if !ok {
				return
			}
			s, ok := nv.(string)
			if !ok || s != configSentinel {
				return
			}
			if ev, ok := existM[p]; ok {
				newM[p] = ev
			}
			return
		}
		nn, ok := newM[p].(map[string]interface{})
		if !ok {
			return
		}
		en, ok := existM[p].(map[string]interface{})
		if !ok {
			return
		}
		newM = nn
		existM = en
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
		redactPath(m, f)
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
		mergePath(newM, existM, f)
	}
	out, _ := json.Marshal(newM)
	return string(out)
}

// TestSender can synchronously send a notification job (used for test endpoint).
type TestSender interface {
	TestSend(ctx context.Context, job domain.NotificationJob) (string, error)
}

// validateCloudEventHTTPURLs applies SSRF protection to every control-plane
// URL on a cloudevent_http channel config: sink_url (outbound event
// delivery), auth.oidc.token_url (client_credentials POST), and
// auth.oidc.issuer (if set, informational on the cache key but also used
// as fallback identifier). A private/loopback/link-local result on ANY of
// them is fatal at create/update time so credentials never leave the
// process for an attacker-chosen host. See security review 02 (C-3) and
// (H-2) on the split allow-private toggle.
func validateCloudEventHTTPURLs(sinkURL, tokenURL, issuer string) error {
	if sinkURL != "" {
		if err := checker.ValidateControlPlaneHTTPTarget(sinkURL); err != nil {
			return err
		}
	}
	if tokenURL != "" {
		if err := checker.ValidateControlPlaneHTTPTarget(tokenURL); err != nil {
			return err
		}
	}
	if issuer != "" {
		if err := checker.ValidateControlPlaneHTTPTarget(issuer); err != nil {
			return err
		}
	}
	return nil
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

	// SSRF protection: reject cloudevent_http channels targeting private/internal URLs.
	// The check applies to sink_url (where events are delivered) AND to the OIDC
	// auth endpoints (token_url, issuer), an attacker with channel-create
	// permission who controls token_url can force yipyap to POST the configured
	// client_credentials form to internal metadata services (169.254.169.254
	// etc.) and receive any returned bearer token, which is then forwarded to
	// sink_url. See security review 02 (C-3).
	if req.Type == "cloudevent_http" && len(req.Config) > 0 {
		var cfg struct {
			SinkURL string `json:"sink_url"`
			Auth    struct {
				OIDC struct {
					Issuer   string `json:"issuer"`
					TokenURL string `json:"token_url"`
				} `json:"oidc"`
			} `json:"auth"`
		}
		if json.Unmarshal(req.Config, &cfg) == nil {
			if err := validateCloudEventHTTPURLs(cfg.SinkURL, cfg.Auth.OIDC.TokenURL, cfg.Auth.OIDC.Issuer); err != nil {
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

		// SSRF protection: reject cloudevent_http channels targeting private/internal URLs.
		// Covers sink_url, auth.oidc.token_url, and auth.oidc.issuer. See C-3.
		if ch.Type == "cloudevent_http" {
			var cfg struct {
				SinkURL string `json:"sink_url"`
				Auth    struct {
					OIDC struct {
						Issuer   string `json:"issuer"`
						TokenURL string `json:"token_url"`
					} `json:"oidc"`
				} `json:"auth"`
			}
			if json.Unmarshal([]byte(newConfig), &cfg) == nil {
				if err := validateCloudEventHTTPURLs(cfg.SinkURL, cfg.Auth.OIDC.TokenURL, cfg.Auth.OIDC.Issuer); err != nil {
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

	// For cloudevent_http channels, the provider expects job.Message to be
	// canonical CloudEvent JSON. Synthesize a run.yipyap.alert.fired.v1 event.
	if ch.Type == "cloudevent_http" {
		ev, err := cevents.NewAlertFiredV1(
			cevents.OrgSource(ch.OrgID),
			cetypes.AlertFiredV1Data{
				AlertID:   "test-" + uuid.New().String(),
				MonitorID: "test-" + uuid.New().String(),
				Severity:  "info",
				Reason:    "Test event from yipyap",
				FiredAt:   time.Now().UTC(),
			},
		)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to build test event")
			return
		}
		evJSON, err := json.Marshal(ev)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to marshal test event")
			return
		}
		job.Message = string(evJSON)
	}

	if _, err := h.testSender.TestSend(r.Context(), job); err != nil {
		errorResponse(w, http.StatusBadGateway, "test notification failed: "+err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, map[string]string{"status": "test notification sent"})
}
