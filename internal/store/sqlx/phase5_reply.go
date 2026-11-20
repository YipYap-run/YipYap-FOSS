package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// ------------------------------- suppressions ----------------------------

// AlertSuppressionStore implements store.AlertSuppressionStore across SQL backends.
type AlertSuppressionStore struct {
	q Queryable
	d Dialect
}

// NewAlertSuppressionStore returns an AlertSuppressionStore bound to q.
func NewAlertSuppressionStore(q Queryable, d Dialect) *AlertSuppressionStore {
	return &AlertSuppressionStore{q: q, d: d}
}

func (s *AlertSuppressionStore) Create(ctx context.Context, r *store.AlertSuppression) error {
	filter := string(r.MatchFilter)
	if filter == "" {
		filter = "{}"
	}
	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO alert_suppressions (id, org_id, alert_id, monitor_id, match_filter, expires_at, reason, source_reply_id, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.OrgID, r.AlertID, r.MonitorID, filter,
		s.d.MillisTimeArg(r.ExpiresAt), r.Reason, r.SourceReplyID,
		s.d.MillisTimeArg(createdAt))
	return err
}

func (s *AlertSuppressionStore) ListActiveForAlert(ctx context.Context, alertID string, at time.Time) ([]*store.AlertSuppression, error) {
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, org_id, alert_id, monitor_id, match_filter, expires_at, reason, source_reply_id, created_at
        FROM alert_suppressions
        WHERE alert_id = ? AND expires_at > ?`,
		alertID, s.d.MillisTimeArg(at))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAlertSuppressions(rows)
}

func (s *AlertSuppressionStore) ListActiveForOrg(ctx context.Context, orgID string, at time.Time) ([]*store.AlertSuppression, error) {
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, org_id, alert_id, monitor_id, match_filter, expires_at, reason, source_reply_id, created_at
        FROM alert_suppressions
        WHERE org_id = ? AND expires_at > ?`,
		orgID, s.d.MillisTimeArg(at))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAlertSuppressions(rows)
}

