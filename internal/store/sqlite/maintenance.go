package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

type maintenanceWindowStore struct{ q queryable }

const mwCols = `id, org_id, monitor_id, name, description, start_at, end_at, public, suppress_alerts, recurrence_type, recurrence_end_at, days_of_week, day_of_month, created_at, created_by`

func (s *maintenanceWindowStore) Create(ctx context.Context, mw *domain.MaintenanceWindow) error {
	var recEnd *string
	if mw.RecurrenceEndAt != nil {
		v := mw.RecurrenceEndAt.UTC().Format(timeFormat)
		recEnd = &v
	}
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO maintenance_windows (`+mwCols+`)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		mw.ID, mw.OrgID, mw.MonitorID, mw.Name, mw.Description,
		mw.StartAt.UTC().Format(timeFormat),
		mw.EndAt.UTC().Format(timeFormat),
		boolToInt(mw.Public), boolToInt(mw.SuppressAlerts),
		mw.RecurrenceType, recEnd, mw.DaysOfWeek, mw.DayOfMonth,
		mw.CreatedAt.UTC().Format(timeFormat), mw.CreatedBy)
	return err
}

func (s *maintenanceWindowStore) GetByID(ctx context.Context, id string) (*domain.MaintenanceWindow, error) {
	return s.scanMW(s.q.QueryRowContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows WHERE id = ?`, id))
}

func (s *maintenanceWindowStore) ListByOrg(ctx context.Context, orgID string, params store.ListParams) ([]*domain.MaintenanceWindow, error) {
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

func (s *maintenanceWindowStore) ListActiveByMonitor(ctx context.Context, monitorID string, at time.Time) ([]*domain.MaintenanceWindow, error) {
	atStr := at.UTC().Format(timeFormat)
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows
		 WHERE monitor_id = ?
		   AND suppress_alerts = 1
		   AND (
		     (recurrence_type = 'none' AND start_at <= ? AND end_at >= ?)
		     OR
		     (recurrence_type != 'none' AND start_at <= ? AND (recurrence_end_at IS NULL OR recurrence_end_at >= ?))
		   )`,
		monitorID, atStr, atStr, atStr, atStr)
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

func (s *maintenanceWindowStore) ListPublicByOrg(ctx context.Context, orgID string) ([]*domain.MaintenanceWindow, error) {
	rows, err := s.q.QueryContext(ctx,
		`SELECT `+mwCols+` FROM maintenance_windows WHERE org_id = ? AND public = 1 ORDER BY start_at`,
		orgID)
	if err != nil {
		return nil, err
	}
	return s.scanMWRows(rows)
}

func (s *maintenanceWindowStore) Update(ctx context.Context, mw *domain.MaintenanceWindow) error {
	var recEnd *string
	if mw.RecurrenceEndAt != nil {
		v := mw.RecurrenceEndAt.UTC().Format(timeFormat)
		recEnd = &v
	}
	if mw.RecurrenceType == "" {
		mw.RecurrenceType = "none"
	}
	if mw.DaysOfWeek == "" {
		mw.DaysOfWeek = "[]"
	}
	res, err := s.q.ExecContext(ctx,
		`UPDATE maintenance_windows SET name = ?, description = ?, start_at = ?, end_at = ?, public = ?, suppress_alerts = ?, monitor_id = ?,
		 recurrence_type = ?, recurrence_end_at = ?, days_of_week = ?, day_of_month = ?
		 WHERE id = ?`,
		mw.Name, mw.Description,
		mw.StartAt.UTC().Format(timeFormat),
		mw.EndAt.UTC().Format(timeFormat),
		boolToInt(mw.Public), boolToInt(mw.SuppressAlerts), mw.MonitorID,
		mw.RecurrenceType, recEnd, mw.DaysOfWeek, mw.DayOfMonth, mw.ID)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *maintenanceWindowStore) Delete(ctx context.Context, id string) error {
	res, err := s.q.ExecContext(ctx, `DELETE FROM maintenance_windows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return expectOneRow(res)
}

func (s *maintenanceWindowStore) scanMW(row *sql.Row) (*domain.MaintenanceWindow, error) {
	var mw domain.MaintenanceWindow
	var startAt, endAt, createdAt string
	var recEnd *string
	var pub, suppress int
	if err := row.Scan(&mw.ID, &mw.OrgID, &mw.MonitorID, &mw.Name, &mw.Description,
		&startAt, &endAt, &pub, &suppress,
		&mw.RecurrenceType, &recEnd, &mw.DaysOfWeek, &mw.DayOfMonth,
		&createdAt, &mw.CreatedBy); err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("maintenance window not found")
		}
		return nil, err
	}
	mw.StartAt = mustParseTime(startAt)
	mw.EndAt = mustParseTime(endAt)
	mw.CreatedAt = mustParseTime(createdAt)
	if recEnd != nil {
		t := mustParseTime(*recEnd)
		mw.RecurrenceEndAt = &t
	}
	mw.Public = pub != 0
	mw.SuppressAlerts = suppress != 0
	return &mw, nil
}

func (s *maintenanceWindowStore) scanMWRows(rows *sql.Rows) ([]*domain.MaintenanceWindow, error) {
	defer func() { _ = rows.Close() }()
	var out []*domain.MaintenanceWindow
	for rows.Next() {
		var mw domain.MaintenanceWindow
		var startAt, endAt, createdAt string
		var recEnd *string
		var pub, suppress int
		if err := rows.Scan(&mw.ID, &mw.OrgID, &mw.MonitorID, &mw.Name, &mw.Description,
			&startAt, &endAt, &pub, &suppress,
			&mw.RecurrenceType, &recEnd, &mw.DaysOfWeek, &mw.DayOfMonth,
			&createdAt, &mw.CreatedBy); err != nil {
			return nil, err
		}
		mw.StartAt = mustParseTime(startAt)
		mw.EndAt = mustParseTime(endAt)
		mw.CreatedAt = mustParseTime(createdAt)
		if recEnd != nil {
			t := mustParseTime(*recEnd)
			mw.RecurrenceEndAt = &t
		}
		mw.Public = pub != 0
		mw.SuppressAlerts = suppress != 0
		out = append(out, &mw)
	}
	return out, rows.Err()
}
