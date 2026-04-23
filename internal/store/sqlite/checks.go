package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type checkStore struct{ q queryable }

func (s *checkStore) Create(ctx context.Context, c *domain.MonitorCheck) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_checks (id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.MonitorID, string(c.Status), c.LatencyMS, c.StatusCode, c.Error, c.Metadata,
		formatOptionalTime(c.TLSExpiry),
		c.CheckedAt.UTC().Format(timeFormat),
	)
	return err
}

func (s *checkStore) ListByMonitor(ctx context.Context, monitorID string, filter store.CheckFilter) ([]*domain.MonitorCheck, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at
		 FROM monitor_checks WHERE monitor_id = ?`
	args := []any{monitorID}

	if filter.Since != nil {
		query += " AND checked_at >= ?"
		args = append(args, filter.Since.UTC().Format(timeFormat))
	}
	if filter.Until != nil {
		query += " AND checked_at <= ?"
		args = append(args, filter.Until.UTC().Format(timeFormat))
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	query += " ORDER BY checked_at DESC LIMIT ? OFFSET ?"
	args = append(args, limit, filter.Offset)

	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorCheck
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *checkStore) GetLatest(ctx context.Context, monitorID string) (*domain.MonitorCheck, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at
		 FROM monitor_checks WHERE monitor_id = ? ORDER BY checked_at DESC LIMIT 1`, monitorID)

	var c domain.MonitorCheck
	var status, checkedAt string
	var tlsExpiry *string
	if err := row.Scan(&c.ID, &c.MonitorID, &status, &c.LatencyMS, &c.StatusCode, &c.Error, &c.Metadata, &tlsExpiry, &checkedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("no checks found")
		}
		return nil, err
	}
	c.Status = domain.CheckStatus(status)
	c.CheckedAt = mustParseTime(checkedAt)
	c.TLSExpiry = parseOptionalTime(tlsExpiry)
	return &c, nil
}

