package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// MaintenanceWindowStore implements store.MaintenanceWindowStore across SQL
// backends.
type MaintenanceWindowStore struct {
	q Queryable
	d Dialect
}

// NewMaintenanceWindowStore returns a MaintenanceWindowStore bound to q.
func NewMaintenanceWindowStore(q Queryable, d Dialect) *MaintenanceWindowStore {
	return &MaintenanceWindowStore{q: q, d: d}
}

const mwCols = `id, org_id, monitor_id, name, description, start_at, end_at, public, suppress_alerts, recurrence_type, recurrence_end_at, days_of_week, day_of_month, timezone, created_at, created_by`

func (s *MaintenanceWindowStore) Create(ctx context.Context, mw *domain.MaintenanceWindow) error {
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	if mw.Timezone == "" {
		mw.Timezone = domain.DefaultMaintenanceTimezone
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO maintenance_windows (`+mwCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mw.ID, mw.OrgID, mw.MonitorID, mw.Name, mw.Description,
		s.d.TimeArg(mw.StartAt), s.d.TimeArg(mw.EndAt),
		s.d.BoolArg(mw.Public), s.d.BoolArg(mw.SuppressAlerts),
		mw.RecurrenceType, s.d.OptionalTimeArg(mw.RecurrenceEndAt), mw.DaysOfWeek, mw.DayOfMonth,
		mw.Timezone, s.d.TimeArg(mw.CreatedAt), mw.CreatedBy)
	return err
}

func (s *MaintenanceWindowStore) GetByID(ctx context.Context, id string) (*domain.MaintenanceWindow, error) {
	return s.scanMW(s.q.QueryRowContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows WHERE id = ?`, id))
}

func (s *MaintenanceWindowStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.MaintenanceWindow, error) {
	limit := params.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows WHERE org_id = ? ORDER BY start_at DESC LIMIT ? OFFSET ?`,
		orgID, limit, params.Offset)
	if err != nil {
		return nil, err
	}
	return s.scanMWRows(rows)
}

// ListActiveByMonitor returns the windows active at `at` for monitorID:
// windows on that monitor plus org-wide windows (empty monitor_id). Only
// windows owned by the monitor's own org count, so a window another org
// created can never suppress its alerts.
func (s *MaintenanceWindowStore) ListActiveByMonitor(ctx context.Context, monitorID string, at time.Time) ([]*domain.MaintenanceWindow, error) {
	atArg := s.d.TimeArg(at)
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows
		 WHERE (monitor_id = ? OR monitor_id = '')
		   AND org_id = (SELECT m.org_id FROM monitors m WHERE m.id = ?)
		   AND suppress_alerts = ?
		   AND (
		     (recurrence_type = 'none' AND start_at <= ? AND end_at >= ?)
		     OR
		     (recurrence_type != 'none' AND start_at <= ? AND (recurrence_end_at IS NULL OR recurrence_end_at >= ?))
		   )`,
		monitorID, monitorID, s.d.BoolArg(true), atArg, atArg, atArg, atArg)
	if err != nil {
		return nil, err
	}
	all, err := s.scanMWRows(rows)
	if err != nil {
		return nil, err
	}
	var active []*domain.MaintenanceWindow
	for _, mw := range all {
		if mw.IsActiveAt(at) {
			active = append(active, mw)
		}
	}
	return active, nil
}

func (s *MaintenanceWindowStore) ListPublicByOrg(ctx context.Context, orgID string) ([]*domain.MaintenanceWindow, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows WHERE org_id = ? AND public = ? ORDER BY start_at`,
		orgID, s.d.BoolArg(true))
	if err != nil {
		return nil, err
	}
	return s.scanMWRows(rows)
}

func (s *MaintenanceWindowStore) ListAll(ctx context.Context) ([]*domain.MaintenanceWindow, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows ORDER BY start_at DESC`)
	if err != nil {
		return nil, err
	}
	return s.scanMWRows(rows)
}

