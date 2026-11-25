package sqlite

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"testing"
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
	db     *sql.DB
	q      queryable // either db or a tx
	memory bool      // opened with dsn ":memory:"
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

	return &SQLiteStore{db: db, q: db, memory: dsn == ":memory:"}, nil
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

	MillisTimeArg: func(t time.Time) any {
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	},
	MillisOptionalTimeArg: func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return t.UTC().Format("2006-01-02T15:04:05.000Z")
	},
	// SQLite stores jsonb columns as plain TEXT.
	JSONBArgSuffix: "",
	JSONBColSuffix: "",
}

// Migrate runs all pending migrations against the database.
// Migrations are auto-discovered from the embedded migrations/ directory.
// Files must be named with a numeric prefix: 001_name.sql, 002_name.sql, etc.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	if s.memory && testing.Testing() {
		return s.migrateFromTestSnapshot(ctx)
	}
	return s.migrate(ctx)
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
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

func (s *SQLiteStore) Orgs() store.OrgStore                           { return sqlx.NewOrgStore(s.q, dialect) }
func (s *SQLiteStore) Users() store.UserStore                         { return sqlx.NewUserStore(s.q, dialect) }
func (s *SQLiteStore) MonitorStates() store.MonitorStateStore          { return sqlx.NewMonitorStateStore(s.q, dialect) }
func (s *SQLiteStore) MonitorMatchRules() store.MonitorMatchRuleStore  { return sqlx.NewMonitorMatchRuleStore(s.q, dialect) }
func (s *SQLiteStore) OIDC() store.OIDCStore                          { return sqlx.NewOIDCStore(s.q, dialect) }
func (s *SQLiteStore) Monitors() store.MonitorStore                   { return sqlx.NewMonitorStore(s.q, s.db, false, dialect) }
func (s *SQLiteStore) Checks() store.CheckStore                       { return sqlx.NewCheckStore(s.q, s.db, false, dialect) }
func (s *SQLiteStore) Alerts() store.AlertStore                       { return sqlx.NewAlertStore(s.q, dialect) }
func (s *SQLiteStore) Teams() store.TeamStore                         { return sqlx.NewTeamStore(s.q, dialect) }
func (s *SQLiteStore) MonitorGroups() store.MonitorGroupStore          { return sqlx.NewMonitorGroupStore(s.q, dialect) }
func (s *SQLiteStore) Schedules() store.ScheduleStore                 { return sqlx.NewScheduleStore(s.q, dialect) }
func (s *SQLiteStore) EscalationPolicies() store.EscalationPolicyStore { return sqlx.NewEscalationPolicyStore(s.q, dialect) }
func (s *SQLiteStore) NotificationChannels() store.NotificationChannelStore { return sqlx.NewNotificationChannelStore(s.q, dialect) }
func (s *SQLiteStore) MaintenanceWindows() store.MaintenanceWindowStore { return sqlx.NewMaintenanceWindowStore(s.q, dialect) }
func (s *SQLiteStore) APIKeys() store.APIKeyStore                      { return sqlx.NewAPIKeyStore(s.q, dialect) }
func (s *SQLiteStore) Dedup() store.DedupStore                         { return sqlx.NewDedupStore(s.q, dialect) }
func (s *SQLiteStore) RateBudget() store.RateBudgetStore               { return sqlx.NewRateBudgetStore(s.q, dialect) }
func (s *SQLiteStore) Usage() store.UsageStore               { return sqlx.NewUsageStore(s.q, dialect) }
func (s *SQLiteStore) Invites() store.InviteStore           { return sqlx.NewInviteStore(s.q, dialect) }
func (s *SQLiteStore) Promos() store.PromoStore             { return sqlx.NewPromoStore(s.q, dialect) }
func (s *SQLiteStore) WebhookEvents() store.WebhookEventStore { return sqlx.NewWebhookEventStore(s.q, dialect) }
func (s *SQLiteStore) Outbox() store.OutboxStore                       { return sqlx.NewOutboxStore(s.q, dialect) }
func (s *SQLiteStore) Billing() store.BillingStore                     { return sqlx.NewBillingStore(s.q, dialect) }
func (s *SQLiteStore) Stats() store.StatsStore                         { return nil }
func (s *SQLiteStore) MFA() store.MFAStore                             { return sqlx.NewMFAStore(s.q, dialect) }
func (s *SQLiteStore) OrgSettings() store.OrgSettingsStore              { return sqlx.NewOrgSettingsStore(s.q, dialect) }
func (s *SQLiteStore) Sessions() store.SessionStore { return sqlx.NewSessionStore(s.q, dialect) }
func (s *SQLiteStore) CloudEventIngest() store.CloudEventIngest          { return sqlx.NewCloudEventIngestStore(s.q, dialect) }
func (s *SQLiteStore) CloudEventPositions() store.CloudEventPositionStore { return sqlx.NewCloudEventPositionStore(s.q, dialect) }
func (s *SQLiteStore) CloudEventReplyAudit() store.CloudEventReplyAudit  { return sqlx.NewCloudEventReplyAuditStore(s.q, dialect) }
func (s *SQLiteStore) AlertSuppressions() store.AlertSuppressionStore    { return sqlx.NewAlertSuppressionStore(s.q, dialect) }
func (s *SQLiteStore) AlertRemediations() store.AlertRemediationStore    { return sqlx.NewAlertRemediationStore(s.q, dialect) }
func (s *SQLiteStore) AlertRoutingRules() store.AlertRoutingRuleStore    { return sqlx.NewAlertRoutingRuleStore(s.q, dialect) }
func (s *SQLiteStore) AlertStatusPageRefs() store.AlertStatusPageRefStore { return sqlx.NewAlertStatusPageRefStore(s.q, dialect) }
func (s *SQLiteStore) OIDCSigningKeys() store.OIDCSigningKeyStore        { return sqlx.NewOIDCSigningKeyStore(s.q, dialect) }