func (s *AlertSuppressionStore) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM alert_suppressions WHERE expires_at < ?`, s.d.MillisTimeArg(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanAlertSuppressions(rows Rows) ([]*store.AlertSuppression, error) {
	var out []*store.AlertSuppression
	for rows.Next() {
		var r store.AlertSuppression
		var filter string
		var expiresAt, createdAt Time
		if err := rows.Scan(&r.ID, &r.OrgID, &r.AlertID, &r.MonitorID, &filter,
			&expiresAt, &r.Reason, &r.SourceReplyID, &createdAt); err != nil {
			return nil, err
		}
		r.MatchFilter = json.RawMessage(filter)
		r.ExpiresAt = expiresAt.T
		r.CreatedAt = createdAt.T
		out = append(out, &r)
	}
	return out, rows.Err()
}

// ------------------------------- remediations ----------------------------

// AlertRemediationStore implements store.AlertRemediationStore across SQL backends.
type AlertRemediationStore struct {
	q Queryable
	d Dialect
}

// NewAlertRemediationStore returns an AlertRemediationStore bound to q.
func NewAlertRemediationStore(q Queryable, d Dialect) *AlertRemediationStore {
	return &AlertRemediationStore{q: q, d: d}
}

func (s *AlertRemediationStore) Create(ctx context.Context, r *store.AlertRemediation) error {
	// org_id is denormalised from the parent alert. When the caller hasn't
	// supplied one (older handlers) we look it up so the FK and the index
	// on (org_id, alert_id) are always populated correctly.
	orgID := r.OrgID
	if orgID == "" {
		if err := s.q.QueryRowContext(ctx,
			`SELECT org_id FROM alerts WHERE id = ?`, r.AlertID).Scan(&orgID); err != nil {
			return fmt.Errorf("derive org_id for remediation: %w", err)
		}
	}
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO alert_remediations (remediation_id, alert_id, org_id, status, started_at, completed_at, expires_at, description, message, artifact_url, source_reply_id)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.RemediationID, r.AlertID, orgID, string(r.Status),
		s.d.MillisTimeArg(r.StartedAt),
		s.d.MillisOptionalTimeArg(r.CompletedAt),
		s.d.MillisTimeArg(r.ExpiresAt),
		r.Description, r.Message, r.ArtifactURL, r.SourceReplyID)
	if err == nil {
		r.OrgID = orgID
	}
	return err
}

func (s *AlertRemediationStore) GetByID(ctx context.Context, remediationID string) (*store.AlertRemediation, error) {
	var r store.AlertRemediation
	var status string
	var startedAt, expiresAt Time
	var completedAt Time
	err := s.q.QueryRowContext(ctx, `
        SELECT remediation_id, alert_id, org_id, status, started_at, completed_at, expires_at, description, message, artifact_url, source_reply_id
        FROM alert_remediations WHERE remediation_id = ?`, remediationID).
		Scan(&r.RemediationID, &r.AlertID, &r.OrgID, &status, &startedAt, &completedAt, &expiresAt,
			&r.Description, &r.Message, &r.ArtifactURL, &r.SourceReplyID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("remediation not found")
		}
		return nil, err
	}
	r.Status = store.RemediationStatus(status)
	r.StartedAt = startedAt.T
	r.ExpiresAt = expiresAt.T
	r.CompletedAt = completedAt.TimePtr()
	return &r, nil
}

func (s *AlertRemediationStore) Complete(ctx context.Context, remediationID string, status store.RemediationStatus, message, artifactURL string) error {
	// A3-NEW-M-2: CAS guard, only transition rows that are still
	// in_progress. Without this, two concurrent remediation_result.v1
	// replies for the same remediation_id could both succeed and the
	// handler would auto-resolve the alert twice. Surface a zero-rows
	// outcome to the caller as ErrAlreadyComplete so the handler can
	// emit a rejected audit instead of treating it as a fresh success.
	res, err := s.q.ExecContext(ctx, `
        UPDATE alert_remediations
        SET status = ?, message = ?, artifact_url = ?, completed_at = ?
        WHERE remediation_id = ? AND status = 'in_progress'`,
		string(status), message, artifactURL, s.d.MillisTimeArg(time.Now().UTC()), remediationID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Distinguish "row didn't exist" from "row was already complete"
		// for callers that need a sentinel.
		if _, gerr := s.GetByID(ctx, remediationID); gerr == nil {
			return store.ErrAlreadyComplete
		}
		return fmt.Errorf("remediation not found")
	}
	return nil
}

func (s *AlertRemediationStore) ListActiveForAlert(ctx context.Context, alertID string, at time.Time) ([]*store.AlertRemediation, error) {
	rows, err := s.q.QueryContext(ctx, `
        SELECT remediation_id, alert_id, org_id, status, started_at, completed_at, expires_at, description, message, artifact_url, source_reply_id
        FROM alert_remediations
        WHERE alert_id = ? AND status = 'in_progress' AND expires_at > ?`,
		alertID, s.d.MillisTimeArg(at))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*store.AlertRemediation
	for rows.Next() {
		var r store.AlertRemediation
		var status string
		var startedAt, expiresAt Time
		var completedAt Time
		if err := rows.Scan(&r.RemediationID, &r.AlertID, &r.OrgID, &status, &startedAt, &completedAt, &expiresAt,
			&r.Description, &r.Message, &r.ArtifactURL, &r.SourceReplyID); err != nil {
			return nil, err
		}
		r.Status = store.RemediationStatus(status)
		r.StartedAt = startedAt.T
		r.ExpiresAt = expiresAt.T
		r.CompletedAt = completedAt.TimePtr()
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *AlertRemediationStore) ExpireStale(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx, `
        UPDATE alert_remediations
        SET status = 'failure', completed_at = ?, message = 'expired'
        WHERE status = 'in_progress' AND expires_at < ?`,
		s.d.MillisTimeArg(cutoff),
		s.d.MillisTimeArg(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ------------------------------- routing rules ---------------------------

// AlertRoutingRuleStore implements store.AlertRoutingRuleStore across SQL backends.
type AlertRoutingRuleStore struct {
	q Queryable
	d Dialect
}

// NewAlertRoutingRuleStore returns an AlertRoutingRuleStore bound to q.
func NewAlertRoutingRuleStore(q Queryable, d Dialect) *AlertRoutingRuleStore {
	return &AlertRoutingRuleStore{q: q, d: d}
}

func (s *AlertRoutingRuleStore) Create(ctx context.Context, r *store.AlertRoutingRule) error {
	filter := string(r.MatchFilter)
	if filter == "" {
		filter = "{}"
	}
	createdAt := r.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO alert_routing_rules (id, org_id, target_channel_id, match_filter, expires_at, priority, reason, source_reply_id, created_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.OrgID, r.TargetChannelID, filter,
		s.d.MillisTimeArg(r.ExpiresAt), r.Priority,
		r.Reason, r.SourceReplyID,
		s.d.MillisTimeArg(createdAt))
	return err
}

func (s *AlertRoutingRuleStore) ListActiveForOrg(ctx context.Context, orgID string, at time.Time) ([]*store.AlertRoutingRule, error) {
	// Phase-6 consumers want a deterministic order when several rules match
	// the same alert; the (priority DESC, created_at DESC, id) tiebreak
	// matches what the (org_id, priority DESC, expires_at) index supports.
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, org_id, target_channel_id, match_filter, expires_at, priority, reason, source_reply_id, created_at
        FROM alert_routing_rules
        WHERE org_id = ? AND expires_at > ?
        ORDER BY priority DESC, created_at DESC, id`,
		orgID, s.d.MillisTimeArg(at))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*store.AlertRoutingRule
	for rows.Next() {
		var r store.AlertRoutingRule
		var filter string
		var expiresAt, createdAt Time
		if err := rows.Scan(&r.ID, &r.OrgID, &r.TargetChannelID, &filter,
			&expiresAt, &r.Priority, &r.Reason, &r.SourceReplyID, &createdAt); err != nil {
			return nil, err
		}
		r.MatchFilter = json.RawMessage(filter)
		r.ExpiresAt = expiresAt.T
		r.CreatedAt = createdAt.T
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *AlertRoutingRuleStore) DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM alert_routing_rules WHERE expires_at < ?`, s.d.MillisTimeArg(cutoff))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// ------------------------------- status page refs ------------------------

// AlertStatusPageRefStore implements store.AlertStatusPageRefStore across SQL backends.
type AlertStatusPageRefStore struct {
	q Queryable
	d Dialect
}

// NewAlertStatusPageRefStore returns an AlertStatusPageRefStore bound to q.
func NewAlertStatusPageRefStore(q Queryable, d Dialect) *AlertStatusPageRefStore {
	return &AlertStatusPageRefStore{q: q, d: d}
}

func (s *AlertStatusPageRefStore) Create(ctx context.Context, r *store.AlertStatusPageRef) error {
	_, err := s.q.ExecContext(ctx, `
        INSERT INTO alert_status_page_refs (id, alert_id, provider, url, public_message, updated_at, source_reply_id)
        VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.AlertID, r.Provider, r.URL, r.PublicMessage,
		s.d.MillisTimeArg(r.UpdatedAt),
		r.SourceReplyID)
	return err
}

func (s *AlertStatusPageRefStore) ListByAlert(ctx context.Context, alertID string) ([]*store.AlertStatusPageRef, error) {
	rows, err := s.q.QueryContext(ctx, `
        SELECT id, alert_id, provider, url, public_message, updated_at, source_reply_id
        FROM alert_status_page_refs
        WHERE alert_id = ?
        ORDER BY updated_at DESC`, alertID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []*store.AlertStatusPageRef
	for rows.Next() {
		var r store.AlertStatusPageRef
		var updatedAt Time
		if err := rows.Scan(&r.ID, &r.AlertID, &r.Provider, &r.URL, &r.PublicMessage,
			&updatedAt, &r.SourceReplyID); err != nil {
			return nil, err
		}
		r.UpdatedAt = updatedAt.T
		out = append(out, &r)
	}
	return out, rows.Err()
}
