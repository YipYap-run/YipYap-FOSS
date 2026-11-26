package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const alertCols = `id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id`

// AlertStore implements store.AlertStore across SQL backends.
type AlertStore struct {
	q Queryable
	d Dialect
}

// NewAlertStore returns an AlertStore bound to q with the given dialect.
func NewAlertStore(q Queryable, d Dialect) *AlertStore {
	return &AlertStore{q: q, d: d}
}

func (s *AlertStore) Create(ctx context.Context, a *domain.Alert) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alerts (`+alertCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.MonitorID, a.OrgID, string(a.Status), string(a.Severity), a.Error,
		s.d.TimeArg(a.StartedAt),
		s.d.OptionalTimeArg(a.AcknowledgedAt), a.AcknowledgedBy,
		s.d.OptionalTimeArg(a.ResolvedAt), a.CurrentEscalationStep,
		NullString(a.IncidentID))
	if err != nil && (a.Status == domain.AlertFiring || a.Status == domain.AlertAcknowledged) {
		// Tell the active-alert unique index apart from other failures
		// without parsing driver-specific error codes: if another active
		// alert for this monitor is visible now, the insert lost that race.
		var one int
		if qerr := s.q.QueryRowContext(ctx,
			`SELECT 1 FROM alerts WHERE monitor_id = ? AND status IN ('firing', 'acknowledged') AND id != ? LIMIT 1`,
			a.MonitorID, a.ID).Scan(&one); qerr == nil {
			return fmt.Errorf("%w: %v", store.ErrActiveAlertExists, err)
		}
	}
	return err
}

func (s *AlertStore) GetByID(ctx context.Context, id string) (*domain.Alert, error) {
	a, err := scanAlert(s.q.QueryRowContext(ctx,
		`SELECT `+alertCols+` FROM alerts WHERE id = ?`, id))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert not found")
		}
		return nil, err
	}
	return a, nil
}

func (s *AlertStore) ListByOrg(ctx context.Context, orgID string, filter store.AlertFilter) ([]*domain.Alert, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT ` + alertCols + ` FROM alerts WHERE org_id = ?`
	args := []any{orgID}

	statuses := filter.Statuses
	if filter.Status != "" {
		statuses = append([]string{filter.Status}, statuses...)
	}
	if len(statuses) > 0 {
		query += " AND status IN (?" + strings.Repeat(", ?", len(statuses)-1) + ")"
		for _, st := range statuses {
			args = append(args, st)
		}
	}
	if filter.Severity != "" {
		query += " AND severity = ?"
		args = append(args, filter.Severity)
	}
	if filter.MonitorID != "" {
		query += " AND monitor_id = ?"
		args = append(args, filter.MonitorID)
	}

	query += " ORDER BY started_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, filter.Offset)

	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *AlertStore) GetActiveByMonitor(ctx context.Context, monitorID string) (*domain.Alert, error) {
	a, err := scanAlert(s.q.QueryRowContext(ctx,
		`SELECT `+alertCols+` FROM alerts WHERE monitor_id = ? AND status != 'resolved' ORDER BY started_at DESC LIMIT 1`, monitorID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrAlertNotFound
		}
		return nil, err
	}
	return a, nil
}

