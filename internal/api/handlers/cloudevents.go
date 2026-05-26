package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/oklog/ulid/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"

	ce "github.com/cloudevents/sdk-go/v2"
	cebinding "github.com/cloudevents/sdk-go/v2/binding"
	cehttp "github.com/cloudevents/sdk-go/v2/protocol/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/oidc"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/telemetry"
)

var cloudEventsIngestTracer = otel.Tracer("github.com/YipYap-run/YipYap-FOSS/internal/api/handlers/cloudevents")

// maxIngestBodyBytes is the upper bound on inbound request size (binary data,
// single structured envelope, or batched envelope). 4 MiB comfortably holds
// hundreds of small events in a batch while staying within memory limits.
const maxIngestBodyBytes = 4 << 20

// maxIngestBatchLen caps the number of events in a single batched
// request (application/cloudevents-batch+json). 4 MiB of minimal-envelope
// events is ~40,000 entries, each driving a synchronous DB TrySeen +
// Create round trip. Without this cap a single 60 req/min rate-limited
// caller produces 2.4M DB round trips per minute. See H-17.
const maxIngestBatchLen = 256

// ingestIdempotencyTTL is how long we remember ce-id per monitor to suppress
// duplicates. 24h matches the CloudEvents ecosystem convention and pairs with
// the retention window used by the background janitor.
const ingestIdempotencyTTL = 24 * time.Hour

// ingestAuthSettingKey is the OrgSettings key under which per-org
// CloudEvents ingest auth configuration is stored. See ingestAuthConfig
// for the schema.
const ingestAuthSettingKey = "cloudevents.ingest.auth"

// ingestAuthConfig is the parsed form of the JSON payload stored in
// OrgSettings under ingestAuthSettingKey.
type ingestAuthConfig struct {
	Type string                `json:"type"`
	OIDC *ingestAuthOIDCConfig `json:"oidc,omitempty"`
}

// ingestAuthOIDCConfig carries the OIDC-specific parameters used to
// construct an *oidc.Verifier when Type == "oidc".
type ingestAuthOIDCConfig struct {
	Issuer    string   `json:"issuer"`
	JWKSURL   string   `json:"jwks_url,omitempty"`
	Audiences []string `json:"audiences"`
}

// oidcVerifierEntry caches a Verifier alongside the JSON blob it was
// built from so we can rebuild only when the config changes.
// The underlying *oidc.Verifier is fetched through a package-level
// VerifierCache so multiple orgs sharing an Issuer/JWKSURL reuse the
// same RemoteKeySet, and the verifier itself expires on TTL, keeping
// key rotation responsive when an IdP swaps JWKs mid-session.
type oidcVerifierEntry struct {
	configJSON string
	verifier   *oidc.Verifier
}

// CloudEventHandler handles inbound CloudEvents ingestion via
// POST /api/v1/cloudevents/ingest/{token}.
type CloudEventHandler struct {
	store   store.Store
	metrics *telemetry.Metrics

	// verifierCache maps orgID → the currently-cached verifier and its
	// source JSON. Guarded by verifierCacheMu. A simple mutex is fine:
	// ingest QPS per org is low and Verifier construction is cheap (it
	// performs no network I/O until first Verify).
	//
	// Verifier instances are sourced from verifierPool (a package-level
	// TTL cache keyed by (issuer, jwks_url, audiences)) so two orgs
	// sharing an IdP share one RemoteKeySet, AND so the underlying
	// verifier is rebuilt on its TTL, bounding staleness when an IdP
	// rotates keys in a way RemoteKeySet's Cache-Control refresh
	// doesn't catch.
	verifierCacheMu sync.Mutex
	verifierCache   map[string]*oidcVerifierEntry
	verifierPool    *oidc.VerifierCache
}

// CloudEventHandlerOption configures a CloudEventHandler.
type CloudEventHandlerOption func(*CloudEventHandler)

