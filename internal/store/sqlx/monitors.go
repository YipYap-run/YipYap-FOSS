package sqlx

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// MonitorStore implements store.MonitorStore across SQL backends.
type MonitorStore struct {
	q        Queryable
	db       *sql.DB
	yugabyte bool
	d        Dialect
}

// NewMonitorStore returns a MonitorStore bound to q. db and yugabyte enable
// follower-read transactions on YugabyteDB (nil db disables them).
func NewMonitorStore(q Queryable, db *sql.DB, yugabyte bool, d Dialect) *MonitorStore {
	return &MonitorStore{q: q, db: db, yugabyte: yugabyte, d: d}
}

func (s *MonitorStore) Create(ctx context.Context, m *domain.Monitor) error {
	regions, _ := json.Marshal(m.Regions)
	downSev := string(m.DownSeverity)
	if downSev == "" {
		downSev = string(domain.SeverityCritical)
	}
	degSev := string(m.DegradedSeverity)
	if degSev == "" {
		degSev = string(domain.SeverityWarning)
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitors (id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, group_id, description, auto_resolve, muted, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.OrgID, m.Name, string(m.Type),
		string(m.Config), m.IntervalSeconds, m.TimeoutSeconds,
		m.LatencyWarningMS, m.LatencyCriticalMS, downSev, degSev,
		string(regions), m.EscalationPolicyID, m.HeartbeatToken,
		NullString(m.IntegrationKey), NullString(m.RunbookURL), NullString(m.ServiceID), NullString(m.GroupID),
		m.Description, s.d.BoolArg(m.AutoResolve), s.d.BoolArg(m.Muted), s.d.BoolArg(m.Enabled),
		s.d.TimeArg(m.CreatedAt),
		s.d.TimeArg(m.UpdatedAt),
	)
	return err
}

func (s *MonitorStore) GetByID(ctx context.Context, id string) (*domain.Monitor, error) {
	return s.scanMonitor(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, group_id, description, auto_resolve, muted, enabled, created_at, updated_at
		 FROM monitors WHERE id = ?`, id)
}

func (s *MonitorStore) GetByIntegrationKey(ctx context.Context, key string) (*domain.Monitor, error) {
	return s.scanMonitor(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, group_id, description, auto_resolve, muted, enabled, created_at, updated_at
		 FROM monitors WHERE integration_key = ?`, key)
}

func (s *MonitorStore) scanMonitor(ctx context.Context, query string, args ...any) (*domain.Monitor, error) {
	m, err := scanMonitorRow(s.q.QueryRowContext(ctx, query, args...))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor not found")
		}
		return nil, err
	}
	return m, nil
}

func (s *MonitorStore) ListByOrg(ctx context.Context, orgID string, filter store.MonitorFilter) ([]*domain.Monitor, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, group_id, description, auto_resolve, muted, enabled, created_at, updated_at
		 FROM monitors WHERE org_id = ?`
	args := []any{orgID}

	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, filter.Type)
	}
	if filter.Enabled != nil {
		query += " AND enabled = ?"
		args = append(args, s.d.BoolArg(*filter.Enabled))
	}
	query += " ORDER BY name, id LIMIT ? OFFSET ?"
	args = append(args, limit, filter.Offset)

	q := s.q
	if s.yugabyte && s.db != nil {
		tx, err := FollowerReadTx(ctx, s.db)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Commit() }()
		q = RebindingQueryable{Q: tx}
	}

	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Monitor
	for rows.Next() {
		m, err := scanMonitorRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MonitorStore) Update(ctx context.Context, m *domain.Monitor) error {
	regions, _ := json.Marshal(m.Regions)
	downSev := string(m.DownSeverity)
	if downSev == "" {
		downSev = string(domain.SeverityCritical)
	}
	degSev := string(m.DegradedSeverity)
	if degSev == "" {
		degSev = string(domain.SeverityWarning)
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE monitors SET name = ?, type = ?, config = ?, interval_seconds = ?, timeout_seconds = ?, latency_warning_ms = ?, latency_critical_ms = ?, down_severity = ?, degraded_severity = ?, regions = ?, escalation_policy_id = ?, integration_key = ?, runbook_url = ?, service_id = ?, group_id = ?, description = ?, auto_resolve = ?, muted = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		m.Name, string(m.Type), string(m.Config), m.IntervalSeconds, m.TimeoutSeconds,
		m.LatencyWarningMS, m.LatencyCriticalMS, downSev, degSev,
		string(regions), m.EscalationPolicyID,
		NullString(m.IntegrationKey), NullString(m.RunbookURL), NullString(m.ServiceID), NullString(m.GroupID),
		m.Description, s.d.BoolArg(m.AutoResolve), s.d.BoolArg(m.Muted), s.d.BoolArg(m.Enabled),
		s.d.TimeArg(m.UpdatedAt), m.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MonitorStore) GetNamesByIDs(ctx context.Context, orgID string, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]byte, 0, len(ids)*2)
	args := make([]any, 0, len(ids)+1)
	args = append(args, orgID)
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args = append(args, id)
	}

	rows, err := s.q.QueryContext(ctx,
		`SELECT id, name FROM monitors WHERE org_id = ? AND id IN (`+string(placeholders)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]string, len(ids))
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		out[id] = name
	}
	return out, rows.Err()
}

func (s *MonitorStore) ListAllEnabled(ctx context.Context) ([]*domain.Monitor, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, group_id, description, auto_resolve, muted, enabled, created_at, updated_at
		 FROM monitors WHERE enabled = ? ORDER BY name`, s.d.BoolArg(true))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Monitor
	for rows.Next() {
		m, err := scanMonitorRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *MonitorStore) Delete(ctx context.Context, id string) error {
	// alerts.monitor_id lacks ON DELETE CASCADE, so the monitor's alerts must
	// be removed first (mirrors org.Delete's ordering). alert_events and
	// alert_escalation_state cascade from the alert delete; the alert children
	// below reference alert_id or monitor_id without a foreign key, so they are
	// cleaned up explicitly to avoid orphaned rows.
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM alert_status_page_refs WHERE alert_id IN (SELECT id FROM alerts WHERE monitor_id = ?)`, id)
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM alert_remediations WHERE alert_id IN (SELECT id FROM alerts WHERE monitor_id = ?)`, id)
	_, _ = s.q.ExecContext(ctx,
		`DELETE FROM alert_suppressions WHERE monitor_id = ? OR alert_id IN (SELECT id FROM alerts WHERE monitor_id = ?)`, id, id)
	for _, stmt := range monitorDeleteExtras {
		_, _ = s.q.ExecContext(ctx, stmt, id)
	}
	if _, err := s.q.ExecContext(ctx, `DELETE FROM alerts WHERE monitor_id = ?`, id); err != nil {
		return err
	}
	res, err := s.q.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MonitorStore) SetLabels(ctx context.Context, monitorID string, labels map[string]string) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM monitor_labels WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for k, v := range labels {
		if _, err := s.q.ExecContext(ctx,
			`INSERT INTO monitor_labels (monitor_id, key, value) VALUES (?, ?, ?)`,
			monitorID, k, v); err != nil {
			return err
		}
	}
	return nil
}