func (s *AlertStore) Update(ctx context.Context, a *domain.Alert) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET status = ?, severity = ?, acknowledged_at = ?, acknowledged_by = ?, resolved_at = ?, current_escalation_step = ?, incident_id = ?
		 WHERE id = ?`,
		string(a.Status), string(a.Severity),
		s.d.OptionalTimeArg(a.AcknowledgedAt), a.AcknowledgedBy,
		s.d.OptionalTimeArg(a.ResolvedAt), a.CurrentEscalationStep,
		NullString(a.IncidentID), a.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

// UpdateIfStatus is Update guarded by the alert's current status: the row is
// written only while its status is one of from. When the alert exists but has
// moved to another status it returns store.ErrAlertStateChanged, so a stale
// client cannot reopen or re-resolve an alert someone else already changed
// (mon-10).
func (s *AlertStore) UpdateIfStatus(ctx context.Context, a *domain.Alert, from ...domain.AlertStatus) error {
	if len(from) == 0 {
		return fmt.Errorf("UpdateIfStatus: no allowed statuses")
	}
	placeholders := make([]string, len(from))
	args := []any{
		string(a.Status), string(a.Severity),
		s.d.OptionalTimeArg(a.AcknowledgedAt), a.AcknowledgedBy,
		s.d.OptionalTimeArg(a.ResolvedAt), a.CurrentEscalationStep,
		NullString(a.IncidentID), a.ID,
	}
	for i, st := range from {
		placeholders[i] = "?"
		args = append(args, string(st))
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET status = ?, severity = ?, acknowledged_at = ?, acknowledged_by = ?, resolved_at = ?, current_escalation_step = ?, incident_id = ?
		 WHERE id = ? AND status IN (`+strings.Join(placeholders, ", ")+`)`,
		args...)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if _, err := s.GetByID(ctx, a.ID); err != nil {
		return err
	}
	return store.ErrAlertStateChanged
}