// WithCloudEventHandlerMetrics wires telemetry.Metrics into the handler for
// ingest counters and duplicate suppression accounting. Nil disables
// recording.
func WithCloudEventHandlerMetrics(m *telemetry.Metrics) CloudEventHandlerOption {
	return func(h *CloudEventHandler) { h.metrics = m }
}

// NewCloudEventHandler returns a new CloudEventHandler.
func NewCloudEventHandler(s store.Store, opts ...CloudEventHandlerOption) *CloudEventHandler {
	h := &CloudEventHandler{
		store:         s,
		verifierCache: make(map[string]*oidcVerifierEntry),
		verifierPool:  oidc.NewVerifierCache(oidc.DefaultVerifierCacheTTL),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(h)
		}
	}
	return h
}

// SetMetrics wires (or replaces) the telemetry bundle on an existing handler.
// Safe to call with nil to disable recording.
func (h *CloudEventHandler) SetMetrics(m *telemetry.Metrics) {
	if h == nil {
		return
	}
	h.metrics = m
}

// recordIngested is a nil-safe helper for the ingest-outcome counter.
func (h *CloudEventHandler) recordIngested(ctx context.Context, ceType, result string) {
	if h == nil || h.metrics == nil || h.metrics.CloudEventsIngested == nil {
		return
	}
	h.metrics.CloudEventsIngested.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", ceType),
		attribute.String("result", result),
	))
}

// recordDuplicate is a nil-safe helper for the duplicate-suppressed counter.
func (h *CloudEventHandler) recordDuplicate(ctx context.Context, ceType string) {
	if h == nil || h.metrics == nil || h.metrics.CloudEventsIngestDuplicates == nil {
		return
	}
	h.metrics.CloudEventsIngestDuplicates.Add(ctx, 1, metric.WithAttributes(
		attribute.String("type", ceType),
	))
}

