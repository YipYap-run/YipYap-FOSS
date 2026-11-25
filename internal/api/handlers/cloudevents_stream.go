package handlers

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"
)

// streamBatchMax bounds one log read on a stream connection.
const streamBatchMax = 100

// streamReadWait is how long one log read waits for new events. Between
// reads the handler checks whether the log epoch changed.
const streamReadWait = 25 * time.Second

// CloudEventsStreamHandler serves GET /api/v1/cloudevents/stream.
//
// A long-lived NDJSON HTTP stream that delivers CloudEvents to the
// yipyap-knative-source adapter as they arrive. The wire format is
// CloudEvents structured mode with a batched content type: each event is
// one JSON object on its own line, flushed after each batch.
//
// It accepts the same query parameters and required X-YipYap-Source-ID
// header as the poll handler. Each event carries its cursor in the yipyapcursor
// extension attribute. A client that reconnects with since set to the
// cursor of the last event it handled resumes right after it, so events
// it never confirmed are delivered again (at-least-once).
type CloudEventsStreamHandler struct {
	feed     *CloudEventsFeed
	readWait time.Duration
}

// NewCloudEventsStreamHandlerWithFeed returns a stream handler on a feed
// shared with the poll handler.
func NewCloudEventsStreamHandlerWithFeed(feed *CloudEventsFeed) *CloudEventsStreamHandler {
	return &CloudEventsStreamHandler{feed: feed, readWait: streamReadWait}
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
//	X-YipYap-Log-Gap: true (only when the cursor predates the retained log)
//	X-YipYap-Log-First-Seq: oldest retained sequence (with Log-Gap)
//
// A gap means unconfirmed events were dropped by retention. If it appears
// only after the stream opened (the connection fell more than the retention
// window behind), the stream ends and the reconnect carries the headers.
func (h *CloudEventsStreamHandler) Stream(w http.ResponseWriter, r *http.Request) {
	req := h.feed.parseRequest(w, r)
	if req == nil {
		return
	}

	// The underlying ResponseWriter must support flushing so clients see
	// bytes as soon as we write them.
	flusher, ok := w.(http.Flusher)
	if !ok {
		errorResponse(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}

	ctx := r.Context()
	after, gap, err := h.feed.start(ctx, req)
	if err != nil {
		feedErrorResponse(w, err)
		return
	}

	// Opt out of the parent http.Server.WriteTimeout: this is a long-lived
	// NDJSON stream and the stdlib's wall-clock write deadline would
	// forcibly tear the connection down at WriteTimeout regardless of
	// whether bytes are flowing (R2-C2). Errors here are non-fatal; the
	// stream then ends at WriteTimeout and the client reconnects.
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		slog.Debug("cloudevents stream: clear write deadline failed",
			"err", err)
	}

	// Set headers BEFORE the first Flush. Once we flush, the status and
	// headers are latched on the wire.
	w.Header().Set("Content-Type", "application/cloudevents-batch+json")
	w.Header().Set("Cache-Control", "no-cache")
	// X-Accel-Buffering disables nginx response buffering on proxied
	// deployments.
	w.Header().Set("X-Accel-Buffering", "no")
	if gap != nil {
		w.Header().Set("X-YipYap-Log-Gap", "true")
		w.Header().Set("X-YipYap-Log-First-Seq", strconv.FormatUint(gap.FirstAvailable, 10))
	}
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	m := h.feed.matcher(req)
	newline := []byte{'\n'}
	gapReported := gap != nil
	for {
		batch, err := h.feed.read(ctx, req, m, after, streamBatchMax, h.readWait)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// Headers are sent; end the stream so the client
			// reconnects with its last cursor.
			slog.Warn("cloudevents stream: read failed", "error", err)
			return
		}

		// If the log was recreated its numbering restarted, and reading
		// after the old sequence would wait forever.
		st, stErr := h.feed.log.LogState(ctx)
		if stErr == nil && st.Epoch != after.E && len(batch.events) == 0 {
			after = h.feed.resolve(req, st, after)
		}
		if stErr == nil && st.Epoch == after.E && gapAfter(after, st) != nil && !gapReported {
			return
		}
		gapReported = false

		for _, raw := range batch.events {
			if _, err := w.Write(raw); err != nil {
				return
			}
			if _, err := w.Write(newline); err != nil {
				return
			}
		}
		if len(batch.events) > 0 {
			flusher.Flush()
		}
		after = batch.next
	}
}
