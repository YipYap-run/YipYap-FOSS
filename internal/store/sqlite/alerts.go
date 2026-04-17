package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type alertStore struct{ q queryable }

func (s *alertStore) Create(ctx context.Context, a *domain.Alert) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alerts (id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.MonitorID, a.OrgID, string(a.Status), string(a.Severity), a.Error,
		a.StartedAt.UTC().Format(timeFormat),
		formatOptionalTime(a.AcknowledgedAt), a.AcknowledgedBy,
		formatOptionalTime(a.ResolvedAt), a.CurrentEscalationStep,
		nullString(a.IncidentID))
	return err
}

func (s *alertStore) GetByID(ctx context.Context, id string) (*domain.Alert, error) {
	return s.scanAlert(s.q.QueryRowContext(ctx,
		`SELECT id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id
		 FROM alerts WHERE id = ?`, id))
}

func (s *alertStore) ListByOrg(ctx context.Context, orgID string, filter store.AlertFilter) ([]*domain.Alert, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id
		 FROM alerts WHERE org_id = ?`
	args := []any{orgID}

	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
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
		a, err := s.scanAlertFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *alertStore) GetActiveByMonitor(ctx context.Context, monitorID string) (*domain.Alert, error) {
	return s.scanAlert(s.q.QueryRowContext(ctx,
		`SELECT id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id
		 FROM alerts WHERE monitor_id = ? AND status != 'resolved' ORDER BY started_at DESC LIMIT 1`, monitorID))
}

func (s *alertStore) Update(ctx context.Context, a *domain.Alert) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET status = ?, severity = ?, acknowledged_at = ?, acknowledged_by = ?, resolved_at = ?, current_escalation_step = ?, incident_id = ?
		 WHERE id = ?`,
		string(a.Status), string(a.Severity),
		formatOptionalTime(a.AcknowledgedAt), a.AcknowledgedBy,
		formatOptionalTime(a.ResolvedAt), a.CurrentEscalationStep,
		nullString(a.IncidentID), a.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *alertStore) ListFiring(ctx context.Context) ([]*domain.Alert, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, monitor_id, org_id, status, severity, error, started_at, acknowledged_at, acknowledged_by, resolved_at, current_escalation_step, incident_id
		 FROM alerts WHERE status = 'firing' ORDER BY started_at`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Alert
	for rows.Next() {
		a, err := s.scanAlertFromRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *alertStore) CreateEvent(ctx context.Context, e *domain.AlertEvent) error {
	detail := string(e.Detail)
	if detail == "" {
		detail = "{}"
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_events (id, alert_id, event_type, channel, target_user_id, detail, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.AlertID, string(e.EventType), e.Channel, e.TargetUserID,
		detail, e.CreatedAt.UTC().Format(timeFormat))
	return err
}

func (s *alertStore) ListEvents(ctx context.Context, alertID string) ([]*domain.AlertEvent, error) {
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
		var eventType, detail, createdAt string
		if err := rows.Scan(&e.ID, &e.AlertID, &eventType, &e.Channel, &e.TargetUserID, &detail, &createdAt); err != nil {
			return nil, err
		}
		e.EventType = domain.AlertEventType(eventType)
		e.Detail = json.RawMessage(detail)
		e.CreatedAt = mustParseTime(createdAt)
		out = append(out, &e)
	}
	return out, rows.Err()
}

func (s *alertStore) GetEscalationState(ctx context.Context, alertID string) (*domain.AlertEscalationState, error) {
	var st domain.AlertEscalationState
	var stepEnteredAt, notifSent string
	var lastNotified *string
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
	st.StepEnteredAt = mustParseTime(stepEnteredAt)
	st.LastNotifiedAt = parseOptionalTime(lastNotified)
	st.NotificationsSent = json.RawMessage(notifSent)
	return &st, nil
}

func (s *alertStore) UpsertEscalationState(ctx context.Context, st *domain.AlertEscalationState) error {
	notifSent := string(st.NotificationsSent)
	if notifSent == "" {
		notifSent = "[]"
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO alert_escalation_state (alert_id, current_step_id, step_entered_at, retry_count, last_notified_at, notifications_sent)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(alert_id) DO UPDATE SET
		   current_step_id = excluded.current_step_id,
		   step_entered_at = excluded.step_entered_at,
		   retry_count = excluded.retry_count,
		   last_notified_at = excluded.last_notified_at,
		   notifications_sent = excluded.notifications_sent`,
		st.AlertID, st.CurrentStepID,
		st.StepEnteredAt.UTC().Format(timeFormat),
		st.RetryCount,
		formatOptionalTime(st.LastNotifiedAt),
		notifSent)
	return err
}

func (s *alertStore) DeleteEscalationState(ctx context.Context, alertID string) error {
	_, err := s.q.ExecContext(ctx, `DELETE FROM alert_escalation_state WHERE alert_id = ?`, alertID)
	return err
}

func (s *alertStore) scanAlert(row *sql.Row) (*domain.Alert, error) {
	var a domain.Alert
	var status, severity, startedAt string
	var ackedAt, resolvedAt *string
	var incidentID sql.NullString
	if err := row.Scan(&a.ID, &a.MonitorID, &a.OrgID, &status, &severity, &a.Error, &startedAt, &ackedAt, &a.AcknowledgedBy, &resolvedAt, &a.CurrentEscalationStep, &incidentID); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("alert not found")
		}
		return nil, err
	}
	a.Status = domain.AlertStatus(status)
	a.Severity = domain.Severity(severity)
	a.StartedAt = mustParseTime(startedAt)
	a.AcknowledgedAt = parseOptionalTime(ackedAt)
	a.ResolvedAt = parseOptionalTime(resolvedAt)
	a.IncidentID = incidentID.String
	return &a, nil
}

func (s *alertStore) scanAlertFromRows(rows *sql.Rows) (*domain.Alert, error) {
	var a domain.Alert
	var status, severity, startedAt string
	var ackedAt, resolvedAt *string
	var incidentID sql.NullString
	if err := rows.Scan(&a.ID, &a.MonitorID, &a.OrgID, &status, &severity, &a.Error, &startedAt, &ackedAt, &a.AcknowledgedBy, &resolvedAt, &a.CurrentEscalationStep, &incidentID); err != nil {
		return nil, err
	}
	a.Status = domain.AlertStatus(status)
	a.Severity = domain.Severity(severity)
	a.StartedAt = mustParseTime(startedAt)
	a.AcknowledgedAt = parseOptionalTime(ackedAt)
	a.ResolvedAt = parseOptionalTime(resolvedAt)
	a.IncidentID = incidentID.String
	return &a, nil
}