// Ingest accepts CloudEvents in binary, structured, or batched mode and applies
// them to the monitor identified by the integration-key token in the URL.
func (h *CloudEventHandler) Ingest(w http.ResponseWriter, r *http.Request) {
	ctx, span := cloudEventsIngestTracer.Start(r.Context(), "cloudevents.ingest")
	defer span.End()

	// statusCode tracks the eventual response status so the span can
	// record it and so rejected-outcome metrics are attributed correctly.
	statusCode := http.StatusOK
	rejectType := ""
	finalize := func() {
		span.SetAttributes(attribute.Int("http.status_code", statusCode))
		if statusCode >= 400 && statusCode < 500 {
			h.recordIngested(ctx, rejectType, "rejected")
		} else if statusCode >= 500 {
			h.recordIngested(ctx, rejectType, "error")
		}
	}

	token := chi.URLParam(r, "token")
	if token == "" {
		statusCode = http.StatusUnauthorized
		span.SetStatus(codes.Error, "missing token")
		errorResponse(w, statusCode, "invalid token")
		finalize()
		return
	}

	monitor, err := h.store.Monitors().GetByIntegrationKey(ctx, token)
	if err != nil || monitor == nil || monitor.IntegrationKey == "" || monitor.IntegrationKey != token {
		statusCode = http.StatusUnauthorized
		span.SetStatus(codes.Error, "invalid token")
		errorResponse(w, statusCode, "invalid token")
		finalize()
		return
	}
	span.SetAttributes(attribute.String("monitor.id", monitor.ID))

	// Per-org OIDC bearer verification. When no settings row exists or
	// type == "none", we fall through and preserve the Phase-1 anonymous
	// behaviour (integration-key-only). On type == "oidc" the verifier
	// decides 200/401/403/503 based on the token and our mapping.
	authCfg, err := h.loadIngestConfig(ctx, monitor.OrgID)
	if err != nil {
		statusCode = http.StatusInternalServerError
		span.SetStatus(codes.Error, "load ingest config")
		span.RecordError(err)
		errorResponse(w, statusCode, "ingest config error")
		finalize()
		return
	}

	var verifiedSub string
	if authCfg.Type == "oidc" {
		bearer, ok := bearerFromRequest(r)
		if !ok {
			statusCode = http.StatusUnauthorized
			span.SetStatus(codes.Error, "missing bearer")
			errorResponse(w, statusCode, "bearer token required")
			finalize()
			return
		}
		v, err := h.resolveVerifier(monitor.OrgID, authCfg)
		if err != nil {
			statusCode = http.StatusInternalServerError
			span.SetStatus(codes.Error, "verifier init")
			span.RecordError(err)
			errorResponse(w, statusCode, "oidc config error")
			finalize()
			return
		}
		tok, err := v.Verify(ctx, bearer)
		if err != nil {
			statusCode = statusForVerifyError(err)
			span.SetStatus(codes.Error, "oidc verify")
			span.RecordError(err)
			errorResponse(w, statusCode, "oidc verification failed")
			finalize()
			return
		}
		verifiedSub = tok.Subject
		span.SetAttributes(attribute.String("oidc.sub", verifiedSub))
	}

	// Cap body size up-front. MaxBytesReader triggers an error when the limit
	// is exceeded during Read; we read the full body here so we can re-hand
	// it to either structured/batched JSON or binary-mode decoders.
	r.Body = http.MaxBytesReader(w, r.Body, maxIngestBodyBytes)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		if isMaxBytesError(err) {
			statusCode = http.StatusRequestEntityTooLarge
			span.SetStatus(codes.Error, "body too large")
			errorResponse(w, statusCode, "request body too large")
			finalize()
			return
		}
		statusCode = http.StatusBadRequest
		span.SetStatus(codes.Error, "read body")
		span.RecordError(err)
		errorResponse(w, statusCode, "failed to read body")
		finalize()
		return
	}

	contentType := r.Header.Get("Content-Type")
	mediaType := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])

	events, err := decodeCloudEvents(ctx, r, bodyBytes, mediaType)
	if err != nil {
		statusCode = http.StatusBadRequest
		span.SetStatus(codes.Error, "decode")
		span.RecordError(err)
		errorResponse(w, statusCode, "invalid cloudevent: "+err.Error())
		finalize()
		return
	}
	if len(events) == 0 {
		statusCode = http.StatusBadRequest
		span.SetStatus(codes.Error, "no events")
		errorResponse(w, statusCode, "no events in request")
		finalize()
		return
	}
	// H-17: reject oversized batches. A single caller that packs 4 MiB
	// of minimal envelopes into one array could drive ~40k DB round
	// trips per request; cap at maxIngestBatchLen events.
	if len(events) > maxIngestBatchLen {
		statusCode = http.StatusRequestEntityTooLarge
		span.SetStatus(codes.Error, "batch too large")
		errorResponse(w, statusCode, "batch too large: max "+strconv.Itoa(maxIngestBatchLen)+" events per request")
		finalize()
		return
	}

	// Annotate the span with the first event's ce.type/ce.id once decoded.
	// For batched requests this is the leading envelope; individual per-event
	// rejection metrics still carry the specific ce.type via rejectType.
	if len(events) > 0 {
		span.SetAttributes(
			attribute.String("ce.type", events[0].Type()),
			attribute.String("ce.id", events[0].ID()),
		)
	}

	for i := range events {
		ev := &events[i]
		if ev.SpecVersion() != "1.0" {
			statusCode = http.StatusBadRequest
			rejectType = ev.Type()
			span.SetStatus(codes.Error, "unsupported specversion")
			errorResponse(w, statusCode, "unsupported specversion: "+ev.SpecVersion())
			finalize()
			return
		}
		if err := ev.Validate(); err != nil {
			statusCode = http.StatusBadRequest
			rejectType = ev.Type()
			span.SetStatus(codes.Error, "invalid event")
			span.RecordError(err)
			errorResponse(w, statusCode, "invalid cloudevent: "+err.Error())
			finalize()
			return
		}
		if !isIngestibleType(ev.Type()) {
			statusCode = http.StatusUnprocessableEntity
			rejectType = ev.Type()
			span.SetStatus(codes.Error, "type not allowed")
			errorResponse(w, statusCode, "type not allowed: "+ev.Type())
			finalize()
			return
		}
	}

	// Apply events in order. A 500 partway through means some events were
	// recorded, that's acceptable; the ce-id dedupe window lets the sender
	// retry without creating duplicates.
	for i := range events {
		ev := &events[i]
		duplicate, err := h.apply(ctx, monitor, ev, verifiedSub)
		if err != nil {
			if errors.Is(err, errBadEventData) {
				statusCode = http.StatusBadRequest
				rejectType = ev.Type()
				span.SetStatus(codes.Error, "bad event data")
				span.RecordError(err)
				errorResponse(w, statusCode, err.Error())
				finalize()
				return
			}
			statusCode = http.StatusInternalServerError
			rejectType = ev.Type()
			span.SetStatus(codes.Error, "storage")
			span.RecordError(err)
			errorResponse(w, statusCode, "storage error")
			finalize()
			return
		}
		if duplicate {
			span.SetAttributes(attribute.Bool("ingest.duplicate", true))
			h.recordDuplicate(ctx, ev.Type())
			h.recordIngested(ctx, ev.Type(), "duplicate")
		} else {
			h.recordIngested(ctx, ev.Type(), "success")
		}
	}

	w.WriteHeader(http.StatusOK)
	finalize()
}

