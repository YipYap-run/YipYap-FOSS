package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type monitorStore struct{ q queryable }

func (s *monitorStore) Create(ctx context.Context, m *domain.Monitor) error {
	regions, _ := json.Marshal(m.Regions)
	downSev := string(m.DownSeverity)
	if downSev == "" {
		downSev = string(domain.SeverityCritical)
	}
	degSev := string(m.DegradedSeverity)
	if degSev == "" {
		degSev = string(domain.SeverityWarning)
	}
	var integrationKey sql.NullString
	if m.IntegrationKey != "" {
		integrationKey = sql.NullString{String: m.IntegrationKey, Valid: true}
	}
	var runbookURL, serviceID sql.NullString
	if m.RunbookURL != "" {
		runbookURL = sql.NullString{String: m.RunbookURL, Valid: true}
	}
	if m.ServiceID != "" {
		serviceID = sql.NullString{String: m.ServiceID, Valid: true}
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitors (id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, enabled, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.ID, m.OrgID, m.Name, string(m.Type),
		string(m.Config), m.IntervalSeconds, m.TimeoutSeconds,
		m.LatencyWarningMS, m.LatencyCriticalMS, downSev, degSev,
		string(regions), m.EscalationPolicyID, m.HeartbeatToken, integrationKey, runbookURL, serviceID, boolToInt(m.Enabled),
		m.CreatedAt.UTC().Format(timeFormat),
		m.UpdatedAt.UTC().Format(timeFormat),
	)
	return err
}

func (s *monitorStore) GetByID(ctx context.Context, id string) (*domain.Monitor, error) {
	return s.scanMonitor(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, enabled, created_at, updated_at
		 FROM monitors WHERE id = ?`, id)
}

func (s *monitorStore) GetByIntegrationKey(ctx context.Context, key string) (*domain.Monitor, error) {
	return s.scanMonitor(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, enabled, created_at, updated_at
		 FROM monitors WHERE integration_key = ?`, key)
}

func (s *monitorStore) scanMonitor(ctx context.Context, query string, args ...any) (*domain.Monitor, error) {
	var m domain.Monitor
	var monType, config, regions, createdAt, updatedAt, downSev, degSev string
	var enabled int
	var heartbeatToken, integrationKey, runbookURL, serviceID sql.NullString
	err := s.q.QueryRowContext(ctx, query, args...).
		Scan(&m.ID, &m.OrgID, &m.Name, &monType, &config, &m.IntervalSeconds, &m.TimeoutSeconds, &m.LatencyWarningMS, &m.LatencyCriticalMS, &downSev, &degSev, &regions, &m.EscalationPolicyID, &heartbeatToken, &integrationKey, &runbookURL, &serviceID, &enabled, &createdAt, &updatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor not found")
		}
		return nil, err
	}
	m.Type = domain.MonitorType(monType)
	m.Config = json.RawMessage(config)
	m.DownSeverity = domain.Severity(downSev)
	m.DegradedSeverity = domain.Severity(degSev)
	_ = json.Unmarshal([]byte(regions), &m.Regions)
	m.HeartbeatToken = heartbeatToken.String
	m.IntegrationKey = integrationKey.String
	m.RunbookURL = runbookURL.String
	m.ServiceID = serviceID.String
	m.Enabled = enabled != 0
	m.CreatedAt = mustParseTime(createdAt)
	m.UpdatedAt = mustParseTime(updatedAt)
	return &m, nil
}

