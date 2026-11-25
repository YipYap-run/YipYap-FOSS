package handlers

import (
	"bytes"
	"encoding/json"
	"errors"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
)

// monitorPatch is the PATCH /monitors/{id} body. Every field is a pointer
// (or raw JSON) so a field the request leaves out keeps its stored value,
// and a field sent as "" / 0 / false / [] / {} sets that value. Decoding
// into domain.Monitor could not tell the two apart, so a PATCH with only a
// name used to disable the monitor and drop its escalation policy (mon-9).
//
// id, org_id, heartbeat_token, muted and the timestamps are not accepted:
// they are server-owned or have their own endpoints.
type monitorPatch struct {
	Name               *string             `json:"name"`
	Type               *domain.MonitorType `json:"type"`
	Config             json.RawMessage     `json:"config"`
	IntervalSeconds    *int                `json:"interval_seconds"`
	TimeoutSeconds     *int                `json:"timeout_seconds"`
	LatencyWarningMS   *int                `json:"latency_warning_ms"`
	LatencyCriticalMS  *int                `json:"latency_critical_ms"`
	DownSeverity       *domain.Severity    `json:"down_severity"`
	DegradedSeverity   *domain.Severity    `json:"degraded_severity"`
	Regions            *[]string           `json:"regions"`
	EscalationPolicyID *string             `json:"escalation_policy_id"`
	IntegrationKey     *string             `json:"integration_key"`
	RunbookURL         *string             `json:"runbook_url"`
	ServiceID          *string             `json:"service_id"`
	GroupID            *string             `json:"group_id"`
	Description        *string             `json:"description"`
	AutoResolve        *bool               `json:"auto_resolve"`
	Enabled            *bool               `json:"enabled"`
	Labels             *map[string]string  `json:"labels"`
}

// hasConfig reports whether the request carries a config object. A JSON
// null is treated as absent.
func (p *monitorPatch) hasConfig() bool {
	c := bytes.TrimSpace(p.Config)
	return len(c) > 0 && !bytes.Equal(c, []byte("null"))
}

// validate checks the fields that do not need the store. The interval
// floor, target and references are checked by the handler.
func (p *monitorPatch) validate(m *domain.Monitor) error {
	if p.Type != nil && *p.Type != m.Type {
		return errors.New("type cannot be changed. Create a new monitor of the new type instead")
	}
	if p.Name != nil && *p.Name == "" {
		return errors.New("name cannot be empty")
	}
	if p.IntervalSeconds != nil && *p.IntervalSeconds <= 0 {
		return errors.New("interval_seconds must be greater than 0")
	}
	if p.TimeoutSeconds != nil && *p.TimeoutSeconds <= 0 {
		return errors.New("timeout_seconds must be greater than 0")
	}
	if p.LatencyWarningMS != nil && *p.LatencyWarningMS < 0 {
		return errors.New("latency_warning_ms cannot be negative")
	}
	if p.LatencyCriticalMS != nil && *p.LatencyCriticalMS < 0 {
		return errors.New("latency_critical_ms cannot be negative")
	}
	return nil
}

// changedRefs returns a monitor holding only the references this request
// changes, for validateMonitorRefs.
func (p *monitorPatch) changedRefs(m *domain.Monitor) domain.Monitor {
	var c domain.Monitor
	if p.EscalationPolicyID != nil && *p.EscalationPolicyID != m.EscalationPolicyID {
		c.EscalationPolicyID = *p.EscalationPolicyID
	}
	if p.GroupID != nil && *p.GroupID != m.GroupID {
		c.GroupID = *p.GroupID
	}
	if p.ServiceID != nil && *p.ServiceID != m.ServiceID {
		c.ServiceID = *p.ServiceID
	}
	return c
}

// apply copies the provided fields onto m. Labels are stored separately
// by the handler.
func (p *monitorPatch) apply(m *domain.Monitor) {
	setIf(&m.Name, p.Name)
	if p.hasConfig() {
		m.Config = p.Config
	}
	setIf(&m.IntervalSeconds, p.IntervalSeconds)
	setIf(&m.TimeoutSeconds, p.TimeoutSeconds)
	setIf(&m.LatencyWarningMS, p.LatencyWarningMS)
	setIf(&m.LatencyCriticalMS, p.LatencyCriticalMS)
	setIf(&m.DownSeverity, p.DownSeverity)
	setIf(&m.DegradedSeverity, p.DegradedSeverity)
	setIf(&m.Regions, p.Regions)
	setIf(&m.EscalationPolicyID, p.EscalationPolicyID)
	setIf(&m.IntegrationKey, p.IntegrationKey)
	setIf(&m.RunbookURL, p.RunbookURL)
	setIf(&m.ServiceID, p.ServiceID)
	setIf(&m.GroupID, p.GroupID)
	setIf(&m.Description, p.Description)
	setIf(&m.AutoResolve, p.AutoResolve)
	setIf(&m.Enabled, p.Enabled)
}

func setIf[T any](dst *T, v *T) {
	if v != nil {
		*dst = *v
	}
}
