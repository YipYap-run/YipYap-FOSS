package telemetry

import (
	"context"
	"time"
)

// CheckSample is one monitor check result for an org's own metric export.
type CheckSample struct {
	MonitorID   string
	MonitorName string
	MonitorType string
	// Status is "up", "down" or "degraded".
	Status    string
	LatencyMS int
	// Interval is the monitor's check interval. It bounds how long the
	// status gauge keeps reporting this value without a newer check.
	Interval time.Duration
}

// OrgRecorder records an org's own metrics for export to the OTLP endpoint
// that org configured. Each call names one org and carries only that org's
// data. Implementations drop calls for orgs with no export configured, so
// callers do not check first. A nil OrgRecorder means export is off (FOSS
// builds, tests).
type OrgRecorder interface {
	// Exporting reports whether orgID has an exporter on this instance.
	// Callers use it to skip extra lookups when nothing would be recorded.
	Exporting(orgID string) bool
	RecordCheck(ctx context.Context, orgID string, s CheckSample)
	// RecordNotification counts one notification outcome. sent is false
	// for a notification that failed after every retry.
	RecordNotification(ctx context.Context, orgID, channelType string, sent bool)
	RecordEscalationStep(ctx context.Context, orgID string, step int)
}
