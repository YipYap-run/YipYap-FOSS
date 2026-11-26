package store

import (
	"context"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// ListParams provides common pagination parameters.
type ListParams struct {
	Limit  int `json:"limit"`
	Offset int `json:"offset"`
}

// UserFilter narrows a user listing. Empty fields do not filter.
type UserFilter struct {
	ListParams
	// Query matches a case-insensitive substring of the email or name.
	Query string
	// Role keeps users with exactly this role.
	Role string
	// IDs keeps only these user IDs (batch lookup by ID).
	IDs []string
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
	Status string `json:"status,omitempty"`
	// Statuses matches any of the listed statuses. It combines with
	// Status: both are matched as one set.
	Statuses  []string `json:"statuses,omitempty"`
	Severity  string   `json:"severity,omitempty"`
	MonitorID string   `json:"monitor_id,omitempty"`
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
	// Invites returns the member invite store.
	Invites() InviteStore

	// Dedup returns the notification dedup store.
	Dedup() DedupStore
	RateBudget() RateBudgetStore
	// WebhookEvents returns the inbound webhook event ledger.
	WebhookEvents() WebhookEventStore
	// Outbox returns the notification outbox store.
	Outbox() OutboxStore
	// Billing returns the billing store.
	Billing() BillingStore
	// Usage returns the metered SMS/voice usage store.
	Usage() UsageStore
	// Promos returns the promo lifecycle store (expiry downgrade and
	// warning markers).
	Promos() PromoStore
	// Stats returns the stats store (may be nil for FOSS builds).
	Stats() StatsStore
	// MFA returns the MFA store (SaaS-only; FOSS returns stub).
	MFA() MFAStore
	// OIDCSigningKeys returns the self-hosted JWKS signing key store.
	// May return nil on backends that do not implement it.
	OIDCSigningKeys() OIDCSigningKeyStore

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
	// CountByOrg returns the number of active (not disabled) users in the
	// org. It is the billable seat count.
	CountByOrg(ctx context.Context, orgID string) (int, error)
	// SearchByOrg returns one page of the org's active users that match the
	// filter, ordered by email, plus the total number of matches.
	SearchByOrg(ctx context.Context, orgID string, f UserFilter) ([]*domain.User, int, error)
	Update(ctx context.Context, user *domain.User) error
	Delete(ctx context.Context, id string) error
	Disable(ctx context.Context, id string, at time.Time) error
	Enable(ctx context.Context, id string) error
	// ListDisabledBefore returns disabled users not yet deleted whose
	// disabled_at is before the given time.
	ListDisabledBefore(ctx context.Context, before time.Time) ([]*domain.User, error)
	// Anonymize turns the user row into a deleted-user tombstone (placeholder
	// email, DeletedUserName, no credentials or contact details, deleted_at
	// set) and removes the user's WebAuthn credentials, OIDC links, team
	// memberships, unfinished schedule overrides and API keys. Records that
	// reference the user (tickets, incidents, updates, past overrides) stay.
	// Call it inside Tx. Fails when the user does not exist or is already
	// deleted.
	Anonymize(ctx context.Context, id string, at time.Time) error
	AnonymizeKeepAPIKeys(ctx context.Context, id string, at time.Time) error
	// MarkEmailVerified sets email_verified_at = now() for the given user.
	MarkEmailVerified(ctx context.Context, id string, at time.Time) error
	// ClearEmailVerified sets email_verified_at back to NULL (after an email
	// change) and resets the verification send counters so a fresh link can
	// go out right away.
	ClearEmailVerified(ctx context.Context, id string, at time.Time) error
	// RecordVerificationSend updates sent_at and increments the hourly
	// resend counter (resetting the window if expired).
	RecordVerificationSend(ctx context.Context, id string, at time.Time) error
	// ListUnverifiedUnsent returns users who have never been sent a
	// verification email (for the startup bulk send).
	ListUnverifiedUnsent(ctx context.Context) ([]*domain.User, error)
	// SetMFAEnforcedAt records the org MFA grace deadline for a user. It only
	// writes when no deadline is stored yet, so concurrent requests cannot
	// move it. Losing that race is not an error; re-read the user to get the
	// stored value.
	SetMFAEnforcedAt(ctx context.Context, id string, deadline time.Time) error
	// ExpireMFAGrace sets the MFA grace deadline to at, replacing any stored
	// value. Used when a user removes their last factor in an org that
	// requires MFA, so removal never earns a fresh grace period.
	ExpireMFAGrace(ctx context.Context, id string, at time.Time) error
	// ClearMFAEnforcedAtByOrg removes every stored MFA grace deadline in an
	// org, so a new MFA requirement starts a fresh grace period.
	ClearMFAEnforcedAtByOrg(ctx context.Context, orgID string) error
	// SetPendingEmail stores a requested email change and the nonce its
	// mailed links carry, replacing any earlier request.
	SetPendingEmail(ctx context.Context, id, email, nonce string, at time.Time) error
	// ClearPendingEmail cancels a pending change. A non-empty nonce limits
	// it to that request. Returns an error when nothing matched.
	ClearPendingEmail(ctx context.Context, id, nonce string) error
	// ConfirmPendingEmail makes the pending address the account email and
	// marks it verified, only if email and nonce match the stored request.
	// Returns an error when nothing matched.
	ConfirmPendingEmail(ctx context.Context, id, email, nonce string, at time.Time) error
}

// InviteStore manages member invites. An invite is pending while it has
// neither been accepted nor revoked; an org holds at most one pending invite
// per email address (case-insensitive).
type InviteStore interface {
	// Create inserts a pending invite. It fails if the org already has a
	// pending invite for the email.
	Create(ctx context.Context, inv *domain.OrgInvite) error
	GetByID(ctx context.Context, id string) (*domain.OrgInvite, error)
	// GetPendingByEmail returns the org's pending invite for email
	// (case-insensitive), or an error if there is none.
	GetPendingByEmail(ctx context.Context, orgID, email string) (*domain.OrgInvite, error)
	// ListPending returns the org's pending invites, expired ones included,
	// newest first.
	ListPending(ctx context.Context, orgID string) ([]*domain.OrgInvite, error)
	// CountActive returns the org's pending invites that have not expired
	// at now.
	CountActive(ctx context.Context, orgID string, now time.Time) (int, error)
	// Rotate replaces the token hash and expiry of a pending invite. It
	// fails if the invite is not pending.
	Rotate(ctx context.Context, id, tokenHash string, expiresAt time.Time) error
	// Revoke marks a pending invite revoked. It fails if the invite is not
	// pending.
	Revoke(ctx context.Context, id string, at time.Time) error
	// MarkAccepted marks the invite accepted only if it is still pending,
	// still carries tokenHash and has not expired at at. It reports whether
	// this call claimed the invite, so two concurrent accepts cannot both
	// succeed.
	MarkAccepted(ctx context.Context, id, tokenHash string, at time.Time) (bool, error)
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
	// ListLinksByUser returns every SSO identity linked to the user.
	ListLinksByUser(ctx context.Context, userID string) ([]*domain.UserOIDCLink, error)
	// UnlinkUser removes the user's identities on one connection.
	UnlinkUser(ctx context.Context, userID, connectionID string) error
	// CountLinksByConnection counts the identities linked through a connection.
	CountLinksByConnection(ctx context.Context, connectionID string) (int, error)

	// CreateDomain adds an unverified SSO email domain to a connection. It
	// returns ErrOIDCDomainClaimed when another org has verified the domain.
	CreateDomain(ctx context.Context, d *domain.OIDCDomain) error
	// GetDomain returns one domain row or ErrOIDCDomainNotFound.
	GetDomain(ctx context.Context, id string) (*domain.OIDCDomain, error)
	// ListDomainsByConnection returns the domains attached to a connection.
	ListDomainsByConnection(ctx context.Context, connectionID string) ([]*domain.OIDCDomain, error)
	// DeleteDomain removes one domain row or returns ErrOIDCDomainNotFound.
	DeleteDomain(ctx context.Context, id string) error
	// MarkDomainVerified sets verified_at. It is a no-op on an already
	// verified row and returns ErrOIDCDomainClaimed when another row for the
	// same domain is verified.
	MarkDomainVerified(ctx context.Context, id string, at time.Time) error
	// GetVerifiedDomain returns the verified row for an exact domain, or
	// ErrOIDCDomainNotFound.
	GetVerifiedDomain(ctx context.Context, name string) (*domain.OIDCDomain, error)
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
	// GetLabelsByMonitors returns the labels of each monitor that has any,
	// keyed by monitor ID, in one query.
	GetLabelsByMonitors(ctx context.Context, monitorIDs []string) (map[string]map[string]string, error)
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
	// GetLatestHeartbeatPing returns the most recent heartbeat POST the
	// ingest handler wrote (identified by the presence of source_ip in
	// metadata). Nil if no such row exists. Ignores legacy evaluator-
	// written Up rows that predate the source_ip enrichment.
	GetLatestHeartbeatPing(ctx context.Context, monitorID string) (*domain.MonitorCheck, error)
	GetLatestByMonitors(ctx context.Context, monitorIDs []string) (map[string]*domain.MonitorCheck, error)
	// ListRecentByMonitors returns up to perMonitor of the newest checks for
	// each monitor, newest first, keyed by monitor ID. It runs a fixed
	// number of queries however many monitors are passed (mon-18).
	ListRecentByMonitors(ctx context.Context, monitorIDs []string, perMonitor int) (map[string][]*domain.MonitorCheck, error)
	GetStatusSince(ctx context.Context, monitorID string, status domain.CheckStatus) (*time.Time, error)
	CreateRollup(ctx context.Context, rollup *domain.MonitorRollup) error
	// UpsertRollup inserts a rollup, or updates the metric columns if a row
	// for (monitor_id, period, period_start) already exists. Used by the
	// rollup aggregator, which may re-run a period.
	UpsertRollup(ctx context.Context, rollup *domain.MonitorRollup) error
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
	// UpdateIfStatus writes the same columns as Update, but only while the
	// stored status is one of from. It returns ErrAlertStateChanged when the
	// alert exists in another status.
	UpdateIfStatus(ctx context.Context, alert *domain.Alert, from ...domain.AlertStatus) error
	// Acknowledge moves a firing alert to acknowledged in one conditional
	// update. It reports false, without error, when the alert exists but
	// is no longer firing, so duplicate ack events change nothing.
	Acknowledge(ctx context.Context, alertID, ackedBy string, at time.Time) (bool, error)
	ListFiring(ctx context.Context) ([]*domain.Alert, error)
	CreateEvent(ctx context.Context, event *domain.AlertEvent) error
	// CreateEventOnce inserts the event unless one with the same ID exists
	// and reports whether it inserted a row.
	CreateEventOnce(ctx context.Context, event *domain.AlertEvent) (bool, error)
	ListEvents(ctx context.Context, alertID string) ([]*domain.AlertEvent, error)
	GetEscalationState(ctx context.Context, alertID string) (*domain.AlertEscalationState, error)
	UpsertEscalationState(ctx context.Context, state *domain.AlertEscalationState) error
	DeleteEscalationState(ctx context.Context, alertID string) error

	RecordPagedTarget(ctx context.Context, target *domain.AlertPagedTarget) error
	ListPagedTargets(ctx context.Context, alertID string) ([]*domain.AlertPagedTarget, error)
	DeletePagedTarget(ctx context.Context, alertID, targetKey string) error
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
	// CountOpenAlerts counts unresolved alerts on monitors that use the policy.
	CountOpenAlerts(ctx context.Context, policyID string) (int, error)
}

// NotificationChannelStore manages notification channels.
type NotificationChannelStore interface {
	Create(ctx context.Context, ch *domain.NotificationChannel) error
	GetByID(ctx context.Context, id string) (*domain.NotificationChannel, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.NotificationChannel, error)
	Update(ctx context.Context, ch *domain.NotificationChannel) error
	// SetVerifiedAt sets or clears the channel's verified_at. Update does
	// not write that column.
	SetVerifiedAt(ctx context.Context, id string, at *time.Time) error
	Delete(ctx context.Context, id string) error
	// DeleteIfUnused deletes the channel only when no escalation step in
	// the channel's org sends through it, in one statement so a policy
	// saved between a check and the delete cannot be left pointing at a
	// missing channel. found is false when no such channel exists;
	// deleted is false when it is still in use.
	DeleteIfUnused(ctx context.Context, id string) (found, deleted bool, err error)
	// ListPolicyRefs returns, per channel ID, the escalation policies in
	// orgID whose steps send through that channel, ordered by policy
	// name. channelID limits the result to one channel; empty means all.
	ListPolicyRefs(ctx context.Context, orgID, channelID string) (map[string][]domain.ChannelPolicyRef, error)
	// RecordDeliverySuccess stamps last_success_at, resets the
	// consecutive failure count and clears the failure notice. recovered
	// is true for exactly one caller when a failure notice was set, so the
	// "delivering again" email goes out once.
	RecordDeliverySuccess(ctx context.Context, id string, at time.Time) (recovered bool, err error)
	// RecordDeliveryFailure stamps last_failure_at, stores the sanitized
	// reason and returns the new consecutive failure count.
	RecordDeliveryFailure(ctx context.Context, id string, at time.Time, reason string) (int, error)
	// ClaimFailureNotice sets failure_notified_at to at when the channel
	// has failed at least threshold times in a row and no notice is set
	// for the current streak. It reports whether this caller won the
	// claim, so concurrent workers send the owner email once.
	ClaimFailureNotice(ctx context.Context, id string, threshold int, at time.Time) (bool, error)
	// ReleaseFailureNotice undoes a claim made at at (for example when the
	// email could not be sent), so a later failure can try again.
	ReleaseFailureNotice(ctx context.Context, id string, at time.Time) error
}

// MaintenanceWindowStore manages maintenance windows.
type MaintenanceWindowStore interface {
	Create(ctx context.Context, mw *domain.MaintenanceWindow) error
	GetByID(ctx context.Context, id string) (*domain.MaintenanceWindow, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.MaintenanceWindow, error)
	ListActiveByMonitor(ctx context.Context, monitorID string, at time.Time) ([]*domain.MaintenanceWindow, error)
	ListPublicByOrg(ctx context.Context, orgID string) ([]*domain.MaintenanceWindow, error)
	// ListAll returns every maintenance window across every org, without
	// pagination. Intended for the background ticker that computes
	// scheduled start/end transitions; not for user-facing APIs.
	ListAll(ctx context.Context) ([]*domain.MaintenanceWindow, error)
	Update(ctx context.Context, mw *domain.MaintenanceWindow) error
	Delete(ctx context.Context, id string) error

	// Lifecycle state shared by every replica's maintenance ticker, so a
	// start or end event is emitted once per transition, not once per
	// replica. Each call that changes state reports whether this caller
	// won the change; only the winner emits.

	// ListLifecycleStates returns the last emitted state of every tracked
	// window, including windows whose row has since been deleted.
	ListLifecycleStates(ctx context.Context) ([]MaintenanceLifecycleState, error)
	// SeedLifecycleState starts tracking a window. It reports false when the
	// window was already tracked (another replica seeded it first).
	SeedLifecycleState(ctx context.Context, st MaintenanceLifecycleState, at time.Time) (bool, error)
	// ClaimLifecycleTransition flips a tracked window from active=from to
	// active=!from. It reports false when the stored state was not from.
	ClaimLifecycleTransition(ctx context.Context, windowID string, from bool, at time.Time) (bool, error)
	// ClaimLifecycleDeletion stops tracking a window. It reports false when
	// another replica already did.
	ClaimLifecycleDeletion(ctx context.Context, windowID string) (bool, error)
}

// MaintenanceLifecycleState is the last lifecycle state emitted for a
// maintenance window.
type MaintenanceLifecycleState struct {
	WindowID  string
	OrgID     string
	MonitorID string
	Active    bool
}

// APIKeyStore manages API keys.
type APIKeyStore interface {
	Create(ctx context.Context, key *domain.APIKey) error
	GetByID(ctx context.Context, id string) (*domain.APIKey, error)
	GetByHash(ctx context.Context, hash string) (*domain.APIKey, error)
	ListByOrg(ctx context.Context, orgID string, params ListParams) ([]*domain.APIKey, error)
	// ListByCreator returns every key in orgID created by userID, newest
	// first. Keys belong to the org, so this is for review on offboarding.
	ListByCreator(ctx context.Context, orgID, userID string) ([]*domain.APIKey, error)
	Delete(ctx context.Context, id string) error
	// DeleteByCreator revokes every key in orgID created by userID and
	// returns how many were removed.
	DeleteByCreator(ctx context.Context, orgID, userID string) (int64, error)
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
	ApplyCheckout(ctx context.Context, b *domain.Billing) (bool, error)
	GetByOrgID(ctx context.Context, orgID string) (*domain.Billing, error)
	// ListWithSubscription returns every billing row that has a payment
	// subscription ID, for periodic reconcile jobs.
	ListWithSubscription(ctx context.Context) ([]*domain.Billing, error)
	UpdateStatus(ctx context.Context, orgID, status string) error
	UpdateSeatCount(ctx context.Context, orgID string, count int) error
	// UpdateEmail sets only the billing contact, so it cannot overwrite a
	// status or seat change a webhook wrote in the meantime.
	UpdateEmail(ctx context.Context, orgID, email string) error
	ResetUsagePeriod(ctx context.Context, orgID string) error
	Delete(ctx context.Context, orgID string) error
	// EnsureCustomer records customerID as the org's Stripe customer unless
	// one is already stored, creating a 'pending' billing row when the org
	// has none. It returns the customer ID that is stored afterwards, which
	// is the earlier one when a concurrent checkout got there first.
	EnsureCustomer(ctx context.Context, orgID, customerID, email string) (string, error)
	// GetCheckoutAttempt returns the org's latest checkout attempt, or nil
	// when it has none.
	GetCheckoutAttempt(ctx context.Context, orgID string) (*domain.CheckoutAttempt, error)
	// SaveCheckoutAttempt stores a, but only if the stored attempt number
	// is still prevAttempt (0 meaning no row). It reports whether it wrote.
	SaveCheckoutAttempt(ctx context.Context, a *domain.CheckoutAttempt, prevAttempt int) (bool, error)
	// ClearCheckoutAttempt drops the org's checkout attempt once checkout
	// has completed.
	ClearCheckoutAttempt(ctx context.Context, orgID string) error
}

// Metered usage kinds for UsageStore.
const (
	UsageSMS   = "sms"
	UsageVoice = "voice"
)

// UsageStore counts metered SMS and voice sends per org and usage period.
// It does not depend on a billing row, so every org is counted.
type UsageStore interface {
	// Reserve atomically counts one send of kind (UsageSMS or UsageVoice)
	// in period when the count is below limit. A negative limit means
	// unlimited. It returns the count after the call and whether the send
	// was counted; when it was not, used is the current count.
	Reserve(ctx context.Context, orgID, period, kind string, limit int) (used int, ok bool, err error)
	// Get returns the SMS and voice counts for period (zero when unset).
	Get(ctx context.Context, orgID, period string) (sms, voice int, err error)
	// ClaimWarning records that the warning for kind at threshold (a
	// percentage) was sent in period. It returns true only for the first
	// caller, so each warning goes out once per period.
	ClaimWarning(ctx context.Context, orgID, period, kind string, threshold int) (bool, error)
}

// PromoStore holds the promo lifecycle writes. They are conditional so a
// background worker and a request path cannot clobber a concurrent plan
// change such as a checkout webhook.
type PromoStore interface {
	// ListActive returns every org on the promo plan with an expiry set.
	ListActive(ctx context.Context) ([]*domain.Org, error)
	// DowngradeExpired moves the org to the free plan and clears its promo
	// fields, but only while it is still on the promo plan with the same
	// promo_expires_at the caller read. It returns false when the row no
	// longer matches (already downgraded, upgraded, or promo extended).
	DowngradeExpired(ctx context.Context, orgID, expiresAt string, at time.Time) (bool, error)
	// ClaimWarning records that the warning sent daysBefore days ahead of
	// the promo expiry expiresAt went out. It returns true only for the
	// first caller, so each warning is sent once across replicas. A new
	// expiry (promo extended) starts a fresh set of warnings.
	ClaimWarning(ctx context.Context, orgID, expiresAt string, daysBefore int, at time.Time) (bool, error)
	// ReleaseWarning removes a claimed marker so the warning is retried,
	// used when no email could be delivered.
	ReleaseWarning(ctx context.Context, orgID, expiresAt string, daysBefore int) error
}

// WebhookClaimResult is the outcome of WebhookEventStore.Claim.
type WebhookClaimResult int

const (
	// WebhookClaimed means the caller holds the processing lease and must
	// call Complete on success or Release on failure.
	WebhookClaimed WebhookClaimResult = iota
	// WebhookInProgress means another delivery holds an unexpired lease.
	WebhookInProgress
	// WebhookDone means the event was already processed successfully.
	WebhookDone
)

// WebhookEventStore records inbound webhook events so that an event is
// marked processed only after its handler succeeds. A delivery takes a
// short processing lease; on success the row is marked done, on failure
// the lease is released so the provider's retry is processed.
type WebhookEventStore interface {
	// Claim takes a processing lease on key for the lease duration. When
	// the result is WebhookClaimed, token identifies this lease for
	// Complete and Release. An expired lease or an expired done row is
	// taken over.
	Claim(ctx context.Context, key string, lease time.Duration) (result WebhookClaimResult, token string, err error)
	// Complete marks key done and keeps it for retain, so duplicates within
	// that window are skipped. It is a no-op if token no longer holds the
	// lease.
	Complete(ctx context.Context, key, token string, retain time.Duration) error
	// Release drops the processing lease so a retry can claim key. It is a
	// no-op if token no longer holds the lease.
	Release(ctx context.Context, key, token string) error
	// Cleanup removes done rows past their retention and stale leases.
	Cleanup(ctx context.Context) error
}

// DedupStore manages notification deduplication across instances.
type DedupStore interface {
	// Claim attempts to insert a dedup key. Returns true if this caller won
	// the claim (first insert). Returns false if the key already exists and
	// has not expired.
	Claim(ctx context.Context, key string, ttl time.Duration) (bool, error)
	Peek(ctx context.Context, key string) (bool, error)
	// Release deletes a claimed key so the next Claim for it succeeds. The
	// notification dispatcher releases a key when delivery did not happen,
	// so a redelivery can try again. Releasing a missing key is not an error.
	Release(ctx context.Context, key string) error
	// Cleanup removes expired entries.
	Cleanup(ctx context.Context) error
}

type RateBudgetStore interface {
	Take(ctx context.Context, key string, window time.Duration, now time.Time) (count int, resetAt time.Time, err error)
	Peek(ctx context.Context, key string, window time.Duration, now time.Time) (count int, resetAt time.Time, err error)
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
	// ConsumeBackupCode removes codeHash from the user's unused backup codes
	// in one conditional update. It reports false when the hash is not
	// present, including when a concurrent request already used it.
	ConsumeBackupCode(ctx context.Context, userID string, codeHash string) (bool, error)
	CreateWebAuthnCredential(ctx context.Context, cred *domain.WebAuthnCredential) error
	ListWebAuthnCredentials(ctx context.Context, userID string) ([]*domain.WebAuthnCredential, error)
	GetWebAuthnCredential(ctx context.Context, id string) (*domain.WebAuthnCredential, error)
	GetWebAuthnCredentialByUserHandle(ctx context.Context, userHandle []byte) (*domain.WebAuthnCredential, error)
	UpdateWebAuthnSignCount(ctx context.Context, id string, signCount uint32) error
	DeleteWebAuthnCredential(ctx context.Context, id string) error
	MFAGuard
}