func (s *monitorStore) ListByOrg(ctx context.Context, orgID string, filter store.MonitorFilter) ([]*domain.Monitor, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, enabled, created_at, updated_at
		 FROM monitors WHERE org_id = ?`
	args := []any{orgID}

	if filter.Type != "" {
		query += " AND type = ?"
		args = append(args, filter.Type)
	}
	if filter.Enabled != nil {
		query += " AND enabled = ?"
		args = append(args, boolToInt(*filter.Enabled))
	}
	query += " ORDER BY name LIMIT ? OFFSET ?"
	args = append(args, limit, filter.Offset)

	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Monitor
	for rows.Next() {
		var m domain.Monitor
		var monType, config, regions, createdAt, updatedAt, downSev, degSev string
		var enabled int
		var heartbeatToken, integrationKey, runbookURL, serviceID sql.NullString
		if err := rows.Scan(&m.ID, &m.OrgID, &m.Name, &monType, &config, &m.IntervalSeconds, &m.TimeoutSeconds, &m.LatencyWarningMS, &m.LatencyCriticalMS, &downSev, &degSev, &regions, &m.EscalationPolicyID, &heartbeatToken, &integrationKey, &runbookURL, &serviceID, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		m.Type = domain.MonitorType(monType)
		m.Config = json.RawMessage(config)
		m.DownSeverity = domain.Severity(downSev)
		m.DegradedSeverity = domain.Severity(degSev)
		_ = json.Unmarshal([]byte(regions), &m.Regions)
		m.HeartbeatToken = heartbeatToken.String
		m.IntegrationKey = integrationKey.String
		m.RunbookURL = runbookURL.String
		m.ServiceID = serviceID.String
		m.Enabled = enabled != 0
		m.CreatedAt = mustParseTime(createdAt)
		m.UpdatedAt = mustParseTime(updatedAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *monitorStore) Update(ctx context.Context, m *domain.Monitor) error {
	regions, _ := json.Marshal(m.Regions)
	downSev := string(m.DownSeverity)
	if downSev == "" {
		downSev = string(domain.SeverityCritical)
	}
	degSev := string(m.DegradedSeverity)
	if degSev == "" {
		degSev = string(domain.SeverityWarning)
	}
	var integrationKey sql.NullString
	if m.IntegrationKey != "" {
		integrationKey = sql.NullString{String: m.IntegrationKey, Valid: true}
	}
	var runbookURL, serviceID sql.NullString
	if m.RunbookURL != "" {
		runbookURL = sql.NullString{String: m.RunbookURL, Valid: true}
	}
	if m.ServiceID != "" {
		serviceID = sql.NullString{String: m.ServiceID, Valid: true}
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE monitors SET name = ?, type = ?, config = ?, interval_seconds = ?, timeout_seconds = ?, latency_warning_ms = ?, latency_critical_ms = ?, down_severity = ?, degraded_severity = ?, regions = ?, escalation_policy_id = ?, integration_key = ?, runbook_url = ?, service_id = ?, enabled = ?, updated_at = ?
		 WHERE id = ?`,
		m.Name, string(m.Type), string(m.Config), m.IntervalSeconds, m.TimeoutSeconds,
		m.LatencyWarningMS, m.LatencyCriticalMS, downSev, degSev,
		string(regions), m.EscalationPolicyID, integrationKey, runbookURL, serviceID, boolToInt(m.Enabled),
		m.UpdatedAt.UTC().Format(timeFormat), m.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *monitorStore) GetNamesByIDs(ctx context.Context, ids []string) (map[string]string, error) {
	if len(ids) == 0 {
		return map[string]string{}, nil
	}
	placeholders := make([]byte, 0, len(ids)*2)
	args := make([]any, len(ids))
	for i, id := range ids {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}

	rows, err := s.q.QueryContext(ctx,
		`SELECT id, name FROM monitors WHERE id IN (`+string(placeholders)+`)`, args...)
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

func (s *monitorStore) ListAllEnabled(ctx context.Context) ([]*domain.Monitor, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, type, config, interval_seconds, timeout_seconds, latency_warning_ms, latency_critical_ms, down_severity, degraded_severity, regions, escalation_policy_id, heartbeat_token, integration_key, runbook_url, service_id, enabled, created_at, updated_at
		 FROM monitors WHERE enabled = 1 ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.Monitor
	for rows.Next() {
		var m domain.Monitor
		var monType, config, regions, createdAt, updatedAt, downSev, degSev string
		var enabled int
		var heartbeatToken, integrationKey, runbookURL, serviceID sql.NullString
		if err := rows.Scan(&m.ID, &m.OrgID, &m.Name, &monType, &config, &m.IntervalSeconds, &m.TimeoutSeconds, &m.LatencyWarningMS, &m.LatencyCriticalMS, &downSev, &degSev, &regions, &m.EscalationPolicyID, &heartbeatToken, &integrationKey, &runbookURL, &serviceID, &enabled, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		m.Type = domain.MonitorType(monType)
		m.Config = json.RawMessage(config)
		m.DownSeverity = domain.Severity(downSev)
		m.DegradedSeverity = domain.Severity(degSev)
		_ = json.Unmarshal([]byte(regions), &m.Regions)
		m.HeartbeatToken = heartbeatToken.String
		m.IntegrationKey = integrationKey.String
		m.RunbookURL = runbookURL.String
		m.ServiceID = serviceID.String
		m.Enabled = true
		m.CreatedAt = mustParseTime(createdAt)
		m.UpdatedAt = mustParseTime(updatedAt)
		out = append(out, &m)
	}
	return out, rows.Err()
}

func (s *monitorStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM monitors WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *monitorStore) SetLabels(ctx context.Context, monitorID string, labels map[string]string) error {
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

func (s *monitorStore) GetLabels(ctx context.Context, monitorID string) (map[string]string, error) {
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

func (s *monitorStore) DeleteLabel(ctx context.Context, monitorID, key string) error {
	_, err := s.q.ExecContext(ctx,
		`DELETE FROM monitor_labels WHERE monitor_id = ? AND key = ?`, monitorID, key)
	return err
}
