package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// ErrAlreadyComplete is returned by AlertRemediationStore.Complete when the
// target row's status is no longer "in_progress", i.e. a concurrent caller
// already won the CAS. Callers (the remediation_result reply handler in
// particular) MUST surface this as a rejected outcome and NOT double-resolve
// the parent alert. (A3-NEW-M-2: closes a concurrent-reply race.)
var ErrAlreadyComplete = errors.New("alert_remediations: row already completed")

// ErrReplyAuditDuplicate is returned by CloudEventReplyAudit.Write when the
// (org_id, alert_id, reply_id) UNIQUE constraint already has a row. Callers
// should treat this as the idempotent no-op it is, the reply has already
// been processed and the prior audit row is the canonical record. The
// dispatcher uses this to short-circuit retried sink replies.
var ErrReplyAuditDuplicate = errors.New("cloudevent_reply_audit: duplicate reply_id for (org, alert)")

// Phase-5 state-store interfaces supporting the mutating reply handlers.
// Each reply type that creates side-state has its own minimal CRUD surface.

// AlertSuppression is a row in alert_suppressions. At least one of AlertID
// or MonitorID will be non-empty; MatchFilter refines which alerts in the
// org get suppressed for the row's duration.
type AlertSuppression struct {
	ID            string
	OrgID         string
	AlertID       string
	MonitorID     string
	MatchFilter   json.RawMessage
	ExpiresAt     time.Time
	Reason        string
	SourceReplyID string
	CreatedAt     time.Time
}

// AlertSuppressionStore is the CRUD surface for reply.suppressed rows.
type AlertSuppressionStore interface {
	Create(ctx context.Context, s *AlertSuppression) error
	ListActiveForAlert(ctx context.Context, alertID string, at time.Time) ([]*AlertSuppression, error)
	ListActiveForOrg(ctx context.Context, orgID string, at time.Time) ([]*AlertSuppression, error)
	DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// RemediationStatus captures the lifecycle of an in-flight remediation.
type RemediationStatus string

const (
	RemediationInProgress RemediationStatus = "in_progress"
	RemediationSuccess    RemediationStatus = "success"
	RemediationFailure    RemediationStatus = "failure"
	RemediationPartial    RemediationStatus = "partial"
)

// AlertRemediation is a row in alert_remediations.
//
// OrgID is denormalised from the parent alert. The store fills it in on
// Create from the alert row when the caller leaves it blank, but a caller
// that already knows the org SHOULD set it explicitly so per-tenant queries
// don't need a JOIN.
type AlertRemediation struct {
	RemediationID string
	AlertID       string
	OrgID         string
	Status        RemediationStatus
	StartedAt     time.Time
	CompletedAt   *time.Time
	ExpiresAt     time.Time
	Description   string
	Message       string
	ArtifactURL   string
	SourceReplyID string
}

// AlertRemediationStore is the CRUD surface for reply.remediation_* rows.
type AlertRemediationStore interface {
	Create(ctx context.Context, r *AlertRemediation) error
	GetByID(ctx context.Context, remediationID string) (*AlertRemediation, error)
	Complete(ctx context.Context, remediationID string, status RemediationStatus, message, artifactURL string) error
	ListActiveForAlert(ctx context.Context, alertID string, at time.Time) ([]*AlertRemediation, error)
	ExpireStale(ctx context.Context, cutoff time.Time) (int64, error)
}

// AlertRoutingRule is a row in alert_routing_rules.
//
// Priority orders rules deterministically when multiple match a single
// alert. Higher Priority wins; ties break on (CreatedAt DESC, ID).
// Phase-6 consumers MUST sort by priority before applying rules.
type AlertRoutingRule struct {
	ID              string
	OrgID           string
	TargetChannelID string
	MatchFilter     json.RawMessage
	ExpiresAt       time.Time
	Priority        int
	Reason          string
	SourceReplyID   string
	CreatedAt       time.Time
}

// AlertRoutingRuleStore is the CRUD surface for reply.route rows.
type AlertRoutingRuleStore interface {
	Create(ctx context.Context, r *AlertRoutingRule) error
	ListActiveForOrg(ctx context.Context, orgID string, at time.Time) ([]*AlertRoutingRule, error)
	DeleteExpiredBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// AlertStatusPageRef is a row in alert_status_page_refs.
type AlertStatusPageRef struct {
	ID            string
	AlertID       string
	Provider      string
	URL           string
	PublicMessage string
	UpdatedAt     time.Time
	SourceReplyID string
}

// AlertStatusPageRefStore is the CRUD surface for reply.status_page rows.
type AlertStatusPageRefStore interface {
	Create(ctx context.Context, s *AlertStatusPageRef) error
	ListByAlert(ctx context.Context, alertID string) ([]*AlertStatusPageRef, error)
}
