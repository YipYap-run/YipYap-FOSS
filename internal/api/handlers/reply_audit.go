package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/api/middleware"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// ReplyAuditHandler exposes the Phase-5 reply audit log over the admin API.
// Surfaces GET /api/v1/admin/cloudevents/replies for the admin dashboard.
type ReplyAuditHandler struct {
	store store.Store
}

// NewReplyAuditHandler returns a ReplyAuditHandler backed by s.
func NewReplyAuditHandler(s store.Store) *ReplyAuditHandler {
	return &ReplyAuditHandler{store: s}
}

// replyAuditEntryView is the wire shape returned to the admin dashboard.
// The store-level entry uses json.RawMessage for Before/After, here we pass
// those through verbatim so the UI can render a simple <pre> diff.
type replyAuditEntryView struct {
	ID          int64           `json:"id"`
	ReplyID     string          `json:"reply_id"`
	ReplyType   string          `json:"reply_type"`
	ReplySource string          `json:"reply_source"`
	ReplySub    string          `json:"reply_sub"`
	AlertID     string          `json:"alert_id"`
	MonitorID   string          `json:"monitor_id"`
	OrgID       string          `json:"org_id"`
	ChannelID   string          `json:"channel_id"`
	Outcome     string          `json:"outcome"`
	Before      json.RawMessage `json:"before,omitempty"`
	After       json.RawMessage `json:"after,omitempty"`
	ReceivedAt  time.Time       `json:"received_at"`
}

// replyAuditResponse is the top-level JSON returned for GET /replies.
type replyAuditResponse struct {
	Entries []replyAuditEntryView `json:"entries"`
	HasMore bool                  `json:"has_more"`
}

// defaultSinceWindow is the default "last N hours" window the admin UI
// requests when the caller does not pass ?since=. 24h is a reasonable
// tradeoff between a forensic-recent snapshot and SQLite scan cost.
const defaultSinceWindow = 24 * time.Hour

// maxReplyAuditLimit caps the per-request window so a malformed limit
// query (or a slow network) cannot thrash the DB. The UI's "Load more"
// button drives further pages via ?since=&limit= on subsequent calls.
const maxReplyAuditLimit = 500

// defaultReplyAuditLimit mirrors the task spec, 100 rows per page.
const defaultReplyAuditLimit = 100

// List handles GET /api/v1/admin/cloudevents/replies.
//
// Query params (all optional):
//
//	since    RFC3339 timestamp, default now-24h
//	channel  channel ID filter (exact match)
//	alert    alert ID filter (exact match)
//	type     reply CloudEvent type filter (exact match)
//	limit    1..500, default 100
//
// Filtering strategy: we call CloudEventReplyAudit.ListByOrg(since, limit+1)
// and apply channel/alert/type predicates in memory. Phase-5 volumes are
// measured in hundreds of rows/day per org, indexed predicates are not
// worth the migration cost yet and can be added later without a breaking
// API change.
//
// Authorization: RequireRole("owner", "admin") gates the route upstream
// in the router so this handler assumes claims are present and admin.
func (h *ReplyAuditHandler) List(w http.ResponseWriter, r *http.Request) {
	claims := middleware.GetClaims(r.Context())
	if claims == nil {
		errorResponse(w, http.StatusUnauthorized, "authentication required")
		return
	}

	q := r.URL.Query()
	since := time.Now().UTC().Add(-defaultSinceWindow)
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			errorResponse(w, http.StatusBadRequest, "invalid since: must be RFC3339")
			return
		}
		since = t.UTC()
	}

	limit := defaultReplyAuditLimit
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			errorResponse(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if n > maxReplyAuditLimit {
			n = maxReplyAuditLimit
		}
		limit = n
	}

	channelFilter := q.Get("channel")
	alertFilter := q.Get("alert")
	typeFilter := q.Get("type")

	// Over-fetch by 1 so we can report has_more without a second round-trip.
	// When in-memory filters prune results we still advertise has_more=true
	// when the raw list saturated limit+1, the UI's "Load more" uses the
	// oldest received_at as the next `since` and the server will re-filter.
	fetchLimit := limit + 1

	rows, err := h.store.CloudEventReplyAudit().ListByOrg(r.Context(), claims.OrgID, since, fetchLimit)
	if err != nil {
		errorResponse(w, http.StatusInternalServerError, "failed to list reply audit entries")
		return
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	entries := make([]replyAuditEntryView, 0, len(rows))
	for _, e := range rows {
		if channelFilter != "" && e.ChannelID != channelFilter {
			continue
		}
		if alertFilter != "" && e.AlertID != alertFilter {
			continue
		}
		if typeFilter != "" && e.ReplyType != typeFilter {
			continue
		}
		entries = append(entries, replyAuditEntryView{
			ID:          e.ID,
			ReplyID:     e.ReplyID,
			ReplyType:   e.ReplyType,
			ReplySource: e.ReplySource,
			ReplySub:    e.ReplySub,
			AlertID:     e.AlertID,
			MonitorID:   e.MonitorID,
			OrgID:       e.OrgID,
			ChannelID:   e.ChannelID,
			Outcome:     e.Outcome,
			Before:      e.Before,
			After:       e.After,
			ReceivedAt:  e.ReceivedAt,
		})
	}

	jsonResponse(w, http.StatusOK, replyAuditResponse{
		Entries: entries,
		HasMore: hasMore,
	})
}
