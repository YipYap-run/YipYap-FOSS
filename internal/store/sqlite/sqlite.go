package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/YipYap-run/YipYap-FOSS/internal/store/sqlx"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// queryable is the common interface between *sql.DB and *sql.Tx.
type queryable = sqlx.Queryable

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

// dialect parameterizes the shared store infrastructure for SQLite.
var dialect = sqlx.Dialect{
	Placeholder: func(int) string { return "?" },
	SchemaDDL:   `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`,
	AppliedAt:   func() any { return timeNowUTC() },
	Now:         func() any { return timeNowUTC() },
	TimeArg:     func(t time.Time) any { return t.UTC().Format(timeFormat) },
	OptionalTimeArg: func(t *time.Time) any {
		return formatOptionalTime(t)
	},
	BoolArg: func(b bool) any { return boolToInt(b) },
}

// Migrate runs all pending migrations against the database.
// Migrations are auto-discovered from the embedded migrations/ directory.
// Files must be named with a numeric prefix: 001_name.sql, 002_name.sql, etc.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	return sqlx.Migrate(ctx, s.db, migrationsFS, "migrations", dialect)
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
func (s *SQLiteStore) Teams() store.TeamStore                         { return sqlx.NewTeamStore(s.q, dialect) }
func (s *SQLiteStore) Schedules() store.ScheduleStore                 { return &scheduleStore{q: s.q} }
func (s *SQLiteStore) EscalationPolicies() store.EscalationPolicyStore { return sqlx.NewEscalationPolicyStore(s.q, dialect) }
func (s *SQLiteStore) NotificationChannels() store.NotificationChannelStore { return sqlx.NewNotificationChannelStore(s.q, dialect) }
func (s *SQLiteStore) MaintenanceWindows() store.MaintenanceWindowStore { return sqlx.NewMaintenanceWindowStore(s.q, dialect) }
func (s *SQLiteStore) APIKeys() store.APIKeyStore                      { return sqlx.NewAPIKeyStore(s.q, dialect) }
func (s *SQLiteStore) Dedup() store.DedupStore                         { return &dedupStore{q: s.q} }
func (s *SQLiteStore) Outbox() store.OutboxStore                       { return &outboxStore{q: s.q} }
func (s *SQLiteStore) Billing() store.BillingStore                     { return &billingStore{q: s.q} }
func (s *SQLiteStore) Stats() store.StatsStore                         { return nil }
func (s *SQLiteStore) MFA() store.MFAStore                             { return &mfaStore{q: s.q} }
func (s *SQLiteStore) OrgSettings() store.OrgSettingsStore              { return &orgSettingsStore{q: s.q} }
func (s *SQLiteStore) CloudEventIngest() store.CloudEventIngest          { return &cloudEventIngestStore{q: s.q} }
func (s *SQLiteStore) CloudEventReplyAudit() store.CloudEventReplyAudit  { return &cloudEventReplyAuditStore{q: s.q} }
func (s *SQLiteStore) AlertSuppressions() store.AlertSuppressionStore    { return &alertSuppressionStore{q: s.q} }
func (s *SQLiteStore) AlertRemediations() store.AlertRemediationStore    { return &alertRemediationStore{q: s.q} }
func (s *SQLiteStore) AlertRoutingRules() store.AlertRoutingRuleStore    { return &alertRoutingRuleStore{q: s.q} }
func (s *SQLiteStore) AlertStatusPageRefs() store.AlertStatusPageRefStore { return &alertStatusPageRefStore{q: s.q} }
func (s *SQLiteStore) OIDCSigningKeys() store.OIDCSigningKeyStore        { return &oidcSigningKeyStore{q: s.q} }
