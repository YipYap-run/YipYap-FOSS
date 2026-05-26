package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	"github.com/YipYap-run/YipYap-FOSS/internal/checker"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/oidc"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/reply"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/notify"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

var cloudEventHTTPTracer = otel.Tracer("github.com/YipYap-run/YipYap-FOSS/internal/notify/providers/cloudevent_http")

// CloudEventHTTPConfig configures a cloudevent_http sink (parsed from the
// notification channel's decrypted TargetConfig JSON).
type CloudEventHTTPConfig struct {
	// SinkURL is the HTTP(S) endpoint that receives the CloudEvent.
	// Required. Scheme must be https:// or http:// if host is localhost/127.0.0.1/[::1].
	SinkURL string `json:"sink_url"`

	// Mode is "binary" (default) or "structured" per the HTTP Protocol Binding.
	Mode string `json:"mode"`

	// EventTypes restricts which CE types this sink accepts. Each entry is a
	// path.Match glob (e.g. "run.yipyap.alert.*"). Empty means no filter.
	EventTypes []string `json:"event_types,omitempty"`

	// CEOverrides sets extension attributes on the event before sending
	// (useful for per-sink tenant/environment tagging).
	CEOverrides map[string]string `json:"ce_overrides,omitempty"`

	// Auth selects an authentication strategy. Supports "none" (empty
	// string also accepted) and "oidc" (client_credentials bearer token).
	Auth CloudEventHTTPAuth `json:"auth"`

	// AcceptedReplies configures which reply CloudEvent types this channel
	// opts in to. When nil or its Types list is empty the dispatcher treats
	// every reply as a silent no-op (Phase 1 log-only behaviour preserved).
	AcceptedReplies *ReplyConfig `json:"accepted_replies,omitempty"`
}

// ReplyConfig is the per-channel reply-dispatcher configuration.
type ReplyConfig struct {
	// Types is a list of path.Match globs over reply CloudEvent type
	// (e.g. "run.yipyap.reply.alert.*"). Empty means "deny all", the
	// dispatcher logs + records a metric but invokes no handler.
	Types []string `json:"types,omitempty"`

	// RateLimit is the per-minute cap per (orgID, replyType). 0 uses the
	// dispatcher's default (60/min).
	RateLimit int `json:"rate_limit,omitempty"`

	// TrustElevated opts the channel into high-blast-radius reply types
	// (escalated, route, monitor.deregister). Defaults to false, an
	// operator must explicitly set it on a per-channel basis.
	TrustElevated bool `json:"trust_elevated,omitempty"`

	// AllowDeregister is a second gate (in addition to TrustElevated)
	// required for reply.monitor.deregister. Splitting the flag lets an
	// operator trust a channel to escalate/route without also trusting it
	// to disable monitors, the latter takes the notification loop dark.
	AllowDeregister bool `json:"allow_deregister,omitempty"`

	// MaxSuppressSeconds caps reply.alert.suppressed duration_seconds.
	// Zero uses the package default (3600).
	MaxSuppressSeconds int `json:"max_suppress_seconds,omitempty"`

	// MaxRemediationSeconds caps reply.alert.remediation_started
	// expected_duration_seconds. Zero uses the package default (600).
	MaxRemediationSeconds int `json:"max_remediation_seconds,omitempty"`

	// MaxRouteDurationSeconds caps reply.alert.route ExpiresAt (seconds
	// from now). Zero uses the package default (86400, i.e. 24h).
	MaxRouteDurationSeconds int `json:"max_route_duration_seconds,omitempty"`
}

// CloudEventHTTPAuth describes the outbound authentication configuration
// for a cloudevent_http channel.
type CloudEventHTTPAuth struct {
	// Type is one of "", "none", or "oidc".
	Type string `json:"type"`
	// OIDC is required when Type=="oidc".
	OIDC *CloudEventHTTPOIDC `json:"oidc,omitempty"`
}

