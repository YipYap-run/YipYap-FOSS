package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// queryable is the common interface between *sql.DB and *sql.Tx.
type queryable interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// SQLiteStore implements store.Store using SQLite via modernc.org/sqlite.
type SQLiteStore struct {
	db *sql.DB
	q  queryable // either db or a tx
}

// New opens a SQLite database at dsn and configures it for WAL mode,
// foreign keys, and busy timeout.
func New(dsn string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlite open: %w", err)
	}

	// SQLite single-writer.
	db.SetMaxOpenConns(1)

	// Pragmas: WAL, foreign keys, busy timeout.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("sqlite pragma %q: %w", pragma, err)
		}
	}

	return &SQLiteStore{db: db, q: db}, nil
}

// discoverMigrations reads all .sql files from the embedded migrations directory,
// extracts the version number from the filename prefix (e.g., "001_init.sql" → version 1),
// and returns them sorted by version.
func discoverMigrations() ([]struct {
	version  int
	filename string
}, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations dir: %w", err)
	}

	type migration struct {
		version  int
		filename string
	}
	var migrations []migration

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		// Extract version from prefix: "001_init.sql" → 1
		parts := strings.SplitN(entry.Name(), "_", 2)
		if len(parts) < 2 {
			continue
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		migrations = append(migrations, migration{
			version:  version,
			filename: "migrations/" + entry.Name(),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].version < migrations[j].version
	})

	// Convert to the return type
	result := make([]struct {
		version  int
		filename string
	}, len(migrations))
	for i, m := range migrations {
		result[i].version = m.version
		result[i].filename = m.filename
	}
	return result, nil
}

// Migrate runs all pending migrations against the database.
// Migrations are auto-discovered from the embedded migrations/ directory.
// Files must be named with a numeric prefix: 001_name.sql, 002_name.sql, etc.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	// Ensure the schema_migrations table exists.
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version INTEGER PRIMARY KEY,
		applied_at TEXT NOT NULL
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	// Check what version we're at.
	var current int
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&current)
	if err != nil {
		return fmt.Errorf("get migration version: %w", err)
	}

	migrations, err := discoverMigrations()
	if err != nil {
		return err
	}

	for _, m := range migrations {
		if m.version <= current {
			continue
		}
		data, err := migrationsFS.ReadFile(m.filename)
		if err != nil {
			return fmt.Errorf("read migration %d: %w", m.version, err)
		}
		// Split on semicolons to handle multiple statements per migration.
		for _, stmt := range strings.Split(string(data), ";") {
			stmt = strings.TrimSpace(stmt)
			if stmt == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply migration %d (%s): %w", m.version, m.filename, err)
			}
		}
		// Record applied timestamp in Go (portable  - no DB-specific datetime functions).
		if _, err := s.db.ExecContext(ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			m.version, timeNowUTC(),
		); err != nil {
			return fmt.Errorf("record migration %d: %w", m.version, err)
		}
	}
	return nil
}

// Tx executes fn within a database transaction.
func (s *SQLiteStore) Tx(ctx context.Context, fn func(store.Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}

	txStore := &SQLiteStore{db: s.db, q: tx}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}

	return tx.Commit()
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// Sub-store accessors.

func (s *SQLiteStore) Orgs() store.OrgStore                           { return &orgStore{q: s.q} }
func (s *SQLiteStore) Users() store.UserStore                         { return &userStore{q: s.q} }
func (s *SQLiteStore) OIDC() store.OIDCStore                          { return &oidcStore{q: s.q} }
func (s *SQLiteStore) Monitors() store.MonitorStore                   { return &monitorStore{q: s.q} }
func (s *SQLiteStore) Checks() store.CheckStore                       { return &checkStore{q: s.q} }
func (s *SQLiteStore) Alerts() store.AlertStore                       { return &alertStore{q: s.q} }
func (s *SQLiteStore) Teams() store.TeamStore                         { return &teamStore{q: s.q} }
func (s *SQLiteStore) Schedules() store.ScheduleStore                 { return &scheduleStore{q: s.q} }
func (s *SQLiteStore) EscalationPolicies() store.EscalationPolicyStore { return &escalationPolicyStore{q: s.q} }
func (s *SQLiteStore) NotificationChannels() store.NotificationChannelStore { return &notificationChannelStore{q: s.q} }
func (s *SQLiteStore) MaintenanceWindows() store.MaintenanceWindowStore { return &maintenanceWindowStore{q: s.q} }
func (s *SQLiteStore) APIKeys() store.APIKeyStore                      { return &apiKeyStore{q: s.q} }
func (s *SQLiteStore) Dedup() store.DedupStore                         { return &dedupStore{q: s.q} }
func (s *SQLiteStore) Outbox() store.OutboxStore                       { return &outboxStore{q: s.q} }
func (s *SQLiteStore) Billing() store.BillingStore                     { return &billingStore{q: s.q} }
func (s *SQLiteStore) Stats() store.StatsStore                         { return nil }
func (s *SQLiteStore) MFA() store.MFAStore                             { return &mfaStore{q: s.q} }
func (s *SQLiteStore) OrgSettings() store.OrgSettingsStore              { return &orgSettingsStore{q: s.q} }