func (s *checkStore) GetLatestHeartbeatPing(ctx context.Context, monitorID string) (*domain.MonitorCheck, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at
		 FROM monitor_checks
		 WHERE monitor_id = ? AND status = 'up' AND metadata LIKE '%source_ip%'
		 ORDER BY checked_at DESC LIMIT 1`,
		monitorID)

	var c domain.MonitorCheck
	var s_, checkedAt string
	var tlsExpiry *string
	if err := row.Scan(&c.ID, &c.MonitorID, &s_, &c.LatencyMS, &c.StatusCode, &c.Error, &c.Metadata, &tlsExpiry, &checkedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.Status = domain.CheckStatus(s_)
	c.CheckedAt = mustParseTime(checkedAt)
	c.TLSExpiry = parseOptionalTime(tlsExpiry)
	return &c, nil
}

func (s *checkStore) GetLatestByStatus(ctx context.Context, monitorID string, status domain.CheckStatus) (*domain.MonitorCheck, error) {
	row := s.q.QueryRowContext(ctx,
		`SELECT id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at
		 FROM monitor_checks WHERE monitor_id = ? AND status = ? ORDER BY checked_at DESC LIMIT 1`,
		monitorID, string(status))

	var c domain.MonitorCheck
	var s_, checkedAt string
	var tlsExpiry *string
	if err := row.Scan(&c.ID, &c.MonitorID, &s_, &c.LatencyMS, &c.StatusCode, &c.Error, &c.Metadata, &tlsExpiry, &checkedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	c.Status = domain.CheckStatus(s_)
	c.CheckedAt = mustParseTime(checkedAt)
	c.TLSExpiry = parseOptionalTime(tlsExpiry)
	return &c, nil
}

func (s *checkStore) GetLatestByMonitors(ctx context.Context, monitorIDs []string) (map[string]*domain.MonitorCheck, error) {
	if len(monitorIDs) == 0 {
		return map[string]*domain.MonitorCheck{}, nil
	}

	placeholders := make([]byte, 0, len(monitorIDs)*2)
	args := make([]any, len(monitorIDs))
	for i, id := range monitorIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}

	query := `SELECT mc.id, mc.monitor_id, mc.status, mc.latency_ms, mc.status_code, mc.error, mc.metadata, mc.tls_expiry_at, mc.checked_at
		FROM monitor_checks mc
		INNER JOIN (
			SELECT monitor_id, MAX(checked_at) AS max_checked_at
			FROM monitor_checks
			WHERE monitor_id IN (` + string(placeholders) + `)
			GROUP BY monitor_id
		) latest ON mc.monitor_id = latest.monitor_id AND mc.checked_at = latest.max_checked_at`

	rows, err := s.q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]*domain.MonitorCheck, len(monitorIDs))
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return nil, err
		}
		out[c.MonitorID] = c
	}
	return out, rows.Err()
}

func (s *checkStore) GetStatusSince(ctx context.Context, monitorID string, status domain.CheckStatus) (*time.Time, error) {
	// Find the earliest check in the current unbroken streak of `status`.
	// Subquery finds the most recent check with a different status; the outer
	// query returns the first check with `status` after that point.
	var checkedAt string
	err := s.q.QueryRowContext(ctx,
		`SELECT checked_at FROM monitor_checks
		 WHERE monitor_id = ? AND status = ? AND checked_at > COALESCE(
		   (SELECT MAX(checked_at) FROM monitor_checks WHERE monitor_id = ? AND status != ?),
		   '0000-01-01T00:00:00Z'
		 )
		 ORDER BY checked_at ASC LIMIT 1`, monitorID, string(status), monitorID, string(status)).Scan(&checkedAt)
	if err != nil {
		return nil, err
	}
	t := mustParseTime(checkedAt)
	return &t, nil
}

func (s *checkStore) CreateRollup(ctx context.Context, r *domain.MonitorRollup) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_rollups (monitor_id, period, period_start, uptime_pct, avg_latency_ms, p95_latency_ms, p99_latency_ms, check_count, failure_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MonitorID, r.Period, r.PeriodStart.UTC().Format(timeFormat),
		r.UptimePct, r.AvgLatencyMS, r.P95LatencyMS, r.P99LatencyMS,
		r.CheckCount, r.FailureCount)
	return err
}

func (s *checkStore) GetRollups(ctx context.Context, monitorID, period string) ([]*domain.MonitorRollup, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT monitor_id, period, period_start, uptime_pct, avg_latency_ms, p95_latency_ms, p99_latency_ms, check_count, failure_count
		 FROM monitor_rollups WHERE monitor_id = ? AND period = ? ORDER BY period_start ASC`,
		monitorID, period)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []*domain.MonitorRollup
	for rows.Next() {
		var r domain.MonitorRollup
		var periodStart string
		if err := rows.Scan(&r.MonitorID, &r.Period, &periodStart, &r.UptimePct, &r.AvgLatencyMS, &r.P95LatencyMS, &r.P99LatencyMS, &r.CheckCount, &r.FailureCount); err != nil {
			return nil, err
		}
		r.PeriodStart = mustParseTime(periodStart)
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *checkStore) CountByMonitor(ctx context.Context, monitorID string, filter store.CheckFilter) (int64, error) {
	query := `SELECT COUNT(*) FROM monitor_checks WHERE monitor_id = ?`
	args := []any{monitorID}

	if filter.Since != nil {
		query += " AND checked_at >= ?"
		args = append(args, filter.Since.UTC().Format(timeFormat))
	}
	if filter.Until != nil {
		query += " AND checked_at <= ?"
		args = append(args, filter.Until.UTC().Format(timeFormat))
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	var count int64
	err := s.q.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *checkStore) AggregateByOrg(ctx context.Context, monitorIDs []string) (*store.CheckAggregate, error) {
	if len(monitorIDs) == 0 {
		return &store.CheckAggregate{}, nil
	}
	placeholders := make([]byte, 0, len(monitorIDs)*2)
	args := make([]any, len(monitorIDs))
	for i, id := range monitorIDs {
		if i > 0 {
			placeholders = append(placeholders, ',')
		}
		placeholders = append(placeholders, '?')
		args[i] = id
	}
	var total int64
	var oldest, newest *string
	err := s.q.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(checked_at), MAX(checked_at)
		 FROM monitor_checks WHERE monitor_id IN (`+string(placeholders)+`)`, args...).Scan(&total, &oldest, &newest)
	if err != nil {
		return nil, err
	}
	agg := &store.CheckAggregate{TotalChecks: total}
	if oldest != nil && *oldest != "" {
		t := mustParseTime(*oldest)
		agg.Oldest = &t
	}
	if newest != nil && *newest != "" {
		t := mustParseTime(*newest)
		agg.Newest = &t
	}
	return agg, nil
}

func (s *checkStore) PruneBefore(ctx context.Context, monitorID string, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM monitor_checks WHERE monitor_id = ? AND checked_at < ?`,
		monitorID, before.UTC().Format(timeFormat))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// scanCheck scans a check row from *sql.Rows.
func scanCheck(rows *sql.Rows) (*domain.MonitorCheck, error) {
	var c domain.MonitorCheck
	var status, checkedAt string
	var tlsExpiry *string
	if err := rows.Scan(&c.ID, &c.MonitorID, &status, &c.LatencyMS, &c.StatusCode, &c.Error, &c.Metadata, &tlsExpiry, &checkedAt); err != nil {
		return nil, err
	}
	c.Status = domain.CheckStatus(status)
	c.CheckedAt = mustParseTime(checkedAt)
	c.TLSExpiry = parseOptionalTime(tlsExpiry)
	return &c, nil
}
