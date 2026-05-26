package handlers

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

// CloudEventsStreamHandler serves GET /api/v1/cloudevents/stream.
//
// Phase-3 stream-mode transport: a long-lived NDJSON HTTP stream that
// delivers CloudEvents to the yipyap-knative-source binary as they arrive
// on the internal bus. The wire format is CloudEvents HTTP binding
// structured-mode with a batched content type, each event is one JSON
// object on its own line, flushed after each write for sub-5s latency.
//
// Shares a ceConsumerCache with CloudEventsPollHandler so a client that
// switches between poll and stream modes on the same (org, filter) sees
// a single durable cursor.
type CloudEventsStreamHandler struct {
	cache *ceConsumerCache
}

// NewCloudEventsStreamHandler returns a stream handler bound to the supplied
// bus. The handler builds a private consumer cache; callers that need
// poll+stream to share cursor state should use
// NewCloudEventsStreamHandlerWithCache.
func NewCloudEventsStreamHandler(b bus.Bus) *CloudEventsStreamHandler {
	return &CloudEventsStreamHandler{cache: newCEConsumerCache(b)}
}

// NewCloudEventsStreamHandlerWithCache returns a stream handler sharing the
// supplied consumer cache with other handlers (notably the poll handler).
func NewCloudEventsStreamHandlerWithCache(cache *ceConsumerCache) *CloudEventsStreamHandler {
	return &CloudEventsStreamHandler{cache: cache}
}

// Stream handles GET /api/v1/cloudevents/stream. It writes one JSON-encoded
// CloudEvent per line to the response body, flushing after each write. The
// stream continues until the client disconnects or the request context is
// cancelled.
//
// Response headers:
//
//	Content-Type: application/cloudevents-batch+json
//	Cache-Control: no-cache
//	X-Accel-Buffering: no
func (h *CloudEventsStreamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// The underlying ResponseWriter must support flushing so clients see
	// bytes as soon as we write them. chi's default mux wraps the stdlib
	// writer, which does implement Flusher, guard anyway.
	flusher, ok := w.(http.Flusher)
	if !ok {
		errorResponse(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	// Accept MULTIPLE `filter` query params and OR them: an event is streamed
	// when its type matches ANY glob. This lets a Knative source forward all
	// of spec.filter.types. Zero filters means no filtering; one filter is
	// exactly today's behaviour.
	filters := cleanFilters(r.URL.Query()["filter"])
	// `since` is accepted but advisory: the durable consumer keyed on the
	// (org, filter) sha256 is authoritative.
	_ = r.URL.Query().Get("since")

	// Canonicalize the filter set into one stable cache key so equivalent
	// sets, in any order, share a single durable consumer.
	pc, err := h.cache.ensure(r.Context(), claims.OrgID, canonicalFilter(filters))
	if err != nil {
		if errors.Is(err, ErrConsumerCapExceeded) {
			w.Header().Set("Retry-After", "60")
			errorResponse(w, http.StatusTooManyRequests, "consumer cap exceeded for org")
			return
		}
		errorResponse(w, http.StatusInternalServerError, "consumer subscribe failed")
		return
	}

	// Opt out of the parent http.Server.WriteTimeout: this is a long-lived
	// NDJSON stream and the stdlib's wall-clock write deadline would
	// forcibly tear the connection down at WriteTimeout (15s in production
	// main.go) regardless of whether bytes are flowing (R2-C2).
	// SetWriteDeadline(time.Time{}) clears the deadline for THIS request
	// only; other handlers retain the global timeout. Errors here (e.g.
	// underlying ResponseWriter doesn't expose the deadline) are
	// non-fatal, log and continue, accepting the WriteTimeout ceiling
	// as a graceful degradation.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Debug("cloudevents stream: clear write deadline failed",
			"err", err)
	}

	// Set headers BEFORE the first Flush. Once we flush, the status and
	// headers are latched on the wire.
	w.Header().Set("Content-Type", "application/cloudevents-batch+json")
	w.Header().Set("Cache-Control", "no-cache")
	// X-Accel-Buffering disables nginx response buffering on proxied
	// deployments, without it, nginx buffers entire responses and the
	// stream semantics collapse to "one big slow POST".
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// Flush headers so the client sees 200 + content-type even before any
	// event arrives. Corporate proxies are less likely to mangle a
	// connection that's clearly pushed its response line.
	flusher.Flush()

	ctx := r.Context()
	newline := []byte{'\n'}
	for {
		select {
		case <-ctx.Done():
			return
		case raw, ok := <-pc.buf:
			if !ok {
				return
			}
			// Apply the glob filters on the event type (OR). On no filter,
			// still validate the payload decodes as a CloudEvent so we
			// never push obviously malformed JSON on the wire.
			if len(filters) > 0 {
				var env struct {
					Type string `json:"type"`
				}
				if err := json.Unmarshal(raw, &env); err != nil {
					continue
				}
				if !cepub.MatchAnyGlob(env.Type, filters) {
					continue
				}
			} else {
				var ev ce.Event
				if err := json.Unmarshal(raw, &ev); err != nil {
					continue
				}
			}
			if _, err := w.Write(raw); err != nil {
				return
			}
			if _, err := w.Write(newline); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
