package sqlx

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// CloudEventReplyAuditStore implements store.CloudEventReplyAuditStore across
// SQL backends.
type CloudEventReplyAuditStore struct {
	q Queryable
	d Dialect
}

// NewCloudEventReplyAuditStore returns a CloudEventReplyAuditStore bound to q.
func NewCloudEventReplyAuditStore(q Queryable, d Dialect) *CloudEventReplyAuditStore {
	return &CloudEventReplyAuditStore{q: q, d: d}
}

// Write persists a single audit row. When e.ReceivedAt is zero the column
// default (strftime now on SQLite) is used.
func (s *CloudEventReplyAuditStore) Write(ctx context.Context, e *store.CloudEventReplyAuditEntry) error {
	before := string(e.Before)
	if before == "" {
		before = "{}"
	}
	after := string(e.After)
	if after == "" {
		after = "{}"
	}

	if e.ReceivedAt.IsZero() {
		_, err := s.q.ExecContext(ctx, `
            INSERT INTO cloudevent_reply_audit
                (reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state)
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?`+s.d.JSONBArgSuffix+`, ?`+s.d.JSONBArgSuffix+`)`,
			e.ReplyID, e.ReplyType, e.ReplySource, e.ReplySub,
			e.AlertID, e.MonitorID, e.OrgID, e.ChannelID,
			e.Outcome, before, after)
		return err
	}
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO cloudevent_reply_audit
            (reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state, received_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?`+s.d.JSONBArgSuffix+`, ?`+s.d.JSONBArgSuffix+`, ?)`,
		e.ReplyID, e.ReplyType, e.ReplySource, e.ReplySub,
		e.AlertID, e.MonitorID, e.OrgID, e.ChannelID,
		e.Outcome, before, after,
		s.d.MillisTimeArg(e.ReceivedAt))
	return err
}

func (s *CloudEventReplyAuditStore) ListByAlert(ctx context.Context, alertID string, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state`+s.d.JSONBColSuffix+`, after_state`+s.d.JSONBColSuffix+`, received_at
        FROM cloudevent_reply_audit
        WHERE alert_id = ?
        ORDER BY received_at DESC, id DESC
        LIMIT ?`, alertID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReplyAuditRows(rows)
}

func (s *CloudEventReplyAuditStore) ListByChannel(ctx context.Context, channelID string, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state`+s.d.JSONBColSuffix+`, after_state`+s.d.JSONBColSuffix+`, received_at
        FROM cloudevent_reply_audit
        WHERE channel_id = ?
        ORDER BY received_at DESC, id DESC
        LIMIT ?`, channelID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReplyAuditRows(rows)
}

func (s *CloudEventReplyAuditStore) ListByOrg(ctx context.Context, orgID string, since time.Time, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state`+s.d.JSONBColSuffix+`, after_state`+s.d.JSONBColSuffix+`, received_at
        FROM cloudevent_reply_audit
        WHERE org_id = ? AND received_at >= ?
        ORDER BY received_at DESC, id DESC
        LIMIT ?`, orgID, s.d.MillisTimeArg(since), limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReplyAuditRows(rows)
}

func (s *CloudEventReplyAuditStore) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_reply_audit WHERE received_at < ?`, s.d.MillisTimeArg(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// scanReplyAuditRows expects the column order from the SELECTs above.
func scanReplyAuditRows(rows interface {
	Next() bool
	Scan(dest ...any) error
	Err() error
}) ([]*store.CloudEventReplyAuditEntry, error) {
	var out []*store.CloudEventReplyAuditEntry
	for rows.Next() {
		var e store.CloudEventReplyAuditEntry
		var before, after string
		var receivedAt Time
		if err := rows.Scan(&e.ID, &e.ReplyID, &e.ReplyType, &e.ReplySource, &e.ReplySub,
			&e.AlertID, &e.MonitorID, &e.OrgID, &e.ChannelID,
			&e.Outcome, &before, &after, &receivedAt); err != nil {
			return nil, err
		}
		e.Before = json.RawMessage(before)
		e.After = json.RawMessage(after)
		e.ReceivedAt = receivedAt.T
		out = append(out, &e)
	}
	return out, rows.Err()
}