// CloudEventHTTPOIDC carries the client_credentials token-fetching
// configuration for an outbound CloudEvents HTTP sink.
//
// ClientSecret is sensitive and must be redacted in the API layer before
// being returned to callers.
type CloudEventHTTPOIDC struct {
	Issuer       string   `json:"issuer"`
	TokenURL     string   `json:"token_url"`
	ClientID     string   `json:"client_id"`
	ClientSecret string   `json:"client_secret"`
	Audience     string   `json:"audience"`
	Scopes       []string `json:"scopes,omitempty"`
}

// CloudEventHTTP delivers CloudEvents over HTTP to a user-configured sink URL
// in either binary or structured mode.
type CloudEventHTTP struct {
	decrypt          notify.DecryptFunc
	client           *http.Client
	metrics          *telemetry.Metrics
	tokenCache       oidc.TokenCache
	outboundRegistry reply.OutboundRegistry
	replyDispatcher  *reply.Dispatcher
	// replyTierResolver resolves the per-org reply rate cap (per-minute,
	// per-(org, type)). When non-nil it overrides the channel-config
	// RateLimit in the dispatcher config. Returning <=0 leaves the
	// dispatcher's default in place. Set via SetReplyTierResolver, FOSS
	// builds typically leave it nil so the package default (60/min)
	// applies and self-hosted operators retain full throughput.
	replyTierResolver func(ctx context.Context, orgID string) int
}

// CloudEventHTTPOption configures a CloudEventHTTP provider.
type CloudEventHTTPOption func(*CloudEventHTTP)

// WithCloudEventHTTPMetrics wires telemetry.Metrics into the provider for
// emit and reply-received accounting. Nil disables recording.
func WithCloudEventHTTPMetrics(m *telemetry.Metrics) CloudEventHTTPOption {
	return func(p *CloudEventHTTP) { p.metrics = m }
}

