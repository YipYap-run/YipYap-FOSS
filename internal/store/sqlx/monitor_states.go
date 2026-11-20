package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/google/uuid"
)

// MonitorStateStore implements store.MonitorStateStore across SQL backends.
type MonitorStateStore struct {
	q Queryable
	d Dialect
}

// NewMonitorStateStore returns a MonitorStateStore bound to q.
func NewMonitorStateStore(q Queryable, d Dialect) *MonitorStateStore {
	return &MonitorStateStore{q: q, d: d}
}

func (s *MonitorStateStore) Create(ctx context.Context, st *domain.MonitorState) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO org_monitor_states (id, org_id, name, slug, health_class, severity, color, position, is_builtin, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		st.ID, st.OrgID, st.Name, st.Slug, st.HealthClass, st.Severity,
		st.Color, st.Position, s.d.BoolArg(st.IsBuiltin),
		s.d.TimeArg(st.CreatedAt),
	)
	return err
}

func (s *MonitorStateStore) GetByID(ctx context.Context, id string) (*domain.MonitorState, error) {
	return scanMonitorState(s.q.QueryRowContext(ctx,
		`SELECT id, org_id, name, slug, health_class, severity, color, position, is_builtin, created_at
		 FROM org_monitor_states WHERE id = ?`, id))
}

func (s *MonitorStateStore) ListByOrg(ctx context.Context, orgID string) ([]*domain.MonitorState, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT id, org_id, name, slug, health_class, severity, color, position, is_builtin, created_at
		 FROM org_monitor_states WHERE org_id = ? ORDER BY position`, orgID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorState
	for rows.Next() {
		st, err := scanMonitorStateRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *MonitorStateStore) Update(ctx context.Context, st *domain.MonitorState) error {
	res, err := s.q.ExecContext(ctx,
		`UPDATE org_monitor_states SET name = ?, slug = ?, health_class = ?, severity = ?, color = ?, position = ?
		 WHERE id = ?`,
		st.Name, st.Slug, st.HealthClass, st.Severity, st.Color, st.Position, st.ID,
	)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MonitorStateStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM org_monitor_states WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MonitorStateStore) SeedBuiltins(ctx context.Context, orgID string) error {
	now := time.Now().UTC()
	builtins := []struct {
		slug        string
		name        string
		healthClass string
		severity    string
		color       string
		position    int
	}{
		{"up", "Up", "healthy", "info", "#10b981", 0},
		{"degraded", "Degraded", "degraded", "warning", "#f59e0b", 1},
		{"down", "Down", "unhealthy", "critical", "#ef4444", 2},
	}
	for _, b := range builtins {
		_, err := s.q.ExecContext(ctx,
			`INSERT INTO org_monitor_states (id, org_id, name, slug, health_class, severity, color, position, is_builtin, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT DO NOTHING`,
			uuid.New().String(), orgID, b.name, b.slug, b.healthClass, b.severity, b.color, b.position,
			s.d.BoolArg(true), s.d.TimeArg(now),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func scanMonitorState(r Scanner) (*domain.MonitorState, error) {
	var (
		st        domain.MonitorState
		isBuiltin Bool
		createdAt Time
	)
	if err := r.Scan(&st.ID, &st.OrgID, &st.Name, &st.Slug, &st.HealthClass,
		&st.Severity, &st.Color, &st.Position, &isBuiltin, &createdAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("monitor state not found")
		}
		return nil, err
	}
	st.IsBuiltin = bool(isBuiltin)
	st.CreatedAt = createdAt.T
	return &st, nil
}

func scanMonitorStateRow(r Scanner) (*domain.MonitorState, error) {
	var (
		st        domain.MonitorState
		isBuiltin Bool
		createdAt Time
	)
	if err := r.Scan(&st.ID, &st.OrgID, &st.Name, &st.Slug, &st.HealthClass,
		&st.Severity, &st.Color, &st.Position, &isBuiltin, &createdAt); err != nil {
		return nil, err
	}
	st.IsBuiltin = bool(isBuiltin)
	st.CreatedAt = createdAt.T
	return &st, nil
}

// MonitorMatchRuleStore implements store.MonitorMatchRuleStore across SQL backends.
type MonitorMatchRuleStore struct {
	q Queryable
	d Dialect
}

// NewMonitorMatchRuleStore returns a MonitorMatchRuleStore bound to q.
func NewMonitorMatchRuleStore(q Queryable, d Dialect) *MonitorMatchRuleStore {
	return &MonitorMatchRuleStore{q: q, d: d}
}

func (s *MonitorMatchRuleStore) ListByMonitor(ctx context.Context, monitorID string) ([]*domain.MonitorMatchRule, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT r.id, r.monitor_id, r.position, r.status_code, r.status_code_min, r.status_code_max,
		        r.body_match, r.body_match_mode, r.header_match, r.header_value, r.header_match_mode, r.state_id,
		        s.name, s.color, s.health_class
		 FROM monitor_match_rules r
		 LEFT JOIN org_monitor_states s ON s.id = r.state_id
		 WHERE r.monitor_id = ?
		 ORDER BY r.position`, monitorID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorMatchRule
	for rows.Next() {
		var rule domain.MonitorMatchRule
		var statusCode, statusCodeMin, statusCodeMax sql.NullInt64
		var stateName, stateColor, healthClass sql.NullString
		if err := rows.Scan(&rule.ID, &rule.MonitorID, &rule.Position,
			&statusCode, &statusCodeMin, &statusCodeMax,
			&rule.BodyMatch, &rule.BodyMatchMode, &rule.HeaderMatch, &rule.HeaderValue, &rule.HeaderMatchMode,
			&rule.StateID, &stateName, &stateColor, &healthClass); err != nil {
			return nil, err
		}
		if statusCode.Valid {
			v := int(statusCode.Int64)
			rule.StatusCode = &v
		}
		if statusCodeMin.Valid {
			v := int(statusCodeMin.Int64)
			rule.StatusCodeMin = &v
		}
		if statusCodeMax.Valid {
			v := int(statusCodeMax.Int64)
			rule.StatusCodeMax = &v
		}
		if stateName.Valid {
			rule.StateName = stateName.String
		}
		if stateColor.Valid {
			rule.StateColor = stateColor.String
		}
		if healthClass.Valid {
			rule.HealthClass = healthClass.String
		}
		out = append(out, &rule)
	}
	return out, rows.Err()
}

func (s *MonitorMatchRuleStore) ReplaceForMonitor(ctx context.Context, monitorID string, rules []*domain.MonitorMatchRule) error {
	if _, err := s.q.ExecContext(ctx, `DELETE FROM monitor_match_rules WHERE monitor_id = ?`, monitorID); err != nil {
		return err
	}
	for _, rule := range rules {
		_, err := s.q.ExecContext(ctx,
			`INSERT INTO monitor_match_rules (id, monitor_id, position, status_code, status_code_min, status_code_max, body_match, body_match_mode, header_match, header_value, header_match_mode, state_id)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			rule.ID, monitorID, rule.Position,
			nullInt(rule.StatusCode), nullInt(rule.StatusCodeMin), nullInt(rule.StatusCodeMax),
			rule.BodyMatch, rule.BodyMatchMode, rule.HeaderMatch, rule.HeaderValue, rule.HeaderMatchMode, rule.StateID,
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func nullInt(v *int) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: int64(*v), Valid: true}
}