// errBadEventData signals a request-level (400) problem with event data
// contents after envelope validation has passed.
var errBadEventData = errors.New("bad event data")

// decodeCloudEvents reads `events` from the request, choosing the mode from
// the request's Content-Type: structured (application/cloudevents+json),
// batched (application/cloudevents-batch+json), or binary (anything else,
// driven by Ce-* headers).
func decodeCloudEvents(ctx context.Context, r *http.Request, body []byte, mediaType string) ([]ce.Event, error) {
	switch mediaType {
	case "application/cloudevents-batch+json":
		var evs []ce.Event
		if err := json.Unmarshal(body, &evs); err != nil {
			return nil, fmt.Errorf("batched decode: %w", err)
		}
		return evs, nil
	case "application/cloudevents+json":
		var ev ce.Event
		if err := json.Unmarshal(body, &ev); err != nil {
			return nil, fmt.Errorf("structured decode: %w", err)
		}
		return []ce.Event{ev}, nil
	default:
		// Binary mode: reconstruct a request view (with a rewindable body)
		// and hand to the sdk-go protocol/http adapter.
		binReq := r.Clone(ctx)
		binReq.Body = io.NopCloser(bytes.NewReader(body))
		msg := cehttp.NewMessageFromHttpRequest(binReq)
		ev, err := cebinding.ToEvent(ctx, msg)
		if err != nil {
			return nil, fmt.Errorf("binary decode: %w", err)
		}
		return []ce.Event{*ev}, nil
	}
}

func isIngestibleType(t string) bool {
	switch t {
	case types.TypeIngestHeartbeatV1, types.TypeIngestStatusV1:
		return true
	}
	return false
}

// apply persists one accepted event. Duplicates (per (monitor, ce-id) within
// TTL) are skipped without error. Returns duplicate=true when the event was
// suppressed by the idempotency ring.
func (h *CloudEventHandler) apply(ctx context.Context, monitor *domain.Monitor, ev *ce.Event, oidcSub string) (bool, error) {
	fresh, err := h.store.CloudEventIngest().TrySeen(ctx, monitor.ID, ev.ID(), ingestIdempotencyTTL)
	if err != nil {
		return false, fmt.Errorf("idempotency: %w", err)
	}
	if !fresh {
		return true, nil
	}

	now := time.Now().UTC()
	check := &domain.MonitorCheck{
		ID:        ulid.Make().String(),
		MonitorID: monitor.ID,
		CheckedAt: now,
	}

	switch ev.Type() {
	case types.TypeIngestHeartbeatV1:
		check.Status = domain.StatusUp
		check.Metadata = buildHeartbeatMetadata(ev, oidcSub)
	case types.TypeIngestStatusV1:
		var data types.IngestStatusV1Data
		if payload := ev.Data(); len(payload) > 0 {
			if err := json.Unmarshal(payload, &data); err != nil {
				return false, fmt.Errorf("%w: data: %v", errBadEventData, err)
			}
		}
		status, ok := parseIngestStatus(data.Status)
		if !ok {
			return false, fmt.Errorf("%w: status %q", errBadEventData, data.Status)
		}
		check.Status = status
		if data.Message != "" {
			check.Error = data.Message
		}
		check.Metadata = buildStatusMetadata(ev, data, oidcSub)
	default:
		// isIngestibleType gated this already.
		return false, fmt.Errorf("unexpected event type: %s", ev.Type())
	}

	if err := h.store.Checks().Create(ctx, check); err != nil {
		return false, err
	}
	return false, nil
}

