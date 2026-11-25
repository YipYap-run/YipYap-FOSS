package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

const checkCols = `id, monitor_id, status, latency_ms, status_code, error, metadata, tls_expiry_at, checked_at`

// CheckStore implements store.CheckStore across SQL backends.
type CheckStore struct {
	q        Queryable
	db       *sql.DB
	yugabyte bool
	d        Dialect
}

// NewCheckStore returns a CheckStore bound to q. db and yugabyte enable
// follower-read transactions on YugabyteDB (nil db disables them).
func NewCheckStore(q Queryable, db *sql.DB, yugabyte bool, d Dialect) *CheckStore {
	return &CheckStore{q: q, db: db, yugabyte: yugabyte, d: d}
}

func (s *CheckStore) Create(ctx context.Context, c *domain.MonitorCheck) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_checks (`+checkCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.ID, c.MonitorID, string(c.Status), c.LatencyMS, c.StatusCode, c.Error, c.Metadata,
		s.d.OptionalTimeArg(c.TLSExpiry),
		s.d.TimeArg(c.CheckedAt),
	)
	return err
}

func (s *CheckStore) ListByMonitor(ctx context.Context, monitorID string, filter store.CheckFilter) ([]*domain.MonitorCheck, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}

	query := `SELECT ` + checkCols + ` FROM monitor_checks WHERE monitor_id = ?`
	args := []any{monitorID}

	if filter.Since != nil {
		query += " AND checked_at >= ?"
		args = append(args, s.d.TimeArg(filter.Since.UTC()))
	}
	if filter.Until != nil {
		query += " AND checked_at <= ?"
		args = append(args, s.d.TimeArg(filter.Until.UTC()))
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	query += " ORDER BY checked_at DESC LIMIT ? OFFSET ?"
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

func (s *CheckStore) GetLatest(ctx context.Context, monitorID string) (*domain.MonitorCheck, error) {
	c, err := scanCheck(s.q.QueryRowContext(ctx,
		`SELECT `+checkCols+` FROM monitor_checks WHERE monitor_id = ? ORDER BY checked_at DESC LIMIT 1`, monitorID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, store.ErrNoChecks
		}
		return nil, err
	}
	return c, nil
}

func (s *CheckStore) GetLatestHeartbeatPing(ctx context.Context, monitorID string) (*domain.MonitorCheck, error) {
	c, err := scanCheck(s.q.QueryRowContext(ctx,
		`SELECT `+checkCols+`
		 FROM monitor_checks
		 WHERE monitor_id = ? AND status = 'up' AND metadata LIKE '%source_ip%'
		 ORDER BY checked_at DESC LIMIT 1`,
		monitorID))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

func (s *CheckStore) GetLatestByStatus(ctx context.Context, monitorID string, status domain.CheckStatus) (*domain.MonitorCheck, error) {
	c, err := scanCheck(s.q.QueryRowContext(ctx,
		`SELECT `+checkCols+` FROM monitor_checks WHERE monitor_id = ? AND status = ? ORDER BY checked_at DESC LIMIT 1`,
		monitorID, string(status)))
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return c, nil
}

func (s *CheckStore) GetLatestByMonitors(ctx context.Context, monitorIDs []string) (map[string]*domain.MonitorCheck, error) {
	if len(monitorIDs) == 0 {
		return map[string]*domain.MonitorCheck{}, nil
	}

	placeholders := strings.Repeat("?,", len(monitorIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(monitorIDs))
	for i, id := range monitorIDs {
		args[i] = id
	}

	query := `SELECT mc.id, mc.monitor_id, mc.status, mc.latency_ms, mc.status_code, mc.error, mc.metadata, mc.tls_expiry_at, mc.checked_at
		FROM monitor_checks mc
		INNER JOIN (
			SELECT monitor_id, MAX(checked_at) AS max_checked_at
			FROM monitor_checks
			WHERE monitor_id IN (` + placeholders + `)
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

// recentChecksChunk caps how many monitors go into one UNION ALL statement,
// well under SQLite's 500-term compound select limit.
const recentChecksChunk = 100

// ListRecentByMonitors returns the newest perMonitor checks of each monitor.
// Each monitor gets its own ORDER BY ... LIMIT branch of one UNION ALL, so
// every branch reads only its newest rows from the (monitor_id, checked_at)
// index instead of ranking a monitor's whole history.
func (s *CheckStore) ListRecentByMonitors(ctx context.Context, monitorIDs []string, perMonitor int) (map[string][]*domain.MonitorCheck, error) {
	out := make(map[string][]*domain.MonitorCheck, len(monitorIDs))
	if len(monitorIDs) == 0 || perMonitor <= 0 {
		return out, nil
	}

	q := s.q
	if s.yugabyte && s.db != nil {
		tx, err := FollowerReadTx(ctx, s.db)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Commit() }()
		q = RebindingQueryable{Q: tx}
	}

	for start := 0; start < len(monitorIDs); start += recentChecksChunk {
		end := min(start+recentChecksChunk, len(monitorIDs))
		var b strings.Builder
		args := make([]any, 0, 2*(end-start))
		for i, id := range monitorIDs[start:end] {
			if i > 0 {
				b.WriteString(" UNION ALL ")
			}
			fmt.Fprintf(&b, "SELECT * FROM (SELECT %s FROM monitor_checks WHERE monitor_id = ? ORDER BY checked_at DESC LIMIT ?) r%d", checkCols, i)
			args = append(args, id, perMonitor)
		}
		if err := s.collectRecent(ctx, q, b.String(), args, out); err != nil {
			return nil, err
		}
	}
	// UNION ALL does not promise branch order; restore newest first.
	for id, checks := range out {
		sort.SliceStable(checks, func(i, j int) bool { return checks[i].CheckedAt.After(checks[j].CheckedAt) })
		out[id] = checks
	}
	return out, nil
}

func (s *CheckStore) collectRecent(ctx context.Context, q Queryable, query string, args []any, out map[string][]*domain.MonitorCheck) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		c, err := scanCheck(rows)
		if err != nil {
			return err
		}
		out[c.MonitorID] = append(out[c.MonitorID], c)
	}
	return rows.Err()
}

