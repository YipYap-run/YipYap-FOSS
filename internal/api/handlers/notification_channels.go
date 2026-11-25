package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	cevents "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	cetypes "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify/providers"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func base64Encode(data []byte) string {
	return base64.StdEncoding.EncodeToString(data)
}

const configSentinel = "••••••••"

// secretFields lists the JSON field paths (per channel type) whose values
// must be redacted on read and preserved on write when the client echoes
// back the sentinel. Paths may be flat (e.g. "bot_token") or dotted to
// traverse nested objects (e.g. "auth.oidc.client_secret"). A final "*"
// covers every value of a map (webhook headers: names are shown, values
// are not). Endpoint URLs that carry a credential are handled separately
// in notification_channels_redact.go.
var secretFields = map[string][]string{
	"webhook":         {"headers.*"},
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
	ID      string               `json:"id"`
	OrgID   string               `json:"org_id"`
	Type    string               `json:"type"`
	Name    string               `json:"name"`
	Config  json.RawMessage      `json:"config"`
	Enabled bool                 `json:"enabled"`
	Health  domain.ChannelHealth `json:"health"`
	// Verification is set only for channel types whose destination must be
	// confirmed before delivery (SMS and voice in SaaS).
	Verification *channelVerificationInfo `json:"verification,omitempty"`
	// SigningSecretSet is set for webhook channels only: true when
	// deliveries are signed. The secret itself is never returned here.
	SigningSecretSet *bool `json:"signing_secret_set,omitempty"`
	// SigningSecret is filled only in the create and rotate-secret
	// responses, so the client can show it once.
	SigningSecret string `json:"signing_secret,omitempty"`
	// Set only when the endpoint URL is write-only (it holds a
	// credential): config[url_field] is the sentinel, url_host is the
	// scheme and host to show, url_set says a URL is stored.
	URLField string `json:"url_field,omitempty"`
	URLHost  string `json:"url_host,omitempty"`
	URLSet   bool   `json:"url_set,omitempty"`
	// PlanStatus is ok, grace (a paid channel type on an org that left
	// its paid plan, still delivering until GraceEndsAt) or disabled
	// (past the grace, dispatch skips it). Free channel types and the
	// FOSS build are always ok. Absent only when the org's plan could not
	// be read.
	PlanStatus  string     `json:"plan_status,omitempty"`
	GraceEndsAt *time.Time `json:"grace_ends_at,omitempty"`
	// UsedBy lists the escalation policies whose steps send through this
	// channel (empty, never null). The channel cannot be deleted while it
	// is non-empty; disabling it is allowed.
	UsedBy []domain.ChannelPolicyRef `json:"used_by"`
}

// channelVerificationInfo tells the UI whether a phone channel is live.
type channelVerificationInfo struct {
	Status     string     `json:"status"` // "verified" or "pending"
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
}

// withPlanStatus fills PlanStatus and GraceEndsAt from status.
func (c channelResponse) withPlanStatus(status channelPlanStatusFunc) channelResponse {
	if status != nil {
		c.PlanStatus, c.GraceEndsAt = status(c.Type)
	}
	return c
}

// toChannelResponse is the only way a channel leaves the API. Secrets are
// always redacted, whatever the caller's role.
func toChannelResponse(ch *domain.NotificationChannel) channelResponse {
	cfg, red := redactConfig(ch.Type, phoneResponseConfig(ch.Type, ch.Config))
	if ch.Type == "webhook" {
		cfg = stripWebhookSecret(cfg)
	}
	raw := json.RawMessage(cfg)
	if len(raw) == 0 {
		raw = json.RawMessage("{}")
	}
	resp := channelResponse{
		ID:               ch.ID,
		OrgID:            ch.OrgID,
		Type:             ch.Type,
		Name:             ch.Name,
		Config:           raw,
		Enabled:          ch.Enabled,
		Health:           ch.Health(),
		Verification:     channelVerification(ch),
		SigningSecretSet: webhookSigningInfo(ch),
	}
	if red != nil {
		resp.URLField, resp.URLHost, resp.URLSet = red.Field, red.Host, true
	}
	return resp
}

