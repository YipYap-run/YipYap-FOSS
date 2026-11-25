package sqlx

import (
	"context"
	"database/sql"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// Queryable is the common interface satisfied by both *sql.DB and *sql.Tx so
// store methods work identically inside and outside a transaction.
type Queryable interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ExpectOneRow returns an error when the result affected zero rows.
func ExpectOneRow(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("not found")
	}
	return nil
}

// NullString returns a sql.NullString for s, treating "" as NULL.
func NullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

// Migration is a numbered SQL migration file discovered from an fs.FS.
type Migration struct {
	Version  int
	Filename string
}

// DiscoverMigrations reads *.sql files from dir, extracts the numeric version
// prefix from each filename (e.g. "001_init.sql" -> 1), and returns them
// sorted by version.
func DiscoverMigrations(fsys fs.FS, dir string) ([]Migration, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}
	var migrations []Migration
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		migrations = append(migrations, Migration{
			Version:  version,
			Filename: dir + "/" + entry.Name(),
		})
	}
	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})
	return migrations, nil
}

// Dialect parameterizes the parts of the migration runner that differ between
// backends: the placeholder style, the schema_migrations DDL, and the
// applied_at value encoding.
type Dialect struct {
	Placeholder func(n int) string
	SchemaDDL   string
	AppliedAt   func() any

	// NoTransaction disables the per-migration transaction. YugabyteDB DDL is
	// non-transactional, so wrapping a migration in a tx makes the driver abort
	// mid-migrate. Migrations are written idempotently (IF NOT EXISTS), so
	// running them outside a tx is safe and recoverable.
	NoTransaction bool

	// Sub-store arg encoding (write side). The read side is handled uniformly
	// by the sqlx.Time and sqlx.Bool scanners.
	Now             func() any
	TimeArg         func(t time.Time) any
	OptionalTimeArg func(t *time.Time) any
	BoolArg         func(b bool) any

	// ForUpdateSkipLocked is appended after a LIMIT in a claim subquery to
	// skip rows already locked by another worker. Postgres uses
	// " FOR UPDATE SKIP LOCKED"; SQLite (single-writer) uses "".
	ForUpdateSkipLocked string

	// Millisecond time args for the cloud-event/reply tables, whose SQLite
	// columns default to strftime('%Y-%m-%dT%H:%M:%fZ') and are compared
	// lexicographically (e.g. `expires_at > ?`). SQLite formats a fixed
	// "2006-01-02T15:04:05.000Z" string; Postgres passes time.Time natively.
	MillisTimeArg         func(t time.Time) any
	MillisOptionalTimeArg func(t *time.Time) any

	// JSONB column casts for the reply-audit store. Postgres stores the
	// before/after state as jsonb (write `?::jsonb`, read `col::text`);
	// SQLite stores plain TEXT (no cast).
	JSONBArgSuffix string
	JSONBColSuffix string
}

// Migrate runs pending migrations from fsys/dir against db. Each migration is
// applied inside a transaction so a mid-file failure does not leave a
// partially-applied, unrecorded migration behind, unless the dialect opts out
// (NoTransaction) because its DDL is not transactional.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS, dir string, d Dialect) error {
	if _, err := db.ExecContext(ctx, d.SchemaDDL); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	var current int
	if err := db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current); err != nil {
		return fmt.Errorf("get migration version: %w", err)
	}

	migrations, err := DiscoverMigrations(fsys, dir)
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		data, err := fs.ReadFile(fsys, m.Filename)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", m.Version, err)
		}

		if check := migrationPrecheck(m.Filename); check != nil {
			if err := check(ctx, db, m); err != nil {
				return err
			}
		}

		if d.NoTransaction {
			if err := applyMigration(ctx, db, string(data), m, d); err != nil {
				return err
			}
			continue
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("begin migration %d: %w", m.Version, err)
		}
		if err := applyMigration(ctx, tx, string(data), m, d); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit migration %d: %w", m.Version, err)
		}
	}
	return nil
}

// applyMigration executes each statement in data, then records the version, on
// the given queryable (either *sql.DB or *sql.Tx).
func applyMigration(ctx context.Context, q Queryable, data string, m Migration, d Dialect) error {
	for _, stmt := range store.SplitSQLStatements(data) {
		if _, err := q.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("apply migration %d (%s): %w", m.Version, m.Filename, err)
		}
	}
	if _, err := q.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, applied_at) VALUES ("+d.Placeholder(1)+", "+d.Placeholder(2)+")",
		m.Version, d.AppliedAt(),
	); err != nil {
		return fmt.Errorf("record migration %d: %w", m.Version, err)
	}
	return nil
}
