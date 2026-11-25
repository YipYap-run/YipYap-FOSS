package sqlx

import (
	"context"
	"database/sql"
	"errors"
	"sync/atomic"
	"time"
)

var rateBudgetNextCleanup atomic.Int64

type RateBudgetStore struct {
	q Queryable
	d Dialect
}

func NewRateBudgetStore(q Queryable, d Dialect) *RateBudgetStore {
	return &RateBudgetStore{q: q, d: d}
}

func (s *RateBudgetStore) Take(ctx context.Context, key string, window time.Duration, now time.Time) (int, time.Time, error) {
	now = now.UTC()
	cutoff := now.Add(-window)
	_, err := s.q.ExecContext(ctx,
		`INSERT INTO rate_budget (budget_key, window_started_at, counter)
		 VALUES (?, ?, 1)
		 ON CONFLICT (budget_key) DO UPDATE SET
		   counter = CASE WHEN rate_budget.window_started_at <= ? THEN 1 ELSE rate_budget.counter + 1 END,
		   window_started_at = CASE WHEN rate_budget.window_started_at <= ? THEN ? ELSE rate_budget.window_started_at END`,
		key, s.d.MillisTimeArg(now),
		s.d.MillisTimeArg(cutoff), s.d.MillisTimeArg(cutoff), s.d.MillisTimeArg(now))
	if err != nil {
		return 0, time.Time{}, err
	}
	next := rateBudgetNextCleanup.Load()
	if now.Unix() >= next && rateBudgetNextCleanup.CompareAndSwap(next, now.Add(time.Hour).Unix()) {
		_, _ = s.q.ExecContext(ctx,
			`DELETE FROM rate_budget WHERE window_started_at < ?`,
			s.d.MillisTimeArg(now.Add(-24*time.Hour)))
	}
	return s.Peek(ctx, key, window, now)
}

func (s *RateBudgetStore) Peek(ctx context.Context, key string, window time.Duration, now time.Time) (int, time.Time, error) {
	now = now.UTC()
	var count int
	var started Time
	err := s.q.QueryRowContext(ctx,
		`SELECT counter, window_started_at FROM rate_budget WHERE budget_key = ?`, key).
		Scan(&count, &started)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, now.Add(window), nil
	}
	if err != nil {
		return 0, time.Time{}, err
	}
	resetAt := started.T.Add(window)
	if !now.Before(resetAt) {
		return 0, now.Add(window), nil
	}
	return count, resetAt, nil
}
