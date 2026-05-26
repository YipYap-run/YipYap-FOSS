package handlers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	ce "github.com/cloudevents/sdk-go/v2"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	cepub "github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
)

// pollDefaultMax is the default event count ceiling when `max` is absent or
// invalid on the incoming request.
const pollDefaultMax = 100

// pollMaxMax is the hard upper bound: requests asking for more events than
// this are clamped without error.
const pollMaxMax = 500

// pollDrainWindow is how long the handler waits collecting events before
// responding. Short enough to feel responsive for idle buses, long enough to
// pick up bursty batches without forcing clients to spin their poll interval.
const pollDrainWindow = 2 * time.Second

// CloudEventsPollHandler serves GET /api/v1/cloudevents/poll.
//
// Phase-3 poll-mode transport: a cursor-resumable HTTP endpoint that drains
// a bounded window of CloudEvents from the internal bus for the caller's
// org. The bus's durable pull-consumer semantics persist cursor state across
// polls; the `since` query parameter is accepted for forward compatibility
// but advisory, the durable consumer's server-side state is authoritative.
type CloudEventsPollHandler struct {
	cache *ceConsumerCache
	// cursorCounter backs the monotonic next_cursor we return. Clients
	// treat the cursor as opaque; we only need it to be different each
	// call so integrations that compare cursors see motion.
	cursorCounter atomic.Uint64
}

// NewCloudEventsPollHandler returns a poll handler bound to the supplied bus.
// The handler builds a fresh consumer cache for its own use; callers that
// need poll and stream modes to share cursor state should use
// NewCloudEventsPollHandlerWithCache.
func NewCloudEventsPollHandler(b bus.Bus) *CloudEventsPollHandler {
	return &CloudEventsPollHandler{cache: newCEConsumerCache(b)}
}

// NewCloudEventsPollHandlerWithCache returns a poll handler sharing the
// supplied consumer cache with other handlers (notably the stream handler).
// Sharing ensures a client that polls and streams on the same (org, filter)
// sees a single consistent cursor.
func NewCloudEventsPollHandlerWithCache(cache *ceConsumerCache) *CloudEventsPollHandler {
	return &CloudEventsPollHandler{cache: cache}
}

// Poll handles GET /api/v1/cloudevents/poll. Response shape:
//
//	{
//	  "events": [ /* full CloudEvent JSON envelopes */ ],
//	  "next_cursor": "opaque-string"
//	}
//
// `events` is always present (possibly empty), never null, so clients can
// rely on the shape without null-guards.
func (h *CloudEventsPollHandler) Poll(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return
	}

	// Accept MULTIPLE `filter` query params and OR them: an event is kept
	// when its type matches ANY glob. This lets a Knative source forward all
	// of spec.filter.types. Zero filters means no filtering; one filter is
	// exactly today's behaviour.
	filters := cleanFilters(r.URL.Query()["filter"])
	maxEvents := parsePollMax(r.URL.Query().Get("max"))
	// `since` is accepted but advisory in Phase-3: JetStream durable
	// consumer state (keyed on consumer name) is authoritative. We
	// deliberately don't introspect the cursor, clients treat it as opaque.
	_ = r.URL.Query().Get("since")

	// Canonicalize the filter set into one stable cache key so equivalent
	// sets, in any order, share a single durable consumer.
	pc, err := h.cache.ensure(r.Context(), claims.OrgID, canonicalFilter(filters))
	if err != nil {
		if errors.Is(err, ErrConsumerCapExceeded) {
			// Per-org cap hit (H-15). 60s is a conservative Retry-After
			// hint, idle TTL is 5min but a worst-case busy caller sees
			// a slot free well before that.
			w.Header().Set("Retry-After", "60")
			errorResponse(w, http.StatusTooManyRequests, "consumer cap exceeded for org")
			return
		}
		errorResponse(w, http.StatusInternalServerError, "consumer subscribe failed")
		return
	}

	drainCtx, cancel := context.WithTimeout(r.Context(), pollDrainWindow)
	defer cancel()

	raws := drainPullWindow(drainCtx, pc.buf, maxEvents)

	events := make([]json.RawMessage, 0, len(raws))
	for _, raw := range raws {
		if len(filters) > 0 {
			// Parse minimally to extract the type for the glob match.
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
			// Even without a filter, validate the payload decodes as a
			// CloudEvent envelope so we never emit obviously malformed
			// JSON to the client.
			var ev ce.Event
			if err := json.Unmarshal(raw, &ev); err != nil {
				continue
			}
		}
		events = append(events, json.RawMessage(raw))
	}

	cursor := h.nextCursor()
	resp := map[string]any{
		"events":      events,
		"next_cursor": cursor,
	}
	jsonResponse(w, http.StatusOK, resp)
}

// nextCursor produces a monotonic opaque cursor. Encoded as base64url of a
// tiny JSON object so future iterations can add fields without breaking the
// wire format.
func (h *CloudEventsPollHandler) nextCursor() string {
	n := h.cursorCounter.Add(1)
	payload, _ := json.Marshal(map[string]uint64{"seq": n})
	return base64.RawURLEncoding.EncodeToString(payload)
}

// parsePollMax clamps a user-supplied max value into [1, pollMaxMax], with
// pollDefaultMax as the fallback for missing/invalid input.
func parsePollMax(raw string) int {
	if raw == "" {
		return pollDefaultMax
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return pollDefaultMax
	}
	if n > pollMaxMax {
		return pollMaxMax
	}
	return n
}

// drainPullWindow reads up to max messages from the supplied buffered channel,
// returning as soon as max is reached or ctx expires, whichever first.
//
// The buffer is filled asynchronously by the long-lived subscription owned by
// ceConsumerCache; see ceConsumerCache.ensure for the wiring. We don't
// re-subscribe per call: durable-consumer state (JetStream) or the in-memory
// queue subscription (ChannelBus) must persist between polls, otherwise every
// poll restarts from the current tail and loses anything that arrived while
// the previous poll was returning its payload.
func drainPullWindow(ctx context.Context, buf <-chan []byte, max int) [][]byte {
	if max <= 0 {
		return nil
	}
	out := make([][]byte, 0, max)
	// idleGrace is how long we wait for *more* events after the first one
	// arrives. This keeps bursty batches together in the same response
	// without making every idle poll pay the full drain window.
	const idleGrace = 50 * time.Millisecond
	for {
		if len(out) == 0 {
			// No events yet, block up to the full request context.
			select {
			case data, ok := <-buf:
				if !ok {
					return out
				}
				out = append(out, data)
				if len(out) >= max {
					return out
				}
			case <-ctx.Done():
				return out
			}
			continue
		}
		// We already have something. Grab more if they arrive within the
		// short idle grace window, otherwise return.
		idle := time.NewTimer(idleGrace)
		select {
		case data, ok := <-buf:
			idle.Stop()
			if !ok {
				return out
			}
			out = append(out, data)
			if len(out) >= max {
				return out
			}
		case <-idle.C:
			return out
		case <-ctx.Done():
			idle.Stop()
			return out
		}
	}
}