// NewCloudEventHTTP returns a CloudEvent HTTP notifier.
func NewCloudEventHTTP(decrypt notify.DecryptFunc, opts ...CloudEventHTTPOption) *CloudEventHTTP {
	p := &CloudEventHTTP{
		decrypt:    decrypt,
		client:     newSSRFGuardedHTTPClient(30 * time.Second),
		tokenCache: oidc.NewMemoryTokenCache(),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// newSSRFGuardedHTTPClient returns an *http.Client whose Transport uses
// ssrfGuardedDialContext to re-check the resolved peer address at every
// TCP connect. Defeats DNS-rebinding between create-time validation and
// send-time (H-4). Redirects follow the same DialContext, so a sink that
// redirects to http://169.254.169.254 is also blocked.
func newSSRFGuardedHTTPClient(timeout time.Duration) *http.Client {
	baseDialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           ssrfGuardedDialContext(baseDialer.DialContext),
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: tr,
	}
}

// SetTokenCache swaps the OIDC token cache used for outbound authenticated
// delivery. Safe to call with any oidc.TokenCache implementation (e.g. a
// future JetStream KV-backed cache so siblings in a cluster share tokens).
// A nil cache disables caching and forces a token fetch per send.
func (p *CloudEventHTTP) SetTokenCache(c oidc.TokenCache) {
	if p == nil {
		return
	}
	p.tokenCache = c
}

// SetMetrics wires (or replaces) the telemetry bundle on an existing
// CloudEventHTTP provider. Safe to call with nil to disable recording.
func (p *CloudEventHTTP) SetMetrics(m *telemetry.Metrics) {
	if p == nil {
		return
	}
	p.metrics = m
}

// SetOutboundRegistry installs (or replaces) the outbound-event registry used
// for reply correlation. Every Send records the outbound CloudEvent id so a
// subsequent reply carrying ce_alertref can be validated. Nil disables
// recording (the reply dispatcher will reject every reply as unknown).
func (p *CloudEventHTTP) SetOutboundRegistry(r reply.OutboundRegistry) {
	if p == nil {
		return
	}
	p.outboundRegistry = r
}

// SetReplyDispatcher installs (or replaces) the reply dispatcher used when the
// sink responds with a CloudEvent. Nil preserves Phase-1 behaviour (log-only
// with metrics).
func (p *CloudEventHTTP) SetReplyDispatcher(d *reply.Dispatcher) {
	if p == nil {
		return
	}
	p.replyDispatcher = d
}

// SetReplyTierResolver installs a callback that maps orgID -> per-minute
// reply rate cap. When set, the resolver's return value (when >0) overrides
// the channel-supplied RateLimit at dispatch time. The intended use is the
// SaaS tier-gating model where Pro caps at 30/min and Enterprise at 60/min;
// FOSS leaves the resolver nil so the package default (60/min) applies.
func (p *CloudEventHTTP) SetReplyTierResolver(fn func(ctx context.Context, orgID string) int) {
	if p == nil {
		return
	}
	p.replyTierResolver = fn
}

// Channel returns the channel identifier "cloudevent_http".
func (p *CloudEventHTTP) Channel() string { return "cloudevent_http" }

// MaxConcurrency returns the maximum number of concurrent workers.
func (p *CloudEventHTTP) MaxConcurrency() int { return 50 }

// Send delivers a CloudEvent notification job to the configured sink.
//
// The job's Message field is expected to contain canonical CloudEvents JSON
// (structured-mode bytes) as produced by cloudevents.Publisher. The provider
// decodes the event, applies config-level extension overrides, and writes it
// to SinkURL in either binary or structured HTTP mode. A filtered event
// (EventTypes mismatch) is a non-error, non-delivery: Send returns ("", nil).
func (p *CloudEventHTTP) Send(ctx context.Context, job domain.NotificationJob) (string, error) {
	ctx, span := cloudEventHTTPTracer.Start(ctx, "cloudevent_http.send")
	defer span.End()

	var cfg CloudEventHTTPConfig
	if err := resolveConfig(job, p.decrypt, &cfg); err != nil {
		span.SetStatus(codes.Error, "resolve config")
		span.RecordError(err)
		return "", fmt.Errorf("resolve cloudevent_http config: %w", err)
	}

	if err := validateCloudEventHTTPConfig(&cfg); err != nil {
		span.SetStatus(codes.Error, "invalid config")
		span.RecordError(err)
		return "", err
	}
	if u, perr := url.Parse(cfg.SinkURL); perr == nil {
		span.SetAttributes(attribute.String("sink.host", u.Host))
	}

	// Parse the canonical CloudEvent JSON from job.Message.
	if job.Message == "" {
		err := fmt.Errorf("cloudevent_http: job.Message is empty (expected CloudEvent JSON)")
		span.SetStatus(codes.Error, "empty message")
		span.RecordError(err)
		return "", err
	}
	var ev ce.Event
	if err := json.Unmarshal([]byte(job.Message), &ev); err != nil {
		span.SetStatus(codes.Error, "parse event")
		span.RecordError(err)
		return "", fmt.Errorf("cloudevent_http: parse CloudEvent: %w", err)
	}
	if err := ev.Validate(); err != nil {
		span.SetStatus(codes.Error, "invalid event")
		span.RecordError(err)
		return "", fmt.Errorf("cloudevent_http: invalid CloudEvent: %w", err)
	}

	span.SetAttributes(
		attribute.String("ce.type", ev.Type()),
		attribute.String("ce.id", ev.ID()),
	)

	// Filter by event type using path.Match globs.
	if len(cfg.EventTypes) > 0 && !cloudevents.MatchAnyGlob(ev.Type(), cfg.EventTypes) {
		return "", nil
	}

	// Apply extension overrides.
	for k, v := range cfg.CEOverrides {
		ev.SetExtension(k, v)
	}

	req, err := buildCloudEventRequest(ctx, cfg, ev)
	if err != nil {
		span.SetStatus(codes.Error, "build request")
		span.RecordError(err)
		p.recordEmit(ctx, ev.Type(), "failure")
		return "", err
	}

	if cfg.Auth.Type == "oidc" {
		token, terr := p.fetchOIDCToken(ctx, cfg)
		if terr != nil {
			span.SetStatus(codes.Error, "oidc token fetch")
			span.RecordError(terr)
			p.recordEmit(ctx, ev.Type(), "failure")
			// IdP failures are transient, wrap as retryable so the worker
			// re-queues the send.
			return "", fmt.Errorf("cloudevent_http: retryable oidc token fetch: %w", terr)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Record the outbound event so a reply carrying ce_alertref can correlate
	// back to it. Non-fatal: if the registry is unavailable, the reply will
	// be rejected as an unknown alertref at dispatch time.
	if p.outboundRegistry != nil {
		_ = p.outboundRegistry.Record(ctx, ev, job.OrgID, job.Channel)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		span.SetStatus(codes.Error, "http do")
		span.RecordError(err)
		p.recordEmit(ctx, ev.Type(), "failure")
		return "", fmt.Errorf("cloudevent_http: POST %s: %w", cfg.SinkURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	span.SetAttributes(attribute.Int("http.status_code", resp.StatusCode))

	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))

	// Log reply events and, when a dispatcher is configured, hand the reply
	// off for acted-on handling (Phase 2+).
	p.handleReply(ctx, resp, bodyBytes, cfg, job.OrgID, job.Channel)

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		p.recordEmit(ctx, ev.Type(), "success")
		return ev.ID(), nil
	case resp.StatusCode == http.StatusRequestTimeout,
		resp.StatusCode == http.StatusTooManyRequests,
		resp.StatusCode >= 500:
		p.recordEmit(ctx, ev.Type(), "failure")
		err := fmt.Errorf("cloudevent_http: sink returned retryable status %d: %s", resp.StatusCode, truncate(bodyBytes, 256))
		span.SetStatus(codes.Error, "retryable status")
		return "", err
	case resp.StatusCode >= 400:
		p.recordEmit(ctx, ev.Type(), "failure")
		err := fmt.Errorf("cloudevent_http: sink returned permanent status %d: %s", resp.StatusCode, truncate(bodyBytes, 256))
		span.SetStatus(codes.Error, "permanent status")
		return "", err
	default:
		p.recordEmit(ctx, ev.Type(), "failure")
		err := fmt.Errorf("cloudevent_http: sink returned unexpected status %d: %s", resp.StatusCode, truncate(bodyBytes, 256))
		span.SetStatus(codes.Error, "unexpected status")
		return "", err
	}
}

// recordEmit is a nil-safe helper recording the emit outcome for the
// cloudevent_http channel.
func (p *CloudEventHTTP) recordEmit(ctx context.Context, ceType, result string) {
	if p == nil || p.metrics == nil || p.metrics.CloudEventsEmitted == nil {
		return
	}
	p.metrics.CloudEventsEmitted.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", ceType),
		attribute.String("channel", "cloudevent_http"),
		attribute.String("result", result),
	))
}

