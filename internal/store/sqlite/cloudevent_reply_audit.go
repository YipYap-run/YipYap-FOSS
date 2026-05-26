package sqlite

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type cloudEventReplyAuditStore struct{ q queryable }

// Write persists a single audit row. When e.ReceivedAt is zero the SQLite
// column default (strftime now) is used.
func (s *cloudEventReplyAuditStore) Write(ctx context.Context, e *store.CloudEventReplyAuditEntry) error {
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
            VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ReplyID, e.ReplyType, e.ReplySource, e.ReplySub,
			e.AlertID, e.MonitorID, e.OrgID, e.ChannelID,
			e.Outcome, before, after)
		return err
	}
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO cloudevent_reply_audit
            (reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state, received_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ReplyID, e.ReplyType, e.ReplySource, e.ReplySub,
		e.AlertID, e.MonitorID, e.OrgID, e.ChannelID,
		e.Outcome, before, after,
		e.ReceivedAt.UTC().Format(sqliteCloudEventTimeFormat))
	return err
}

func (s *cloudEventReplyAuditStore) ListByAlert(ctx context.Context, alertID string, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state, received_at
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

func (s *cloudEventReplyAuditStore) ListByChannel(ctx context.Context, channelID string, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state, received_at
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

func (s *cloudEventReplyAuditStore) ListByOrg(ctx context.Context, orgID string, since time.Time, limit int) ([]*store.CloudEventReplyAuditEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	sinceStr := since.UTC().Format(sqliteCloudEventTimeFormat)
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, reply_id, reply_type, reply_source, reply_sub, alert_id, monitor_id, org_id, channel_id, outcome, before_state, after_state, received_at
        FROM cloudevent_reply_audit
        WHERE org_id = ? AND received_at >= ?
        ORDER BY received_at DESC, id DESC
        LIMIT ?`, orgID, sinceStr, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReplyAuditRows(rows)
}

func (s *cloudEventReplyAuditStore) PruneBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM cloudevent_reply_audit WHERE received_at < ?`,
		cutoff.UTC().Format(sqliteCloudEventTimeFormat))
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
		var before, after, receivedAt string
		if err := rows.Scan(&e.ID, &e.ReplyID, &e.ReplyType, &e.ReplySource, &e.ReplySub,
			&e.AlertID, &e.MonitorID, &e.OrgID, &e.ChannelID,
			&e.Outcome, &before, &after, &receivedAt); err != nil {
			return nil, err
		}
		e.Before = json.RawMessage(before)
		e.After = json.RawMessage(after)
		e.ReceivedAt = mustParseTime(receivedAt)
		out = append(out, &e)
	}
	return out, rows.Err()
}