// validateUserURLs applies the create-time SSRF check to the URL fields an
// org member can set on webhook (url) and ntfy (server_url) channels. The
// notifier re-checks every connection at send time; this gives a clear 400
// up front.
func validateUserURLs(channelType string, config json.RawMessage) error {
	if len(config) == 0 {
		return nil
	}
	var field, value string
	switch channelType {
	case "webhook":
		var cfg struct {
			URL string `json:"url"`
		}
		if json.Unmarshal(config, &cfg) != nil {
			return errors.New("invalid config")
		}
		field, value = "url", cfg.URL
	case "ntfy":
		var cfg struct {
			ServerURL string `json:"server_url"`
		}
		if json.Unmarshal(config, &cfg) != nil {
			return errors.New("invalid config")
		}
		field, value = "server_url", cfg.ServerURL
	default:
		return nil
	}
	if value == "" {
		return nil
	}
	if err := checker.ValidateHTTPTarget(value); err != nil {
		// Do not echo what the name resolved to: that would let the form
		// map internal DNS.
		if strings.Contains(err.Error(), "private/internal") {
			return fmt.Errorf("%s cannot point to a private or internal address", field)
		}
		return fmt.Errorf("%s: %w", field, err)
	}
	return nil
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
	store         store.Store
	testSender    TestSender
	discordLookup ChannelLookup
	// Platform Telegram bot linking (see telegram_link.go).
	telegramBotUsername string
	telegramLinking     bool
	now                 func() time.Time
	// platformEmail is true when the server has a platform mailer that
	// email channels in platform mode can send through.
	platformEmail bool
	// recovery emails the owners when a Test ends a failure streak they
	// were told about. nil means no follow-up email.
	recovery ChannelRecoveryNotifier
	// testLimit caps channel tests per org.
	testLimit *channelTestLimiter
}

func NewNotificationChannelHandler(s store.Store, ts TestSender) *NotificationChannelHandler {
	return &NotificationChannelHandler{store: s, testSender: ts, testLimit: newChannelTestLimiter()}
}

func (h *NotificationChannelHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	params := paginationFromQuery(r)
	channels, err := h.store.NotificationChannels().ListByOrg(r.Context(), claims.OrgID, params)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list notification channels")
		return
	}
	planStatus, err := h.channelPlanStatus(r.Context(), claims.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read the plan for notification channels")
		return
	}
	refs, err := h.store.NotificationChannels().ListPolicyRefs(r.Context(), claims.OrgID, "")
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read which escalation policies use these channels")
		return
	}
	resp := make([]channelResponse, len(channels))
	for i, ch := range channels {
		resp[i] = toChannelResponse(ch).withPlanStatus(planStatus).withUsedBy(refs[ch.ID])
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
	planStatus, err := h.channelPlanStatus(r.Context(), ch.OrgID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read the plan for this notification channel")
		return
	}
	refs, err := h.store.NotificationChannels().ListPolicyRefs(r.Context(), ch.OrgID, ch.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read which escalation policies use this channel")
		return
	}
	jsonResponse(w, http.StatusOK, toChannelResponse(ch).withPlanStatus(planStatus).withUsedBy(refs[ch.ID]))
}