// metricLabelUnknownReplyType is the bucket label used for any sink
// `Ce-Type` value that does not appear in the registered reply-catalog.
// All unknown values collapse into this single label so an authenticated
// sink cannot inflate OTel metric cardinality by returning arbitrary
// type strings (R2-H7).
const metricLabelUnknownReplyType = "_unknown"

// knownReplyTypes is the set of registered reply CloudEvent type strings
// (the catalog from internal/cloudevents/types). Anything outside this
// allowlist is recorded under metricLabelUnknownReplyType so a malicious
// sink cannot pin O(N) unique strings as metric labels.
//
// Keep in sync with handlers.RegisterAll: every Type() registered there
// must appear here, and only those.
var knownReplyTypes = map[string]struct{}{
	types.TypeReplyAlertAckWithContextV1:     {},
	types.TypeReplyAlertClaimedV1:            {},
	types.TypeReplyAlertDeduplicatedV1:       {},
	types.TypeReplyAlertEnrichedV1:           {},
	types.TypeReplyAlertEscalatedV1:          {},
	types.TypeReplyAlertLinkedV1:             {},
	types.TypeReplyAlertOwnershipV1:          {},
	types.TypeReplyAlertRemediationResultV1:  {},
	types.TypeReplyAlertRemediationStartedV1: {},
	types.TypeReplyAlertRouteV1:              {},
	types.TypeReplyAlertStatusPageV1:         {},
	types.TypeReplyAlertSuppressedV1:         {},
	types.TypeReplyMonitorDeregisterV1:       {},
}

