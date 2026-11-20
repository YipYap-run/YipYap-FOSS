package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

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
			return nil, fmt.Errorf("alert not found")
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

func (s *AlertStore) MarkDuplicate(ctx context.Context, alertID, duplicateOfID, reason string) error {
	if alertID == "" || duplicateOfID == "" {
		return fmt.Errorf("MarkDuplicate: empty alert id or duplicate_of")
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET duplicate_of = ? WHERE id = ?`,
		duplicateOfID, alertID)
	if err != nil {
		return err
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"duplicate_of": duplicateOfID,
		"reason":       reason,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventDuplicated,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *AlertStore) SetOwner(ctx context.Context, alertID, ownerKind, ownerID, reason string) error {
	if alertID == "" {
		return fmt.Errorf("SetOwner: empty alert id")
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts SET owner_kind = ?, owner_id = ? WHERE id = ?`,
		ownerKind, ownerID, alertID)
	if err != nil {
		return err
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"owner_kind": ownerKind,
		"owner_id":   ownerID,
		"reason":     reason,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventOwnershipTransferred,
		Detail:    detail,
		CreatedAt: time.Now().UTC(),
	})
}

func (s *AlertStore) AckWithContext(ctx context.Context, alertID, reasonCode, note, referenceID, ackedBy string) error {
	if alertID == "" {
		return fmt.Errorf("AckWithContext: empty alert id")
	}
	now := time.Now().UTC()
	res, err := s.q.ExecContext(ctx,
		`UPDATE alerts
		 SET status = 'acknowledged',
		     acknowledged_at = ?,
		     acknowledged_by = ?,
		     ack_reason_code = ?,
		     ack_reference_id = ?
		 WHERE id = ?`,
		s.d.TimeArg(now), ackedBy, reasonCode, referenceID, alertID)
	if err != nil {
		return err
	}
	if err := ExpectOneRow(res); err != nil {
		return err
	}
	detail, _ := json.Marshal(map[string]string{
		"reason_code":  reasonCode,
		"note":         note,
		"reference_id": referenceID,
		"acked_by":     ackedBy,
	})
	return s.CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventAckWithContext,
		Detail:    detail,
		CreatedAt: now,
	})
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
