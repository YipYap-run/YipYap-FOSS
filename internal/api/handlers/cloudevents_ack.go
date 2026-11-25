package handlers

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
)

// CEAckRatePerMinute is the per-org limit on ack requests. Clients should
// ack at most once every few seconds per source, not once per event.
const CEAckRatePerMinute = 120

// ceAckMaxBody bounds the ack request body. A cursor is at most
// ceMaxCursorLen bytes, so 4 KiB leaves room for JSON framing only.
const ceAckMaxBody = 4 << 10

// CloudEventsAckHandler serves POST /api/v1/cloudevents/ack.
//
// A client that keeps a stream open, or polls with a cursor it holds in
// memory, uses it to confirm progress to the server without waiting for
// its next request. The body is {"cursor": "<cursor>"}, any cursor the
// feed issued (next_cursor or an event's yipyapcursor). The
// X-YipYap-Source-ID header is required: the ack moves that source's
// remembered position, so a restarted client that sends no since resumes
// right after the acked event. Other sources on the org are unaffected.
//
// An ack never deletes anything from the log. Acking an older cursor moves
// the position back and the events after it are delivered again, the same
// as sending that cursor as since.
type CloudEventsAckHandler struct {
	feed *CloudEventsFeed
}

// NewCloudEventsAckHandlerWithFeed returns an ack handler on the feed shared
// with the poll and stream handlers.
func NewCloudEventsAckHandlerWithFeed(feed *CloudEventsFeed) *CloudEventsAckHandler {
	return &CloudEventsAckHandler{feed: feed}
}

// Ack handles POST /api/v1/cloudevents/ack and answers 204 on success.
func (h *CloudEventsAckHandler) Ack(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil || claims.OrgID == "" {
		errorResponse(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	if !validSubjectToken(claims.OrgID) {
		errorResponse(w, http.StatusInternalServerError, "invalid org")
		return
	}
	sourceID := r.Header.Get(CESourceIDHeader)
	if !validSourceID(sourceID) {
		errorResponse(w, http.StatusBadRequest, CESourceIDHeader+" header is required (1 to 128 chars of [A-Za-z0-9._:/-])")
		return
	}

	defer func() { _ = r.Body.Close() }()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, ceAckMaxBody))
	dec.DisallowUnknownFields()
	var body struct {
		Cursor string `json:"cursor"`
	}
	if err := dec.Decode(&body); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		// The second Decode rejects trailing data after the object.
		errorResponse(w, http.StatusBadRequest, "body must be {\"cursor\": \"...\"}")
		return
	}
	cur, err := decodeCECursor(body.Cursor)
	if err != nil || cur == nil {
		// A missing or legacy cursor names no position to confirm.
		errorResponse(w, http.StatusBadRequest, "invalid cursor")
		return
	}

	req := &ceRequest{orgID: claims.OrgID, key: "source\x00" + sourceID, since: cur}
	if _, _, err := h.feed.start(r.Context(), req); err != nil {
		feedErrorResponse(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