func (s *MonitorStore) GetLabels(ctx context.Context, monitorID string) (map[string]string, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT key, value FROM monitor_labels WHERE monitor_id = ?`, monitorID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	labels := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		labels[k] = v
	}
	return labels, rows.Err()
}

func (s *MonitorStore) GetLabelsByMonitors(ctx context.Context, monitorIDs []string) (map[string]map[string]string, error) {
	out := make(map[string]map[string]string)
	if len(monitorIDs) == 0 {
		return out, nil
	}
	args := make([]any, len(monitorIDs))
	for i, id := range monitorIDs {
		args[i] = id
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT monitor_id, key, value FROM monitor_labels WHERE monitor_id IN (?`+strings.Repeat(", ?", len(monitorIDs)-1)+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, k, v string
		if err := rows.Scan(&id, &k, &v); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = make(map[string]string)
		}
		out[id][k] = v
	}
	return out, rows.Err()
}

func (s *MonitorStore) DeleteLabel(ctx context.Context, monitorID, key string) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM monitor_labels WHERE monitor_id = ? AND key = ?`, monitorID, key)
	return err
}

func scanMonitorRow(r Scanner) (*domain.Monitor, error) {
	var m domain.Monitor
	var monType, config, regions, downSev, degSev string
	var autoResolve, muted, enabled Bool
	var createdAt, updatedAt Time
	var heartbeatToken, integrationKey, runbookURL, serviceID, groupID sql.NullString
	err := r.Scan(&m.ID, &m.OrgID, &m.Name, &monType, &config, &m.IntervalSeconds, &m.TimeoutSeconds, &m.LatencyWarningMS, &m.LatencyCriticalMS, &downSev, &degSev, &regions, &m.EscalationPolicyID, &heartbeatToken, &integrationKey, &runbookURL, &serviceID, &groupID, &m.Description, &autoResolve, &muted, &enabled, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	m.Type = domain.MonitorType(monType)
	// A monitor saved without a config has an empty column. An empty
	// RawMessage fails to marshal, which turned GET into a 200 with no body.
	if config != "" {
		m.Config = json.RawMessage(config)
	}
	m.DownSeverity = domain.Severity(downSev)
	m.DegradedSeverity = domain.Severity(degSev)
	_ = json.Unmarshal([]byte(regions), &m.Regions)
	m.HeartbeatToken = heartbeatToken.String
	m.IntegrationKey = integrationKey.String
	m.RunbookURL = runbookURL.String
	m.ServiceID = serviceID.String
	m.GroupID = groupID.String
	m.AutoResolve = bool(autoResolve)
	m.Muted = bool(muted)
	m.Enabled = bool(enabled)
	m.CreatedAt = createdAt.T
	m.UpdatedAt = updatedAt.T
	return &m, nil
}
