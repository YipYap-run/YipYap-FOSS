package store

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ListParams provides common pagination parameters.
type ListParams struct {
	Limit  int    `json:"limit"`
	Offset int    `json:"offset"`
	Sort   string `json:"sort"`
}

// MonitorFilter extends ListParams with monitor-specific filters.
type MonitorFilter struct {
	ListParams
	Type    string            `json:"type,omitempty"`
	Status  string            `json:"status,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

// AlertFilter extends ListParams with alert-specific filters.
type AlertFilter struct {
	ListParams
	Status    string `json:"status,omitempty"`
	Severity  string `json:"severity,omitempty"`
	MonitorID string `json:"monitor_id,omitempty"`
}

// CheckFilter extends ListParams with time-range and status filters for checks.
type CheckFilter struct {
	ListParams
	Since  *time.Time `json:"since,omitempty"`
	Until  *time.Time `json:"until,omitempty"`
	Status string     `json:"status,omitempty"`
}

// Store is the top-level interface for all data access.
type Store interface {
	Orgs() OrgStore
	Users() UserStore
	OIDC() OIDCStore
	Monitors() MonitorStore
	Checks() CheckStore
	Alerts() AlertStore
	Teams() TeamStore
	Schedules() ScheduleStore
	EscalationPolicies() EscalationPolicyStore
	NotificationChannels() NotificationChannelStore
	MaintenanceWindows() MaintenanceWindowStore
	APIKeys() APIKeyStore

	// Dedup returns the notification dedup store.
	Dedup() DedupStore
	// Outbox returns the notification outbox store.
	Outbox() OutboxStore
	// Billing returns the billing store.
	Billing() BillingStore
	// Stats returns the stats store (may be nil for FOSS builds).
	Stats() StatsStore
	// MFA returns the MFA store (SaaS-only; FOSS returns stub).
	MFA() MFAStore

	// Tx executes fn within a database transaction. The Store passed to fn
	// shares the transaction. If fn returns an error, the transaction is
	// rolled back; otherwise it is committed.
	Tx(ctx context.Context, fn func(Store) error) error

	// Migrate runs any pending database migrations.
	Migrate(ctx context.Context) error

	// Close releases all resources held by the store.
	Close() error
}

// OrgStore manages organization CRUD.
type OrgStore interface {
	Create(ctx context.Context, org *domain.Org) error
	GetByID(ctx context.Context, id string) (*domain.Org, error)
	GetBySlug(ctx context.Context, slug string) (*domain.Org, error)
	Update(ctx context.Context, org *domain.Org) error
	Delete(ctx context.Context, id string) error
}

// UserStore manages user CRUD within an organization.
type UserStore interface {
	Create(ctx context.Context, user *domain.User) error
	GetByID(ctx context.Context, id string) (*domain.User, error)
	GetByEmail(ctx context.Context, email string) (*domain.User, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.User, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id string) error
	Disable(ctx context.Context, id string, at time.Time) error
	Enable(ctx context.Context, id string) error
	ListDisabledBefore(ctx context.Context, before time.Time) ([]*domain.User, error)
}

// OIDCStore manages OIDC connections and user links.
type OIDCStore interface {
	CreateConnection(ctx context.Context, conn *domain.OIDCConnection) error
	GetConnection(ctx context.Context, id string) (*domain.OIDCConnection, error)
	ListConnectionsByOrg(ctx context.Context, orgID string) ([]*domain.OIDCConnection, error)
	UpdateConnection(ctx context.Context, conn *domain.OIDCConnection) error
	DeleteConnection(ctx context.Context, id string) error
	LinkUser(ctx context.Context, link *domain.UserOIDCLink) error
	GetUserByOIDC(ctx context.Context, connectionID, externalSubjectID string) (*domain.User, error)
	ListAllEnabled(ctx context.Context) ([]*domain.OIDCConnection, error)
}

// MonitorStore manages monitors and their labels.
type MonitorStore interface {
	Create(ctx context.Context, m *domain.Monitor) error
	GetByID(ctx context.Context, id string) (*domain.Monitor, error)
	ListByOrg(ctx context.Context, orgID string, filter MonitorFilter) ([]*domain.Monitor, error)
	Update(ctx context.Context, m *domain.Monitor) error
	Delete(ctx context.Context, id string) error
	ListAllEnabled(ctx context.Context) ([]*domain.Monitor, error)
	GetByIntegrationKey(ctx context.Context, key string) (*domain.Monitor, error)
	GetNamesByIDs(ctx context.Context, orgID string, ids []string) (map[string]string, error)
	SetLabels(ctx context.Context, monitorID string, labels map[string]string) error
	GetLabels(ctx context.Context, monitorID string) (map[string]string, error)
	DeleteLabel(ctx context.Context, monitorID, key string) error
}

// CheckStore manages monitor checks and rollups.
type CheckStore interface {
	Create(ctx context.Context, check *domain.MonitorCheck) error
	ListByMonitor(ctx context.Context, monitorID string, filter CheckFilter) ([]*domain.MonitorCheck, error)
	GetLatest(ctx context.Context, monitorID string) (*domain.MonitorCheck, error)
	// GetLatestByStatus returns the most recent check matching the given
	// status, or (nil, nil) if none. Needed for heartbeat evaluation where
	// the scheduler must distinguish actual pings (status=up) from its own
	// Down transition writes in the same table.
	GetLatestByStatus(ctx context.Context, monitorID string, status domain.CheckStatus) (*domain.MonitorCheck, error)
	GetLatestByMonitors(ctx context.Context, monitorIDs []string) (map[string]*domain.MonitorCheck, error)
	GetStatusSince(ctx context.Context, monitorID string, status domain.CheckStatus) (*time.Time, error)
	CreateRollup(ctx context.Context, rollup *domain.MonitorRollup) error
	GetRollups(ctx context.Context, monitorID, period string) ([]*domain.MonitorRollup, error)
	CountByMonitor(ctx context.Context, monitorID string, filter CheckFilter) (int64, error)
	// AggregateByOrg returns total check count, oldest check time, and newest
	// check time across all monitors belonging to the given org.
	AggregateByOrg(ctx context.Context, monitorIDs []string) (*CheckAggregate, error)
	PruneBefore(ctx context.Context, monitorID string, before time.Time) (int64, error)
}

// CheckAggregate holds aggregate stats across monitors.
type CheckAggregate struct {
	TotalChecks int64      `json:"total_checks"`
	Oldest      *time.Time `json:"oldest,omitempty"`
	Newest      *time.Time `json:"newest,omitempty"`
}

// AlertStore manages alerts, events, and escalation state.
type AlertStore interface {
	Create(ctx context.Context, alert *domain.Alert) error
	GetByID(ctx context.Context, id string) (*domain.Alert, error)
	ListByOrg(ctx context.Context, orgID string, filter AlertFilter) ([]*domain.Alert, error)
	GetActiveByMonitor(ctx context.Context, monitorID string) (*domain.Alert, error)
	Update(ctx context.Context, alert *domain.Alert) error
	ListFiring(ctx context.Context) ([]*domain.Alert, error)
	CreateEvent(ctx context.Context, event *domain.AlertEvent) error
	ListEvents(ctx context.Context, alertID string) ([]*domain.AlertEvent, error)
	GetEscalationState(ctx context.Context, alertID string) (*domain.AlertEscalationState, error)
	UpsertEscalationState(ctx context.Context, state *domain.AlertEscalationState) error
	DeleteEscalationState(ctx context.Context, alertID string) error
}

// TeamStore manages teams and their membership.
type TeamStore interface {
	Create(ctx context.Context, team *domain.Team) error
	GetByID(ctx context.Context, id string) (*domain.Team, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.Team, error)
	Update(ctx context.Context, team *domain.Team) error
	Delete(ctx context.Context, id string) error
	AddMember(ctx context.Context, member *domain.TeamMember) error
	RemoveMember(ctx context.Context, teamID, userID string) error
	UpdateMember(ctx context.Context, member *domain.TeamMember) error
	ListMembers(ctx context.Context, teamID string) ([]*domain.TeamMember, error)
}

// ScheduleStore manages on-call schedules and overrides.
type ScheduleStore interface {
	Create(ctx context.Context, sched *domain.Schedule) error
	GetByID(ctx context.Context, id string) (*domain.Schedule, error)
	ListByTeam(ctx context.Context, teamID string) ([]*domain.Schedule, error)
	ListByOrg(ctx context.Context, orgID string) ([]*domain.Schedule, error)
	Update(ctx context.Context, sched *domain.Schedule) error
	Delete(ctx context.Context, id string) error
	CreateOverride(ctx context.Context, o *domain.ScheduleOverride) error
	GetOverrideByID(ctx context.Context, id string) (*domain.ScheduleOverride, error)
	ListOverrides(ctx context.Context, scheduleID string) ([]*domain.ScheduleOverride, error)
	DeleteOverride(ctx context.Context, id string) error
}

// EscalationPolicyStore manages escalation policies, steps, and targets.
type EscalationPolicyStore interface {
	Create(ctx context.Context, policy *domain.EscalationPolicy) error
	GetByID(ctx context.Context, id string) (*domain.EscalationPolicy, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.EscalationPolicy, error)
	Update(ctx context.Context, policy *domain.EscalationPolicy) error
	Delete(ctx context.Context, id string) error
	ReplaceSteps(ctx context.Context, policyID string, steps []domain.EscalationStep, targets map[string][]domain.StepTarget) error
	GetSteps(ctx context.Context, policyID string) ([]domain.EscalationStep, error)
	GetTargets(ctx context.Context, stepID string) ([]domain.StepTarget, error)
	GetNextStep(ctx context.Context, policyID string, currentPosition int) (*domain.EscalationStep, error)
}

// NotificationChannelStore manages notification channels.
type NotificationChannelStore interface {
	Create(ctx context.Context, ch *domain.NotificationChannel) error
	GetByID(ctx context.Context, id string) (*domain.NotificationChannel, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.NotificationChannel, error)
	Update(ctx context.Context, ch *domain.NotificationChannel) error
	Delete(ctx context.Context, id string) error
}

// MaintenanceWindowStore manages maintenance windows.
type MaintenanceWindowStore interface {
	Create(ctx context.Context, mw *domain.MaintenanceWindow) error
	GetByID(ctx context.Context, id string) (*domain.MaintenanceWindow, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.MaintenanceWindow, error)
	ListActiveByMonitor(ctx context.Context, monitorID string, at time.Time) ([]*domain.MaintenanceWindow, error)
	ListPublicByOrg(ctx context.Context, orgID string) ([]*domain.MaintenanceWindow, error)
	Update(ctx context.Context, mw *domain.MaintenanceWindow) error
	Delete(ctx context.Context, id string) error
}

// APIKeyStore manages API keys.
type APIKeyStore interface {
	Create(ctx context.Context, key *domain.APIKey) error
	GetByID(ctx context.Context, id string) (*domain.APIKey, error)
	GetByHash(ctx context.Context, hash string) (*domain.APIKey, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.APIKey, error)
	Delete(ctx context.Context, id string) error
	UpdateLastUsed(ctx context.Context, id string, at time.Time) error
}

// OutboxStore manages the notification outbox for at-least-once delivery.
type OutboxStore interface {
	// Enqueue writes a notification job to the outbox.
	Enqueue(ctx context.Context, id, payload string) error
	// Claim atomically claims up to `limit` pending jobs for this worker.
	// Jobs that were claimed longer than `staleAfter` ago are also reclaimed.
	Claim(ctx context.Context, workerID string, limit int, staleAfter time.Duration) ([]OutboxJob, error)
	// Complete marks a job as done.
	Complete(ctx context.Context, id string) error
	// Fail increments the attempt counter. If attempts >= maxAttempts, marks as dead.
	Fail(ctx context.Context, id string, maxAttempts int) error
	// Cleanup removes completed/dead jobs older than the given duration.
	Cleanup(ctx context.Context, olderThan time.Duration) error
}

// OutboxJob is a row from the notification outbox.
type OutboxJob struct {
	ID       string
	Payload  string
	Attempts int
}

// BillingStore manages billing state for paying orgs.
type BillingStore interface {
	Upsert(ctx context.Context, b *domain.Billing) error
	GetByOrgID(ctx context.Context, orgID string) (*domain.Billing, error)
	UpdateStatus(ctx context.Context, orgID, status string) error
	UpdateSeatCount(ctx context.Context, orgID string, count int) error
	IncrementSMSUsed(ctx context.Context, orgID string) error
	IncrementVoiceUsed(ctx context.Context, orgID string) error
	ResetSMSVoice(ctx context.Context, orgID string) error
	Delete(ctx context.Context, orgID string) error
}

// DedupStore manages notification deduplication across instances.
type DedupStore interface {
	// Claim attempts to insert a dedup key. Returns true if this caller won
	// the claim (first insert). Returns false if the key already exists and
	// has not expired.
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
	// Cleanup removes expired entries.
	Cleanup(ctx context.Context) error
}

// PlatformStats holds aggregate counts for telemetry reporting.
type PlatformStats struct {
	TotalUsers      int64
	TotalMonitors   int64
	MonitorsByType  map[string]int64 // e.g. "http" -> 42
	CustomersByPlan map[string]int64 // e.g. "pro" -> 10
}

// StatsStore provides aggregate platform metrics for telemetry.
type StatsStore interface {
	GetPlatformStats(ctx context.Context) (*PlatformStats, error)
}

// MFAStore manages TOTP secrets and WebAuthn credentials (SaaS-only).
type MFAStore interface {
	SetTOTPSecret(ctx context.Context, userID string, encryptedSecret string) error
	GetTOTPSecret(ctx context.Context, userID string) (string, error)
	EnableTOTP(ctx context.Context, userID string, hashedBackupCodes []string) error
	DisableTOTP(ctx context.Context, userID string) error
	GetBackupCodes(ctx context.Context, userID string) ([]string, error)
	UseBackupCode(ctx context.Context, userID string, remainingCodes []string) error
	CreateWebAuthnCredential(ctx context.Context, cred *domain.WebAuthnCredential) error
	ListWebAuthnCredentials(ctx context.Context, userID string) ([]*domain.WebAuthnCredential, error)
	GetWebAuthnCredential(ctx context.Context, id string) (*domain.WebAuthnCredential, error)
	GetWebAuthnCredentialByUserHandle(ctx context.Context, userHandle []byte) (*domain.WebAuthnCredential, error)
	UpdateWebAuthnSignCount(ctx context.Context, id string, signCount uint32) error
	DeleteWebAuthnCredential(ctx context.Context, id string) error
}