func parseIngestStatus(s string) (domain.CheckStatus, bool) {
	switch s {
	case string(domain.StatusUp):
		return domain.StatusUp, true
	case string(domain.StatusDown):
		return domain.StatusDown, true
	case string(domain.StatusDegraded):
		return domain.StatusDegraded, true
	}
	return "", false
}

func buildHeartbeatMetadata(ev *ce.Event, oidcSub string) string {
	meta := map[string]any{
		"source":   ev.Source(),
		"event_id": ev.ID(),
	}
	if data := ev.Data(); len(data) > 0 {
		var payload map[string]any
		if err := json.Unmarshal(data, &payload); err == nil {
			for k, v := range payload {
				meta[k] = v
			}
		}
	}
	if oidcSub != "" {
		meta["oidc_sub"] = oidcSub
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(b)
}

func buildStatusMetadata(ev *ce.Event, d types.IngestStatusV1Data, oidcSub string) string {
	meta := map[string]any{
		"source":   ev.Source(),
		"event_id": ev.ID(),
		"status":   d.Status,
	}
	if d.Message != "" {
		meta["message"] = d.Message
	}
	if oidcSub != "" {
		meta["oidc_sub"] = oidcSub
	}
	b, err := json.Marshal(meta)
	if err != nil {
		return ""
	}
	return string(b)
}

// isMaxBytesError reports whether err came from http.MaxBytesReader hitting
// its limit. The stdlib exports *http.MaxBytesError in Go 1.19+.
func isMaxBytesError(err error) bool {
	var mbe *http.MaxBytesError
	return errors.As(err, &mbe)
}

// loadIngestConfig reads the per-org CloudEvents ingest auth config from
// OrgSettings. A missing row, empty payload, or "type": "none" all yield
// a config whose Type is "none" so the caller can branch simply. JSON
// that is present but malformed is returned as an error, the operator
// configured something broken, and silently falling through to no-auth
// would be a footgun.
func (h *CloudEventHandler) loadIngestConfig(ctx context.Context, orgID string) (*ingestAuthConfig, error) {
	cfg := &ingestAuthConfig{Type: "none"}
	if h == nil || h.store == nil || orgID == "" {
		return cfg, nil
	}
	// OrgSettings is not on the top-level Store interface; the concrete
	// backends (postgres/sqlite/mariadb) expose it via OrgSettingsProvider.
	// When the underlying store doesn't implement it we degrade to "no
	// auth configured" rather than failing, this keeps test doubles and
	// any future backends without settings support usable.
	provider, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		return cfg, nil
	}
	raw, err := provider.OrgSettings().Get(ctx, orgID, ingestAuthSettingKey)
	if err != nil {
		// The sqlite/postgres/mariadb implementations return a fmt.Errorf
		// "org setting not found: ..." on missing rows; treat that, and
		// only that, as absence. Any other error is unexpected and must
		// propagate so operators see DB outages rather than silent
		// auth-disabled drift.
		if strings.Contains(err.Error(), "org setting not found") {
			return cfg, nil
		}
		return nil, fmt.Errorf("org settings: %w", err)
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg, nil
	}
	var parsed ingestAuthConfig
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("ingest auth json: %w", err)
	}
	if parsed.Type == "" {
		parsed.Type = "none"
	}
	return &parsed, nil
}