// bucketReplyTypeLabel returns the value to use for the "type" label on
// the CloudEventsReplyReceived metric. Sink-supplied Ce-Type strings are
// allowlisted; unknown values collapse to metricLabelUnknownReplyType to
// bound metric cardinality (R2-H7).
//
// Empty input is also bucketed under "_unknown" so an absent Ce-Type
// header doesn't produce an empty-string label.
func bucketReplyTypeLabel(ceType string) string {
	if ceType == "" {
		return metricLabelUnknownReplyType
	}
	if _, ok := knownReplyTypes[ceType]; ok {
		return ceType
	}
	return metricLabelUnknownReplyType
}

// handleReply logs a structured entry for every CloudEvent reply, records the
// `yipyap.cloudevents.reply_received` metric, and, when a dispatcher is
// configured, hands the reply off for validation and handler invocation.
//
// The `accepted` label on the metric reflects the dispatcher's outcome:
//
//   - "true"  when the dispatcher returned accepted=true (handler ran)
//   - "false" otherwise (log-only, opt-out, validation failure, or no dispatcher)
func (p *CloudEventHTTP) handleReply(ctx context.Context, resp *http.Response, body []byte, cfg CloudEventHTTPConfig, orgID, channelID string) {
	logReplyEvent(resp, body)
	if p == nil {
		return
	}

	ceType, alertref, ok := inspectReply(resp, body)
	if !ok {
		return
	}

	accepted := false
	if p.replyDispatcher != nil {
		ev, evOK := parseReplyEvent(resp, body)
		if evOK {
			dispatcherCfg := replyDispatcherConfigFrom(cfg)
			// Tier gating (R2-C1): a non-nil resolver overrides the
			// channel-configured RateLimit with the org's per-tier cap.
			// FOSS builds leave the resolver nil so the package default
			// applies.
			if p.replyTierResolver != nil {
				if cap := p.replyTierResolver(ctx, orgID); cap > 0 {
					dispatcherCfg.RateLimit = cap
				}
			}
			// sub is unavailable at this layer, the OIDC auth on outbound
			// identifies yipyap to the sink, not the sink to yipyap. A
			// future hop (EventPolicy-style inbound reply auth) can
			// populate this.
			acc, err := p.replyDispatcher.DispatchForChannel(ctx, ev, dispatcherCfg, orgID, channelID, "")
			if err != nil {
				slog.Warn("cloudevent_http: reply dispatch rejected",
					"ce-id", ev.ID(),
					"ce-type", ev.Type(),
					"error", err,
				)
			}
			accepted = acc
		}
	}

	if p.metrics == nil || p.metrics.CloudEventsReplyReceived == nil {
		return
	}
	valid := "false"
	if alertref != "" {
		valid = "true"
	}
	acceptedLabel := "false"
	if accepted {
		acceptedLabel = "true"
	}
	// R2-H7: bucket sink-supplied Ce-Type into the catalog allowlist so an
	// authenticated attacker cannot inflate metric cardinality with arbitrary
	// type strings.
	p.metrics.CloudEventsReplyReceived.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", bucketReplyTypeLabel(ceType)),
		attribute.String("valid", valid),
		attribute.String("accepted", acceptedLabel),
	))
}

