package handlers

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/limits"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// planError marks a rejection that comes from the org's plan (interval
// floor, resource limits, paid-only channel types), or in FOSS from a cap
// the operator configured. Handlers answer it with 403 so the SPA can tell
// it apart from a malformed request. operatorCap is set for FOSS caps:
// there is no plan to upgrade, so no upgrade prompt is offered.
type planError struct {
	msg         string
	operatorCap bool
}

func (e *planError) Error() string { return e.msg }

func newPlanError(format string, args ...any) error {
	return &planError{msg: fmt.Sprintf(format, args...)}
}

func isPlanError(err error) bool {
	var pe *planError
	if errors.As(err, &pe) {
		return true
	}
	var ce *limits.CapError
	return errors.As(err, &ce)
}

// upgradeApplies reports whether err is a plan rejection that a plan
// upgrade would lift. Operator caps in FOSS are not.
func upgradeApplies(err error) bool {
	var pe *planError
	return errors.As(err, &pe) && !pe.operatorCap
}

// validMonitorTypes lists the monitor types the checker can run.
var validMonitorTypes = map[domain.MonitorType]bool{
	domain.MonitorHTTP:      true,
	domain.MonitorTCP:       true,
	domain.MonitorPing:      true,
	domain.MonitorDNS:       true,
	domain.MonitorHeartbeat: true,
}

// validateMonitorRefs checks that every ID a monitor points at belongs to
// the caller's org. Used on create, import and update.
func validateMonitorRefs(ctx context.Context, s store.Store, orgID string, m *domain.Monitor) error {
	if m.EscalationPolicyID != "" {
		ep, err := s.EscalationPolicies().GetByID(ctx, m.EscalationPolicyID)
		if err != nil || ep.OrgID != orgID {
			return fmt.Errorf("escalation_policy_id does not reference an escalation policy in this org")
		}
	}
	if m.GroupID != "" {
		if mgp, ok := s.(store.MonitorGroupProvider); ok {
			grp, err := mgp.MonitorGroups().GetByID(ctx, m.GroupID)
			if err != nil || grp.OrgID != orgID {
				return fmt.Errorf("group_id does not reference a monitor group in this org")
			}
		}
	}
	// No-op in FOSS where the service catalog does not exist.
	if m.ServiceID != "" && !serviceExistsInOrg(ctx, s, orgID, m.ServiceID) {
		return fmt.Errorf("service_id does not reference a service in this org")
	}
	return nil
}

// prepareNewMonitor runs every per-monitor check that single create and
// bulk import share, then fills the server-owned fields (ID, org, times,
// heartbeat token, mute default). It does not check the plan's monitor
// count: callers do that once for the whole request with
// checkMonitorLimitN.
func prepareNewMonitor(ctx context.Context, s store.Store, org *domain.Org, m *domain.Monitor, now time.Time) error {
	if !validMonitorTypes[m.Type] {
		return fmt.Errorf("invalid type %q", m.Type)
	}

	// Store an empty object rather than an empty column, so the monitor
	// always reads back as valid JSON (heartbeat monitors need no config).
	if len(bytes.TrimSpace(m.Config)) == 0 || string(bytes.TrimSpace(m.Config)) == "null" {
		m.Config = json.RawMessage("{}")
	}

	// SSRF protection: reject monitors targeting private/internal IPs.
	if m.Config != nil {
		if err := validateMonitorTarget(m.Type, m.Config); err != nil {
			return err
		}
	}

	// Reject dangerous URI schemes in runbook_url.
	if err := validateRunbookURL(m.RunbookURL); err != nil {
		return err
	}

	if err := validateMonitorRefs(ctx, s, org.ID, m); err != nil {
		return err
	}

	if m.IntervalSeconds == 0 {
		m.IntervalSeconds = defaultCheckIntervalSeconds(org)
	}
	if err := checkIntervalFloor(org, m.IntervalSeconds); err != nil {
		return err
	}

	m.ID = uuid.New().String()
	m.OrgID = org.ID
	m.CreatedAt = now
	m.UpdatedAt = now

	// The token is the only thing that authenticates a heartbeat ping, so a
	// heartbeat monitor never gets stored without one. Any client-supplied
	// value is replaced.
	m.HeartbeatToken = ""
	if m.Type == domain.MonitorHeartbeat {
		tok, err := newHeartbeatToken()
		if err != nil {
			return err
		}
		m.HeartbeatToken = tok
	}

	// Honor the org's mute_new_monitors setting.
	if sp, ok := s.(store.OrgSettingsProvider); ok {
		if val, err := sp.OrgSettings().Get(ctx, org.ID, "mute_new_monitors"); err == nil && val == "true" {
			m.Muted = true
		}
	}
	return nil
}

func newHeartbeatToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate heartbeat token")
	}
	return hex.EncodeToString(b), nil
}

// writeMonitorPrepareError maps a prepareNewMonitor error to a response.
func writeMonitorPrepareError(w http.ResponseWriter, err error) {
	var te *monitorTargetError
	switch {
	case errors.As(err, &te):
		writeMonitorTargetError(w, err)
	case isPlanError(err):
		errorResponse(w, http.StatusForbidden, err.Error())
	default:
		errorResponse(w, http.StatusBadRequest, err.Error())
	}
}