// resolveVerifier returns a Verifier for orgID, rebuilding it when the
// underlying JSON has changed since the last cache entry. The caller is
// responsible for having already confirmed authCfg.Type == "oidc".
func (h *CloudEventHandler) resolveVerifier(orgID string, authCfg *ingestAuthConfig) (*oidc.Verifier, error) {
	if authCfg == nil || authCfg.OIDC == nil {
		return nil, fmt.Errorf("oidc config missing")
	}
	// Canonicalise the config JSON for equality comparison. Using
	// json.Marshal on the parsed struct avoids whitespace/key-order
	// sensitivity in the raw OrgSettings blob.
	key, err := json.Marshal(authCfg)
	if err != nil {
		return nil, fmt.Errorf("marshal auth cfg: %w", err)
	}
	canon := string(key)

	h.verifierCacheMu.Lock()
	defer h.verifierCacheMu.Unlock()

	if h.verifierCache == nil {
		h.verifierCache = make(map[string]*oidcVerifierEntry)
	}
	if entry, ok := h.verifierCache[orgID]; ok && entry.configJSON == canon {
		return entry.verifier, nil
	}

	// Route construction through the package-level TTL cache so two orgs
	// with identical OIDC config share a verifier (and its RemoteKeySet),
	// and so verifiers expire on TTL instead of sticking forever.
	cfg := oidc.VerifierConfig{
		Issuer:    authCfg.OIDC.Issuer,
		JWKSURL:   authCfg.OIDC.JWKSURL,
		Audiences: authCfg.OIDC.Audiences,
	}
	v := h.verifierPool.Get(cfg, func() *oidc.Verifier { return oidc.NewVerifier(cfg) })
	// When the config changes for this orgID we must drop the old pool
	// entry too, not just the per-org entry, otherwise a stale verifier
	// would linger in the pool until its TTL. Best-effort invalidate:
	// an unreachable-but-still-cached verifier just wastes a pool slot
	// and self-evicts on TTL.
	if entry, ok := h.verifierCache[orgID]; ok && entry.configJSON != canon {
		var prev ingestAuthConfig
		if err := json.Unmarshal([]byte(entry.configJSON), &prev); err == nil && prev.OIDC != nil {
			h.verifierPool.Invalidate(oidc.VerifierConfig{
				Issuer:    prev.OIDC.Issuer,
				JWKSURL:   prev.OIDC.JWKSURL,
				Audiences: prev.OIDC.Audiences,
			})
		}
	}
	h.verifierCache[orgID] = &oidcVerifierEntry{
		configJSON: canon,
		verifier:   v,
	}
	return v, nil
}

// bearerFromRequest returns the token from a "Authorization: Bearer …"
// header. Case-insensitive on the scheme. Returns ok=false when the
// header is missing or malformed.
func bearerFromRequest(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if h == "" {
		return "", false
	}
	const prefix = "bearer "
	if len(h) < len(prefix) || !strings.EqualFold(h[:len(prefix)], prefix) {
		return "", false
	}
	tok := strings.TrimSpace(h[len(prefix):])
	if tok == "" {
		return "", false
	}
	return tok, true
}

// statusForVerifyError maps an oidc.Verifier sentinel error to its HTTP
// status. Unknown errors default to 401: failing closed on
// authentication is the safe choice.
func statusForVerifyError(err error) int {
	switch {
	case errors.Is(err, oidc.ErrJWKSUnavailable):
		return http.StatusServiceUnavailable
	case errors.Is(err, oidc.ErrWrongIssuer),
		errors.Is(err, oidc.ErrWrongAudience):
		return http.StatusForbidden
	case errors.Is(err, oidc.ErrTokenExpired),
		errors.Is(err, oidc.ErrInvalidSignature),
		errors.Is(err, oidc.ErrMalformedToken):
		return http.StatusUnauthorized
	default:
		return http.StatusUnauthorized
	}
}
