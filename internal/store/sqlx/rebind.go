package sqlx

import (
	"context"
	"database/sql"
	"strconv"
	"strings"
)

// Rebind rewrites ? placeholders to $N in order. It skips ? inside
// single-quoted string literals (including doubled ” escapes), which is safe
// for this codebase where ? is only ever a bind placeholder.
func Rebind(query string) string {
	var b strings.Builder
	n := 0
	inQuote := false
	for i := 0; i < len(query); i++ {
		c := query[i]
		if inQuote {
			b.WriteByte(c)
			if c == '\'' {
				if i+1 < len(query) && query[i+1] == '\'' {
					b.WriteByte(query[i+1])
					i++
				} else {
					inQuote = false
				}
			}
			continue
		}
		switch c {
		case '\'':
			inQuote = true
			b.WriteByte(c)
		case '?':
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// RebindingQueryable wraps a Queryable and rewrites ? placeholders to $N on
// every query. Postgres uses this so shared store code can write ? uniformly;
// SQLite passes the underlying Queryable directly.
type RebindingQueryable struct {
	Q Queryable
}

func (r RebindingQueryable) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return r.Q.ExecContext(ctx, Rebind(query), args...)
}

func (r RebindingQueryable) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return r.Q.QueryContext(ctx, Rebind(query), args...)
}

func (r RebindingQueryable) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return r.Q.QueryRowContext(ctx, Rebind(query), args...)
}

var _ Queryable = RebindingQueryable{}