// parseReplyEvent decodes a full ce.Event from the sink response. Supports
// both structured-mode response bodies (application/cloudevents+json) and
// binary-mode responses (ce-* headers + body as data). Returns false when
// the response is not a CloudEvent.
func parseReplyEvent(resp *http.Response, body []byte) (ce.Event, bool) {
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "application/cloudevents+json") && len(body) > 0 {
		var ev ce.Event
		if err := json.Unmarshal(body, &ev); err == nil {
			return ev, true
		}
		return ce.Event{}, false
	}

	// Binary mode: require at least ce-id + ce-type headers.
	id := resp.Header.Get("Ce-Id")
	typ := resp.Header.Get("Ce-Type")
	if id == "" || typ == "" {
		return ce.Event{}, false
	}
	ev := ce.NewEvent()
	specVersion := resp.Header.Get("Ce-Specversion")
	if specVersion == "" {
		specVersion = "1.0"
	}
	ev.SetSpecVersion(specVersion)
	ev.SetID(id)
	ev.SetType(typ)
	if v := resp.Header.Get("Ce-Source"); v != "" {
		ev.SetSource(v)
	}
	if v := resp.Header.Get("Ce-Subject"); v != "" {
		ev.SetSubject(v)
	}
	if v := resp.Header.Get("Ce-Alertref"); v != "" {
		ev.SetExtension("alertref", v)
	}
	if ct != "" && len(body) > 0 {
		_ = ev.SetData(ct, body)
	}
	return ev, true
}

// replyDispatcherConfigFrom extracts the dispatcher-facing view of a channel
// config. A nil AcceptedReplies means "deny all" (Phase-1 log-only preserved).
func replyDispatcherConfigFrom(cfg CloudEventHTTPConfig) reply.DispatcherConfig {
	if cfg.AcceptedReplies == nil {
		return reply.DispatcherConfig{}
	}
	return reply.DispatcherConfig{
		AcceptedReplyTypes:    cfg.AcceptedReplies.Types,
		RateLimit:             cfg.AcceptedReplies.RateLimit,
		TrustElevated:         cfg.AcceptedReplies.TrustElevated,
		AllowDeregister:       cfg.AcceptedReplies.AllowDeregister,
		MaxSuppressSeconds:    cfg.AcceptedReplies.MaxSuppressSeconds,
		MaxRemediationSeconds: cfg.AcceptedReplies.MaxRemediationSeconds,
		MaxRouteDuration:      cfg.AcceptedReplies.MaxRouteDurationSeconds,
	}
}

// validateCloudEventHTTPConfig applies defaults and rejects invalid values.
// Modifies cfg in place (for Mode defaulting).
//
// Host checking here is scheme-agnostic and applies to http:// AND https://
// URLs (H-3). Prior to this, https://169.254.169.254, https://10.x, etc.
// slipped through because the https branch unconditionally returned OK.
func validateCloudEventHTTPConfig(cfg *CloudEventHTTPConfig) error {
	if cfg.SinkURL == "" {
		return fmt.Errorf("cloudevent_http: sink_url is required")
	}
	u, err := url.Parse(cfg.SinkURL)
	if err != nil {
		return fmt.Errorf("cloudevent_http: invalid sink_url: %w", err)
	}
	switch u.Scheme {
	case "http", "https":
		// Scheme is fine; fall through to host check.
	default:
		return fmt.Errorf("cloudevent_http: unsupported scheme %q; must be https (or http for localhost)", u.Scheme)
	}
	// Scheme-agnostic host check: reject private/loopback/link-local IPs
	// regardless of whether the sink claims to be https. Localhost and
	// the control-plane allow-private toggle are the two escape hatches.
	// isPrivateHost resolves DNS and rejects IPv4+IPv6 private ranges
	// including IPv6 ULA fc00::/7 and link-local fe80::/10.
	if !isLocalhost(u.Host) && !checker.AllowPrivateControlPlaneTargets {
		if blocked, why := isPrivateHost(u.Hostname()); blocked {
			return fmt.Errorf("cloudevent_http: sink_url host %q resolves to private range (%s)", u.Hostname(), why)
		}
	}

	if cfg.Mode == "" {
		cfg.Mode = "binary"
	}
	if cfg.Mode != "binary" && cfg.Mode != "structured" {
		return fmt.Errorf("cloudevent_http: invalid mode %q; must be 'binary' or 'structured'", cfg.Mode)
	}

	switch cfg.Auth.Type {
	case "", "none":
		// OK.
	case "oidc":
		if cfg.Auth.OIDC == nil {
			return fmt.Errorf("cloudevent_http: auth.oidc: token_url, client_id, client_secret required")
		}
		o := cfg.Auth.OIDC
		if o.TokenURL == "" || o.ClientID == "" || o.ClientSecret == "" {
			return fmt.Errorf("cloudevent_http: auth.oidc: token_url, client_id, client_secret required")
		}
	default:
		return fmt.Errorf("cloudevent_http: unknown auth.type %q", cfg.Auth.Type)
	}

	return nil
}