func (s *CheckStore) GetStatusSince(ctx context.Context, monitorID string, status domain.CheckStatus) (*time.Time, error) {
	var checkedAt Time
	err := s.q.QueryRowContext(ctx,
		`SELECT checked_at FROM monitor_checks
		 WHERE monitor_id = ? AND status = ? AND checked_at > COALESCE(
		   (SELECT MAX(checked_at) FROM monitor_checks WHERE monitor_id = ? AND status != ?),
		   '0001-01-01'
		 )
		 ORDER BY checked_at ASC LIMIT 1`, monitorID, string(status), monitorID, string(status)).Scan(&checkedAt)
	if err != nil {
		return nil, err
	}
	return checkedAt.TimePtr(), nil
}

func (s *CheckStore) CreateRollup(ctx context.Context, r *domain.MonitorRollup) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_rollups (monitor_id, period, period_start, uptime_pct, avg_latency_ms, p95_latency_ms, p99_latency_ms, check_count, failure_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.MonitorID, r.Period, s.d.TimeArg(r.PeriodStart),
		r.UptimePct, r.AvgLatencyMS, r.P95LatencyMS, r.P99LatencyMS,
		r.CheckCount, r.FailureCount)
	return err
}

func (s *CheckStore) UpsertRollup(ctx context.Context, r *domain.MonitorRollup) error {
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO monitor_rollups (monitor_id, period, period_start, uptime_pct, avg_latency_ms, p95_latency_ms, p99_latency_ms, check_count, failure_count)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (monitor_id, period, period_start) DO UPDATE SET
		   uptime_pct = excluded.uptime_pct,
		   avg_latency_ms = excluded.avg_latency_ms,
		   p95_latency_ms = excluded.p95_latency_ms,
		   p99_latency_ms = excluded.p99_latency_ms,
		   check_count = excluded.check_count,
		   failure_count = excluded.failure_count`,
		r.MonitorID, r.Period, s.d.TimeArg(r.PeriodStart),
		r.UptimePct, r.AvgLatencyMS, r.P95LatencyMS, r.P99LatencyMS,
		r.CheckCount, r.FailureCount)
	return err
}

func (s *CheckStore) GetRollups(ctx context.Context, monitorID, period string) ([]*domain.MonitorRollup, error) {
	q := s.q
	if s.yugabyte && s.db != nil {
		tx, err := FollowerReadTx(ctx, s.db)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Commit() }()
		q = RebindingQueryable{Q: tx}
	}

	rows, err := q.QueryContext(ctx,
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
		var periodStart Time
		if err := rows.Scan(&r.MonitorID, &r.Period, &periodStart, &r.UptimePct, &r.AvgLatencyMS, &r.P95LatencyMS, &r.P99LatencyMS, &r.CheckCount, &r.FailureCount); err != nil {
			return nil, err
		}
		r.PeriodStart = periodStart.T
		out = append(out, &r)
	}
	return out, rows.Err()
}

func (s *CheckStore) CountByMonitor(ctx context.Context, monitorID string, filter store.CheckFilter) (int64, error) {
	query := `SELECT COUNT(*) FROM monitor_checks WHERE monitor_id = ?`
	args := []any{monitorID}

	if filter.Since != nil {
		query += " AND checked_at >= ?"
		args = append(args, s.d.TimeArg(filter.Since.UTC()))
	}
	if filter.Until != nil {
		query += " AND checked_at <= ?"
		args = append(args, s.d.TimeArg(filter.Until.UTC()))
	}
	if filter.Status != "" {
		query += " AND status = ?"
		args = append(args, filter.Status)
	}

	q := s.q
	if s.yugabyte && s.db != nil {
		tx, err := FollowerReadTx(ctx, s.db)
		if err != nil {
			return 0, err
		}
		defer func() { _ = tx.Commit() }()
		q = RebindingQueryable{Q: tx}
	}

	var count int64
	err := q.QueryRowContext(ctx, query, args...).Scan(&count)
	return count, err
}

func (s *CheckStore) AggregateByOrg(ctx context.Context, monitorIDs []string) (*store.CheckAggregate, error) {
	if len(monitorIDs) == 0 {
		return &store.CheckAggregate{}, nil
	}

	placeholders := strings.Repeat("?,", len(monitorIDs))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]any, len(monitorIDs))
	for i, id := range monitorIDs {
		args[i] = id
	}

	q := s.q
	if s.yugabyte && s.db != nil {
		tx, err := FollowerReadTx(ctx, s.db)
		if err != nil {
			return nil, err
		}
		defer func() { _ = tx.Commit() }()
		q = RebindingQueryable{Q: tx}
	}

	var total int64
	var oldest, newest Time
	err := q.QueryRowContext(ctx,
		`SELECT COUNT(*), MIN(checked_at), MAX(checked_at)
		 FROM monitor_checks WHERE monitor_id IN (`+placeholders+`)`, args...).Scan(&total, &oldest, &newest)
	if err != nil {
		return nil, err
	}
	return &store.CheckAggregate{TotalChecks: total, Oldest: oldest.TimePtr(), Newest: newest.TimePtr()}, nil
}

func (s *CheckStore) PruneBefore(ctx context.Context, monitorID string, before time.Time) (int64, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM monitor_checks WHERE monitor_id = ? AND checked_at < ?`,
		monitorID, s.d.TimeArg(before))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func scanCheck(r Scanner) (*domain.MonitorCheck, error) {
	var c domain.MonitorCheck
	var status string
	var tlsExpiry, checkedAt Time
	if err := r.Scan(&c.ID, &c.MonitorID, &status, &c.LatencyMS, &c.StatusCode, &c.Error, &c.Metadata, &tlsExpiry, &checkedAt); err != nil {
		return nil, err
	}
	c.Status = domain.CheckStatus(status)
	c.CheckedAt = checkedAt.T
	c.TLSExpiry = tlsExpiry.TimePtr()
	return &c, nil
}