// prepareChannelConfig runs every config check a new channel must pass:
// the typed shape, unresolved secrets, outbound URL targets, the phone
// number, the email sender rules, and the chat-app installs. It returns
// the canonical config, or the first problem as a *channelConfigError or
// a plain error. Single create and bulk import both go through here so an
// imported channel can never be looser than one made in the form.
func (h *NotificationChannelHandler) prepareChannelConfig(ctx context.Context, orgID, channelType string, config json.RawMessage) (json.RawMessage, error) {
	// The signing secret is server-owned: drop whatever the client sent
	// before the shape check, so an echoed secret is ignored, not refused.
	if channelType == "webhook" {
		config = json.RawMessage(stripWebhookSecret(string(config)))
	}
	if err := validateChannelConfig(channelType, config, nil); err != nil {
		return nil, err
	}
	if field := unresolvedSentinel(channelType, config); field != "" {
		return nil, unresolvedSentinelError(field)
	}
	if err := validateUserURLs(channelType, config); err != nil {
		return nil, err
	}
	if channelType == "cloudevent_http" && len(config) > 0 {
		var cfg struct {
			SinkURL string `json:"sink_url"`
			Auth    struct {
				OIDC struct {
					Issuer   string `json:"issuer"`
					TokenURL string `json:"token_url"`
				} `json:"oidc"`
			} `json:"auth"`
		}
		if json.Unmarshal(config, &cfg) == nil {
			if err := validateCloudEventHTTPURLs(cfg.SinkURL, cfg.Auth.OIDC.TokenURL, cfg.Auth.OIDC.Issuer); err != nil {
				return nil, err
			}
		}
	}
	phoneCfg, err := validatePhoneIfNeeded(channelType, config)
	if err != nil {
		return nil, err
	}
	config = phoneCfg
	if channelType == emailChannelType {
		canon, err := h.checkEmailConfig(config)
		if err != nil {
			return nil, err
		}
		config = canon
	}
	slackCfg, err := prepareSlackAppConfig(ctx, h.store, orgID, channelType, string(config))
	if err != nil {
		return nil, err
	}
	discordCfg, err := prepareDiscordAppConfig(ctx, h.store, h.discordLookup, orgID, channelType, slackCfg)
	if err != nil {
		return nil, err
	}
	telegramCfg, err := prepareTelegramCreateConfig(channelType, discordCfg)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(telegramCfg), nil
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
	if msg := retiredChannelType(req.Type); msg != "" {
		errorResponse(w, http.StatusBadRequest, msg)
		return
	}
	if !knownChannelType(req.Type) {
		writeChannelConfigError(w, &channelConfigError{Field: "type", Msg: "is not a channel type this server supports"})
		return
	}
	if !checkChannelPlanGate(h, w, r, req.Type) {
		return
	}

	if err := checkChannelLimit(r.Context(), h.store, claims.OrgID); err != nil {
		errorResponse(w, http.StatusForbidden, err.Error())
		return
	}

	prepared, err := h.prepareChannelConfig(r.Context(), claims.OrgID, req.Type, req.Config)
	if err != nil {
		var ce *channelConfigError
		if errors.As(err, &ce) {
			writeChannelConfigError(w, err)
		} else {
			errorResponse(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	req.Config = prepared

	ch := &domain.NotificationChannel{
		ID:      uuid.New().String(),
		OrgID:   claims.OrgID,
		Type:    req.Type,
		Name:    req.Name,
		Config:  string(req.Config),
		Enabled: req.Enabled,
	}

	// Webhook channels get a server-generated signing secret. Any value
	// the client sent is discarded.
	var signingSecret string
	if req.Type == "webhook" {
		secret, err := providers.NewWebhookSecret()
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to generate signing secret")
			return
		}
		cfg, err := withWebhookSecret(ch.Config, secret)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		ch.Config = cfg
		signingSecret = secret
	}

	if err := h.store.NotificationChannels().Create(r.Context(), ch); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to create notification channel")
		return
	}
	// A new channel has no policy references yet.
	resp := toChannelResponse(ch).withPlanStatus(h.channelPlanStatusBestEffort(r.Context(), ch.OrgID)).withUsedBy(nil)
	resp.SigningSecret = signingSecret
	jsonResponse(w, http.StatusCreated, resp)
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
	clearVerification := false
	if err := decodeBody(r, &req); err != nil {
		errorResponse(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name != nil {
		ch.Name = *req.Name
	}
	if req.Config != nil {
		newConfig := string(*req.Config)

		// SMS and voice: the config is only a US/Canada destination number.
		phoneCfg, err := validatePhoneIfNeeded(ch.Type, json.RawMessage(newConfig))
		if err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		newConfig = string(phoneCfg)

		// Stored secrets go back in wherever the client echoed the
		// sentinel, before any check, so the checks below see the config
		// that will be saved (a write-only URL included).
		// mergeConfig uses ch.Type: the update handler does not allow
		// changing channel type. If that is ever added, this must use the
		// new type and re-validate secret fields.
		merged := newConfig
		// A phone config is replaced whole: merging would carry legacy
		// Twilio credentials from the old row into the new one.
		if !isPhoneChannel(ch.Type) {
			merged = mergeConfig(ch.Type, newConfig, ch.Config)
		}
		if field := unresolvedSentinel(ch.Type, []byte(merged)); field != "" {
			writeChannelConfigError(w, unresolvedSentinelError(field))
			return
		}

		// SSRF protection: reject webhook and ntfy channels targeting
		// private/internal URLs.
		if err := validateUserURLs(ch.Type, json.RawMessage(merged)); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
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
			if json.Unmarshal([]byte(merged), &cfg) == nil {
				if err := validateCloudEventHTTPURLs(cfg.SinkURL, cfg.Auth.OIDC.TokenURL, cfg.Auth.OIDC.Issuer); err != nil {
					errorResponse(w, http.StatusBadRequest, err.Error())
					return
				}
			}
		}

		// Checked after the merge so a redacted secret echoed back as the
		// sentinel counts as present.
		if err := validateChannelConfig(ch.Type, []byte(merged), []byte(ch.Config)); err != nil {
			writeChannelConfigError(w, err)
			return
		}
		if ch.Type == emailChannelType {
			canon, err := h.checkEmailConfig([]byte(merged))
			if err != nil {
				writeEmailConfigError(w, err)
				return
			}
			merged = string(canon)
		}
		if merged, err = prepareSlackAppConfig(r.Context(), h.store, ch.OrgID, ch.Type, merged); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		if merged, err = prepareDiscordAppConfig(r.Context(), h.store, h.discordLookup, ch.OrgID, ch.Type, merged); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		if merged, err = prepareTelegramUpdateConfig(ch.Type, merged, ch.Config); err != nil {
			errorResponse(w, http.StatusBadRequest, err.Error())
			return
		}
		// The signing secret is server-owned: keep the stored one and
		// ignore whatever the client sent.
		if ch.Type == "webhook" {
			kept, err := withWebhookSecret(merged, webhookSecretOf(ch.Config))
			if err != nil {
				errorResponse(w, http.StatusBadRequest, err.Error())
				return
			}
			merged = kept
		}
		// A new number has not been confirmed by anyone, so the channel goes
		// back to pending until it is verified again.
		if phoneTargetChanged(ch.Type, ch.Config, merged) {
			clearVerification = true
		}
		ch.Config = merged
	}
	if req.Enabled != nil {
		ch.Enabled = *req.Enabled
	}

	// Read before the write, so a failure here changes nothing. Disabling
	// a channel that policies use is allowed; the response lists them so
	// the client can say which policies lose this route.
	refs, err := h.store.NotificationChannels().ListPolicyRefs(r.Context(), ch.OrgID, ch.ID)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to read which escalation policies use this channel")
		return
	}
	if err := h.store.NotificationChannels().Update(r.Context(), ch); err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to update notification channel")
		return
	}
	if clearVerification && ch.VerifiedAt != nil {
		if err := h.store.NotificationChannels().SetVerifiedAt(r.Context(), ch.ID, nil); err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to update notification channel")
			return
		}
		ch.VerifiedAt = nil
	}
	jsonResponse(w, http.StatusOK, toChannelResponse(ch).withPlanStatus(h.channelPlanStatusBestEffort(r.Context(), ch.OrgID)).withUsedBy(refs[ch.ID]))
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
	// Refused while any escalation step in the org sends through the
	// channel: deleting it would leave those steps paging nobody.
	found, deleted, err := h.store.NotificationChannels().DeleteIfUnused(r.Context(), id)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to delete notification channel")
		return
	}
	if !found {
		errorResponse(w, http.StatusNotFound, "notification channel not found")
		return
	}
	if !deleted {
		refs, err := h.store.NotificationChannels().ListPolicyRefs(r.Context(), ch.OrgID, ch.ID)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to read which escalation policies use this channel")
			return
		}
		writeChannelInUse(w, refs[ch.ID])
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
	if ok, wait, scope := h.testLimit.allow(ch.OrgID, ch.Type); !ok {
		writeTestRateLimited(w, wait, scope)
		return
	}

	if channelAwaitingVerification(ch) {
		errorResponse(w, http.StatusConflict, "verify this phone number before sending a test")
		return
	}
	if telegramChannelUnlinked(ch) {
		errorResponse(w, http.StatusConflict, "link a Telegram chat to this channel before sending a test")
		return
	}

	// Email channels are re-checked against the current rules, so a legacy
	// row the notifier would treat differently fails here with the field
	// to fix instead of passing a test that real alerts would not.
	testConfig := ch.Config
	if ch.Type == emailChannelType {
		canon, err := h.checkEmailConfig([]byte(ch.Config))
		if err != nil {
			writeEmailConfigError(w, err)
			return
		}
		testConfig = string(canon)
	}
	// A row saved before typed checks (for example a CloudEvents filter
	// stored as a string) fails every send. Say which field instead of
	// sending a test that cannot decode its config. Keys the row already
	// holds are tolerated, as on update.
	if err := validateChannelConfig(ch.Type, []byte(ch.Config), []byte(ch.Config)); err != nil {
		writeChannelConfigError(w, err)
		return
	}

	if h.testSender == nil {
		jsonResponse(w, http.StatusOK, map[string]string{"status": "test notification sent (dry run, no dispatcher configured)"})
		return
	}

	// Build a test job with the channel's config as plaintext (version byte 0x00).
	configBytes := append([]byte{0x00}, []byte(testConfig)...)
	job := domain.NotificationJob{
		ID:           uuid.New().String(),
		AlertID:      "test-" + uuid.New().String(),
		OrgID:        ch.OrgID,
		MonitorID:    "test-" + uuid.New().String(),
		MonitorName:  "Test Notification",
		Status:       string(domain.AlertFiring),
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
		// The provider drops events its filter does not match without an
		// error, so a filter that leaves out alert.fired would report a test
		// as sent while nothing reached the sink. Say so instead.
		var filter struct {
			EventTypes []string `json:"event_types"`
		}
		_ = json.Unmarshal([]byte(testConfig), &filter)
		if len(filter.EventTypes) > 0 && !cevents.MatchAnyGlob(ev.Type(), filter.EventTypes) {
			jsonResponse(w, http.StatusOK, map[string]interface{}{
				"status":     "filtered",
				"filtered":   true,
				"event_type": ev.Type(),
			})
			return
		}
		evJSON, err := json.Marshal(ev)
		if err != nil {
			errorResponse(w, http.StatusInternalServerError, "failed to marshal test event")
			return
		}
		job.Message = string(evJSON)
	}

	// Last check before the send, so a phone test that fails validation
	// above does not use quota.
	if !checkChannelTestGate(h, w, r, ch) {
		return
	}

	if _, err := h.testSender.TestSend(r.Context(), job); err != nil {
		// The client gets the failure class only. The provider error can
		// hold an upstream response body or a resolved internal address,
		// which would turn Test into a probe for internal services.
		slog.Warn("notification channel test failed",
			"org_id", ch.OrgID, "channel_id", ch.ID, "type", ch.Type, "error", notify.LogSafeError(err))
		errorResponse(w, http.StatusBadGateway, notify.PublicDeliveryError(err))
		return
	}
	// A test that got through clears a failing status, so fixing the
	// channel and testing it is enough to confirm the fix. A failed test
	// is reported to the caller only and does not count toward the
	// consecutive failures that alert deliveries track.
	// When the owners were emailed that this channel was failing, the
	// fix is news to them too, so they get the same follow-up a real
	// delivery would send. The store hands recovered=true to one caller.
	ownersNotified := false
	recovered, err := h.store.NotificationChannels().RecordDeliverySuccess(r.Context(), ch.ID, time.Now().UTC())
	if err != nil {
		slog.Warn("notification channel test: record success", "channel_id", ch.ID, "error", err)
	} else if recovered && h.recovery != nil {
		h.recovery.ChannelRecovered(r.Context(), ch.ID)
		ownersNotified = true
	}
	jsonResponse(w, http.StatusOK, map[string]interface{}{
		"status":          "test notification sent",
		"owners_notified": ownersNotified,
	})
}