// isLocalhost reports whether host (as returned from url.URL.Host, possibly
// including a port) is one of the canonical loopback identifiers.
func isLocalhost(host string) bool {
	h := host
	if i := strings.LastIndex(h, ":"); i > 0 && !strings.Contains(h[i:], "]") {
		// Strip port (but not when host is "[::1]:NNNN", handled below).
		h = h[:i]
	}
	// Normalise "[::1]" with or without port.
	h = strings.TrimPrefix(h, "[")
	h = strings.TrimSuffix(h, "]")
	switch h {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}

// buildCloudEventRequest builds the HTTP request for the given event and mode.
func buildCloudEventRequest(ctx context.Context, cfg CloudEventHTTPConfig, ev ce.Event) (*http.Request, error) {
	switch cfg.Mode {
	case "structured":
		body, err := json.Marshal(ev)
		if err != nil {
			return nil, fmt.Errorf("cloudevent_http: marshal structured event: %w", err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.SinkURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("cloudevent_http: build request: %w", err)
		}
		req.Header.Set("Content-Type", "application/cloudevents+json")
		return req, nil

	case "binary":
		data := ev.Data()
		dct := ev.DataContentType()
		if dct == "" {
			dct = "application/json"
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.SinkURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("cloudevent_http: build request: %w", err)
		}
		req.Header.Set("Content-Type", dct)
		req.Header.Set("Ce-Specversion", ev.SpecVersion())
		req.Header.Set("Ce-Id", ev.ID())
		req.Header.Set("Ce-Source", ev.Source())
		req.Header.Set("Ce-Type", ev.Type())
		if !ev.Time().IsZero() {
			req.Header.Set("Ce-Time", ev.Time().UTC().Format(time.RFC3339Nano))
		}
		if subj := ev.Subject(); subj != "" {
			req.Header.Set("Ce-Subject", subj)
		}
		for k, v := range ev.Extensions() {
			s, err := formatExtension(v)
			if err != nil {
				return nil, fmt.Errorf("cloudevent_http: format extension %q: %w", k, err)
			}
			req.Header.Set("Ce-"+canonicalExtHeader(k), s)
		}
		return req, nil

	default:
		return nil, fmt.Errorf("cloudevent_http: unsupported mode %q", cfg.Mode)
	}
}

// canonicalExtHeader converts an extension name to the canonical HTTP header
// suffix. http.Header.Set already canonicalises keys; this just capitalises
// the first letter so logs show nice `Ce-Environment: prod` rather than
// `Ce-environment`.
func canonicalExtHeader(name string) string {
	if name == "" {
		return name
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// formatExtension stringifies a CloudEvent extension attribute value for HTTP
// header serialisation. CE extensions are typically strings; fall back to fmt
// for numeric/bool/time values.
func formatExtension(v any) (string, error) {
	switch x := v.(type) {
	case string:
		return x, nil
	case fmt.Stringer:
		return x.String(), nil
	case bool:
		if x {
			return "true", nil
		}
		return "false", nil
	case nil:
		return "", nil
	default:
		return fmt.Sprintf("%v", x), nil
	}
}

// logReplyEvent inspects the sink response and, if it looks like a CloudEvent
// (structured via Content-Type, or binary via Ce-* headers), logs the
// identifying attributes. Phase 1 does not act on replies (Task 1.10).
func logReplyEvent(resp *http.Response, body []byte) {
	contentType := resp.Header.Get("Content-Type")
	isStructured := strings.HasPrefix(contentType, "application/cloudevents+json")
	hasBinary := resp.Header.Get("Ce-Id") != "" && resp.Header.Get("Ce-Type") != ""
	if !isStructured && !hasBinary {
		return
	}

	var (
		ceID, ceType, ceSource, ceAlertref string
	)
	if isStructured && len(body) > 0 {
		var ev ce.Event
		if err := json.Unmarshal(body, &ev); err == nil {
			ceID = ev.ID()
			ceType = ev.Type()
			ceSource = ev.Source()
			if raw, ok := ev.Extensions()["alertref"]; ok {
				if s, err := formatExtension(raw); err == nil {
					ceAlertref = s
				}
			}
		}
	}
	if hasBinary {
		if ceID == "" {
			ceID = resp.Header.Get("Ce-Id")
		}
		if ceType == "" {
			ceType = resp.Header.Get("Ce-Type")
		}
		if ceSource == "" {
			ceSource = resp.Header.Get("Ce-Source")
		}
		if ceAlertref == "" {
			ceAlertref = resp.Header.Get("Ce-Alertref")
		}
	}

	slog.Info("reply event received",
		"channel", "cloudevent_http",
		"ce-id", ceID,
		"ce-type", ceType,
		"ce-source", ceSource,
		"ce-alertref", ceAlertref,
	)
}

// inspectReply returns the (ceType, alertref, isReply) tuple for a sink
// response that looks like a CloudEvent. isReply is false when the response
// has no CloudEvent markers at all (Content-Type or Ce-* headers).
func inspectReply(resp *http.Response, body []byte) (string, string, bool) {
	contentType := resp.Header.Get("Content-Type")
	isStructured := strings.HasPrefix(contentType, "application/cloudevents+json")
	hasBinary := resp.Header.Get("Ce-Id") != "" && resp.Header.Get("Ce-Type") != ""
	if !isStructured && !hasBinary {
		return "", "", false
	}

	var ceType, ceAlertref string
	if isStructured && len(body) > 0 {
		var ev ce.Event
		if err := json.Unmarshal(body, &ev); err == nil {
			ceType = ev.Type()
			if raw, ok := ev.Extensions()["alertref"]; ok {
				if s, err := formatExtension(raw); err == nil {
					ceAlertref = s
				}
			}
		}
	}
	if hasBinary {
		if ceType == "" {
			ceType = resp.Header.Get("Ce-Type")
		}
		if ceAlertref == "" {
			ceAlertref = resp.Header.Get("Ce-Alertref")
		}
	}
	return ceType, ceAlertref, true
}

// fetchOIDCToken returns a bearer token for the given OIDC config, reusing
// the provider's shared token cache so multiple sends can share a token.
// Validation (non-empty token_url/client_id/client_secret) has already run
// in validateCloudEventHTTPConfig.
//
// Uses the same SSRF-guarded transport as the outbound sink request so
// token_url is also protected against DNS-rebinding at send time (H-4):
// an attacker-controlled evil.example.com that resolved to a public A at
// create-time cannot flip to 169.254.169.254 mid-flight.
func (p *CloudEventHTTP) fetchOIDCToken(ctx context.Context, cfg CloudEventHTTPConfig) (string, error) {
	o := cfg.Auth.OIDC
	client := oidc.NewClient(oidc.ClientConfig{
		Issuer:       o.Issuer,
		TokenURL:     o.TokenURL,
		ClientID:     o.ClientID,
		ClientSecret: o.ClientSecret,
		Audience:     o.Audience,
		Scopes:       o.Scopes,
	}, p.tokenCache, oidc.WithHTTPClient(newSSRFGuardedHTTPClient(30*time.Second)))
	return client.Token(ctx)
}

// truncate returns b as a string, truncating to max bytes for log/error use.
func truncate(b []byte, max int) string {
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "..."
}
