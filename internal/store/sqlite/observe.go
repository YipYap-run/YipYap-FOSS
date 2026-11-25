package sqlite

import (
	"context"
	"database/sql"
)

// WithQueryObserver returns a view of the store that calls fn with every
// SQL statement it runs outside a transaction. It shares the database with
// s. Tests use it to check that a handler's query count does not grow with
// the number of rows it lists.
func (s *SQLiteStore) WithQueryObserver(fn func(query string)) *SQLiteStore {
	return &SQLiteStore{db: s.db, q: observedQueryable{q: s.q, fn: fn}, memory: s.memory}
}

type observedQueryable struct {
	q  queryable
	fn func(string)
}

func (o observedQueryable) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	o.fn(query)
	return o.q.ExecContext(ctx, query, args...)
}

func (o observedQueryable) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	o.fn(query)
	return o.q.QueryContext(ctx, query, args...)
}

func (o observedQueryable) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	o.fn(query)
	return o.q.QueryRowContext(ctx, query, args...)
}
