package store

import (
	"context"
	"encoding/json"
	"time"
)

// CloudEventReplyAuditEntry is one row in the audit log written by each
// Phase-5 state-mutating reply handler. Every handler emits an entry,
// accepted, rejected, or errored, so operators have a single forensic
// trail across all reply types.
type CloudEventReplyAuditEntry struct {
	ID          int64
	ReplyID     string
	ReplyType   string
	ReplySource string
	ReplySub    string
	AlertID     string
	MonitorID   string
	OrgID       string
	ChannelID   string
	// Outcome is "accepted" | "rejected:<reason>" | "handler_error:<message>".
	Outcome    string
	Before     json.RawMessage
	After      json.RawMessage
	ReceivedAt time.Time
}

// CloudEventReplyAudit persists audit rows for state-mutating reply events.
//
// Retention is plan-gated, a background janitor calls PruneBefore with a
// cutoff derived from the org's plan (FOSS 14d, Pro 30d, Enterprise 90d).
// Callers that want to surface recent activity use the ListBy* methods.
type CloudEventReplyAudit interface {
	Write(ctx context.Context, e *CloudEventReplyAuditEntry) error
	ListByAlert(ctx context.Context, alertID string, limit int) ([]*CloudEventReplyAuditEntry, error)
	ListByChannel(ctx context.Context, channelID string, limit int) ([]*CloudEventReplyAuditEntry, error)
	ListByOrg(ctx context.Context, orgID string, since time.Time, limit int) ([]*CloudEventReplyAuditEntry, error)
	PruneBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
