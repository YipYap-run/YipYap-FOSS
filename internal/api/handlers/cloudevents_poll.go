package handlers

import (
	"net/http"
	"strconv"
	"time"
)

// pollDefaultMax is the default event count ceiling when `max` is absent or
// invalid on the incoming request.
const pollDefaultMax = 100

// pollMaxMax is the hard upper bound: requests asking for more events than
// this are clamped without error.
const pollMaxMax = 500

// pollDrainWindow is how long the handler waits for a first event before
// responding empty. Short enough to feel responsive for idle orgs, long
// enough that clients need not spin their poll interval.
const pollDrainWindow = 2 * time.Second

// CloudEventsPollHandler serves GET /api/v1/cloudevents/poll.
//
// Query parameters:
//
//	since       cursor from a previous response; confirms everything up to
//	            it and returns what follows (at-least-once, see feed docs)
//	max         1..500, default 100
//	filter      CloudEvent type glob, repeatable, OR'd
//	severity    critical|warning|info, repeatable; applies to alert events
//	monitor_id  monitor in the caller's org, repeatable
//
// Header X-YipYap-Source-ID is required and identifies the client for the
// remembered position used when since is absent.
type CloudEventsPollHandler struct {
	feed *CloudEventsFeed
	// drainWindow is a field so tests can shorten it.
	drainWindow time.Duration
}

// NewCloudEventsPollHandlerWithFeed returns a poll handler on a feed shared
// with the stream handler, so both modes use one set of remembered
// positions.
func NewCloudEventsPollHandlerWithFeed(feed *CloudEventsFeed) *CloudEventsPollHandler {
	return &CloudEventsPollHandler{feed: feed, drainWindow: pollDrainWindow}
}

// Poll handles GET /api/v1/cloudevents/poll. Response shape:
//
//	{
//	  "events": [ /* CloudEvent JSON envelopes, each with yipyapcursor */ ],
//	  "next_cursor": "opaque-string",
//	  "gap": {"after": 41, "first_available": 77}
//	}
//
// `events` is always present (possibly empty), never null. `next_cursor` is
// always set; send it as since on the next poll once every event in this
// response is handled. Until then, re-sending the previous cursor returns
// the same events again. `gap` is present only when the requested position
// is older than the retained log: every entry after `after` and before
// `first_available` has already been dropped, so the client can never receive
// it.
func (h *CloudEventsPollHandler) Poll(w http.ResponseWriter, r *http.Request) {
	req := h.feed.parseRequest(w, r)
	if req == nil {
		return
	}
	maxEvents := parsePollMax(r.URL.Query().Get("max"))

	after, gap, err := h.feed.start(r.Context(), req)
	if err != nil {
		feedErrorResponse(w, err)
		return
	}
	batch, err := h.feed.read(r.Context(), req, h.feed.matcher(req), after, maxEvents, h.drainWindow)
	if err != nil {
		feedErrorResponse(w, err)
		return
	}

	body := map[string]any{
		"events":      batch.events,
		"next_cursor": batch.next.encode(),
	}
	if gap != nil {
		body["gap"] = gap
	}
	jsonResponse(w, http.StatusOK, body)
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