// Acknowledge sets a firing alert to acknowledged. The status condition in
// the WHERE clause makes it a compare-and-set, so two ack events for the same
// alert (a redelivery, or a user acking from Slack and the web at once) ack it
// once. It returns false when the alert is not firing, and the GetByID error
// when the alert does not exist.
func (s *AlertStore) Acknowledge(ctx context.Context, alertID, ackedBy string, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET status = 'acknowledged', acknowledged_at = ?, acknowledged_by = ?
		 WHERE id = ? AND status = 'firing'`,
		s.d.TimeArg(at), ackedBy, alertID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 1 {
		return true, nil
	}
	if _, err := s.GetByID(ctx, alertID); err != nil {
		return false, err
	}
	return false, nil
}

func (s *AlertStore) ListFiring(ctx context.Context) ([]*domain.Alert, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+alertCols+` FROM alerts WHERE status = 'firing' ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Alert
	for rows.Next() {
		a, err := scanAlert(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *AlertStore) CreateEvent(ctx context.Context, e *domain.AlertEvent) error {
	detail := string(e.Detail)
	if detail == "" {
		detail = "{}"
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_events (id, alert_id, event_type, channel, target_user_id, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.AlertID, string(e.EventType), e.Channel, e.TargetUserID,
		detail, s.d.TimeArg(e.CreatedAt))
	return err
}

// CreateEventOnce inserts e unless an event with the same ID exists. It
// reports whether a row was inserted, so callers that derive the ID from a
// job can process a redelivered message exactly once.
func (s *AlertStore) CreateEventOnce(ctx context.Context, e *domain.AlertEvent) (bool, error) {
	detail := string(e.Detail)
	if detail == "" {
		detail = "{}"
	}
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_events (id, alert_id, event_type, channel, target_user_id, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (id) DO NOTHING`,
		e.ID, e.AlertID, string(e.EventType), e.Channel, e.TargetUserID,
		detail, s.d.TimeArg(e.CreatedAt))
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *AlertStore) ListEvents(ctx context.Context, alertID string) ([]*domain.AlertEvent, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, alert_id, event_type, channel, target_user_id, detail, created_at
		 FROM alert_events WHERE alert_id = ? ORDER BY created_at`, alertID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.AlertEvent
	for rows.Next() {
		var e domain.AlertEvent
		var eventType, detail string
		var createdAt Time
		if err := rows.Scan(&e.ID, &e.AlertID, &eventType, &e.Channel, &e.TargetUserID, &detail, &createdAt); err != nil {
			return nil, err
		}
		e.EventType = domain.AlertEventType(eventType)
		e.Detail = json.RawMessage(detail)
		e.CreatedAt = createdAt.T
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *AlertStore) GetEscalationState(ctx context.Context, alertID string) (*domain.AlertEscalationState, error) {
	var st domain.AlertEscalationState
	var stepEnteredAt, lastNotified Time
	var notifSent string
	err := s.q.QueryRowContext(ctx,
		`SELECT alert_id, current_step_id, step_entered_at, retry_count, last_notified_at, notifications_sent
		 FROM alert_escalation_state WHERE alert_id = ?`, alertID).
		Scan(&st.AlertID, &st.CurrentStepID, &stepEnteredAt, &st.RetryCount, &lastNotified, &notifSent)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("escalation state not found")
		}
		return nil, err
	}
	st.StepEnteredAt = stepEnteredAt.T
	st.LastNotifiedAt = lastNotified.TimePtr()
	st.NotificationsSent = json.RawMessage(notifSent)
	return &st, nil
}

func (s *AlertStore) UpsertEscalationState(ctx context.Context, st *domain.AlertEscalationState) error {
	notifSent := string(st.NotificationsSent)
	if notifSent == "" {
		notifSent = "[]"
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_escalation_state (alert_id, current_step_id, step_entered_at, retry_count, last_notified_at, notifications_sent)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT (alert_id) DO UPDATE SET
		   current_step_id = excluded.current_step_id,
		   step_entered_at = excluded.step_entered_at,
		   retry_count = excluded.retry_count,
		   last_notified_at = excluded.last_notified_at,
		   notifications_sent = excluded.notifications_sent`,
		st.AlertID, st.CurrentStepID,
		s.d.TimeArg(st.StepEnteredAt),
		st.RetryCount,
		s.d.OptionalTimeArg(st.LastNotifiedAt),
		notifSent)
	return err
}

func (s *AlertStore) DeleteEscalationState(ctx context.Context, alertID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM alert_escalation_state WHERE alert_id = ?`, alertID)
	return err
}

func (s *AlertStore) RecordPagedTarget(ctx context.Context, t *domain.AlertPagedTarget) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_paged_targets (alert_id, target_key, kind, user_id, method_id, channel_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (alert_id, target_key) DO NOTHING`,
		t.AlertID, t.TargetKey, string(t.Kind), t.UserID, t.MethodID, t.ChannelID, s.d.TimeArg(t.CreatedAt))
	return err
}

func (s *AlertStore) ListPagedTargets(ctx context.Context, alertID string) ([]*domain.AlertPagedTarget, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT alert_id, target_key, kind, user_id, method_id, channel_id, created_at
		 FROM alert_paged_targets WHERE alert_id = ? ORDER BY created_at, target_key`, alertID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.AlertPagedTarget
	for rows.Next() {
		var (
			t    domain.AlertPagedTarget
			kind string
			at   Time
		)
		if err := rows.Scan(&t.AlertID, &t.TargetKey, &kind, &t.UserID, &t.MethodID, &t.ChannelID, &at); err != nil {
			return nil, err
		}
		t.Kind = domain.PagedTargetKind(kind)
		t.CreatedAt = at.T
		out = append(out, &t)
	}
	return out, rows.Err()
}

func (s *AlertStore) DeletePagedTarget(ctx context.Context, alertID, targetKey string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM alert_paged_targets WHERE alert_id = ? AND target_key = ?`, alertID, targetKey)
	return err
}

func scanAlert(r Scanner) (*domain.Alert, error) {
	var a domain.Alert
	var status, severity string
	var startedAt, ackedAt, resolvedAt Time
	var incidentID sql.NullString
	if err := r.Scan(&a.ID, &a.MonitorID, &a.OrgID, &status, &severity, &a.Error, &startedAt, &ackedAt, &a.AcknowledgedBy, &resolvedAt, &a.CurrentEscalationStep, &incidentID); err != nil {
		return nil, err
	}
	a.Status = domain.AlertStatus(status)
	a.Severity = domain.Severity(severity)
	a.StartedAt = startedAt.T
	a.AcknowledgedAt = ackedAt.TimePtr()
	a.ResolvedAt = resolvedAt.TimePtr()
	a.IncidentID = incidentID.String
	return &a, nil
}