func (s *MaintenanceWindowStore) Update(ctx context.Context, mw *domain.MaintenanceWindow) error {
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	if mw.Timezone == "" {
		mw.Timezone = domain.DefaultMaintenanceTimezone
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE maintenance_windows SET name = ?, description = ?, start_at = ?, end_at = ?, public = ?, suppress_alerts = ?, monitor_id = ?,
		 recurrence_type = ?, recurrence_end_at = ?, days_of_week = ?, day_of_month = ?, timezone = ?
		 WHERE id = ?`,
		mw.Name, mw.Description,
		s.d.TimeArg(mw.StartAt), s.d.TimeArg(mw.EndAt),
		s.d.BoolArg(mw.Public), s.d.BoolArg(mw.SuppressAlerts), mw.MonitorID,
		mw.RecurrenceType, s.d.OptionalTimeArg(mw.RecurrenceEndAt), mw.DaysOfWeek, mw.DayOfMonth, mw.Timezone, mw.ID)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MaintenanceWindowStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return ExpectOneRow(res)
}

func (s *MaintenanceWindowStore) scanMW(row *sql.Row) (*domain.MaintenanceWindow, error) {
	var mw domain.MaintenanceWindow
	var startAt, endAt, createdAt, recEnd Time
	var pub, suppress Bool
	if err := row.Scan(&mw.ID, &mw.OrgID, &mw.MonitorID, &mw.Name, &mw.Description,
		&startAt, &endAt, &pub, &suppress,
		&mw.RecurrenceType, &recEnd, &mw.DaysOfWeek, &mw.DayOfMonth,
		&mw.Timezone, &createdAt, &mw.CreatedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("maintenance window not found")
		}
		return nil, err
	}
	mw.StartAt = startAt.T
	mw.EndAt = endAt.T
	mw.CreatedAt = createdAt.T
	mw.RecurrenceEndAt = recEnd.TimePtr()
	mw.Public = bool(pub)
	mw.SuppressAlerts = bool(suppress)
	return &mw, nil
}

func (s *MaintenanceWindowStore) scanMWRows(rows *sql.Rows) ([]*domain.MaintenanceWindow, error) {
	defer func() { _ = rows.Close() }()
	var out []*domain.MaintenanceWindow
	for rows.Next() {
		var mw domain.MaintenanceWindow
		var startAt, endAt, createdAt, recEnd Time
		var pub, suppress Bool
		if err := rows.Scan(&mw.ID, &mw.OrgID, &mw.MonitorID, &mw.Name, &mw.Description,
			&startAt, &endAt, &pub, &suppress,
			&mw.RecurrenceType, &recEnd, &mw.DaysOfWeek, &mw.DayOfMonth,
			&mw.Timezone, &createdAt, &mw.CreatedBy); err != nil {
			return nil, err
		}
		mw.StartAt = startAt.T
		mw.EndAt = endAt.T
		mw.CreatedAt = createdAt.T
		mw.RecurrenceEndAt = recEnd.TimePtr()
		mw.Public = bool(pub)
		mw.SuppressAlerts = bool(suppress)
		out = append(out, &mw)
	}
	return out, rows.Err()
}

// ListLifecycleStates implements store.MaintenanceWindowStore.
func (s *MaintenanceWindowStore) ListLifecycleStates(ctx context.Context) ([]store.MaintenanceLifecycleState, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT window_id, org_id, monitor_id, active FROM maintenance_lifecycle_state`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []store.MaintenanceLifecycleState
	for rows.Next() {
		var st store.MaintenanceLifecycleState
		if err := rows.Scan(&st.WindowID, &st.OrgID, &st.MonitorID, &st.Active); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SeedLifecycleState implements store.MaintenanceWindowStore.
func (s *MaintenanceWindowStore) SeedLifecycleState(ctx context.Context, st store.MaintenanceLifecycleState, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`INSERT INTO maintenance_lifecycle_state (window_id, org_id, monitor_id, active, changed_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT (window_id) DO NOTHING`,
		st.WindowID, st.OrgID, st.MonitorID, s.d.BoolArg(st.Active), s.d.TimeArg(at))
	return affectedOne(res, err)
}

// ClaimLifecycleTransition implements store.MaintenanceWindowStore.
func (s *MaintenanceWindowStore) ClaimLifecycleTransition(ctx context.Context, windowID string, from bool, at time.Time) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`UPDATE maintenance_lifecycle_state SET active = ?, changed_at = ?
		  WHERE window_id = ? AND active = ?`,
		s.d.BoolArg(!from), s.d.TimeArg(at), windowID, s.d.BoolArg(from))
	return affectedOne(res, err)
}

// ClaimLifecycleDeletion implements store.MaintenanceWindowStore.
func (s *MaintenanceWindowStore) ClaimLifecycleDeletion(ctx context.Context, windowID string) (bool, error) {
	res, err := s.q.ExecContext(ctx,
		`DELETE FROM maintenance_lifecycle_state WHERE window_id = ?`, windowID)
	return affectedOne(res, err)
}
