package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// OrgSettingAllowResolveWhileDown is the org_settings key that lets users
// resolve an alert while its monitor's latest check is still failing. Off by
// default: the checker only opens a new alert when a monitor goes from up to
// down, so resolving mid-outage means nobody is paged again for that outage.
const OrgSettingAllowResolveWhileDown = "allow_resolve_while_down"

// Conflict codes returned in the "code" field of a 409 from Ack or Resolve.
// The SPA keys its inline message off these.
const (
	alertConflictResolved     = "alert_resolved"
	alertConflictAcknowledged = "alert_acknowledged"
	alertConflictMonitorDown  = "monitor_still_down"
)

// alertConflict is the 409 body for an ack or resolve that the alert's
// current state does not allow. Error is a sentence the UI can show as is.
type alertConflict struct {
	Error         string             `json:"error"`
	Code          string             `json:"code"`
	AlertID       string             `json:"alert_id"`
	MonitorID     string             `json:"monitor_id"`
	Status        domain.AlertStatus `json:"status"`
	ActorID       string             `json:"actor_id,omitempty"`
	ActorName     string             `json:"actor_name,omitempty"`
	MonitorStatus domain.CheckStatus `json:"monitor_status,omitempty"`
}

// writeStateConflict answers a 409 that explains who already moved the
// alert. alert must be the current stored row.
func (h *AlertHandler) writeStateConflict(ctx context.Context, w http.ResponseWriter, alert *domain.Alert) {
	body := alertConflict{
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		Status:    alert.Status,
	}
	switch alert.Status {
	case domain.AlertResolved:
		body.Code = alertConflictResolved
		var found bool
		body.ActorID, found = h.resolvedBy(ctx, alert.ID)
		body.ActorName = h.userName(ctx, alert.OrgID, body.ActorID)
		switch {
		case body.ActorName != "":
			body.Error = "This alert was already resolved by " + body.ActorName + "."
		case found && body.ActorID == "":
			body.Error = "This alert was already resolved automatically when the monitor recovered."
		default:
			body.Error = "This alert was already resolved."
		}
	case domain.AlertAcknowledged:
		body.Code = alertConflictAcknowledged
		body.ActorID = alert.AcknowledgedBy
		body.ActorName = h.userName(ctx, alert.OrgID, body.ActorID)
		if body.ActorName != "" {
			body.Error = "This alert was already acknowledged by " + body.ActorName + "."
		} else {
			body.Error = "This alert was already acknowledged."
		}
	default:
		body.Code = "alert_state_changed"
		body.Error = "This alert changed while you were viewing it. Refresh to see its current state."
	}
	jsonResponse(w, http.StatusConflict, body)
}

// failingCheck returns the monitor's latest check when it is down or
// degraded, or nil when the monitor is up or has never been checked.
func (h *AlertHandler) failingCheck(ctx context.Context, monitorID string) (*domain.MonitorCheck, error) {
	c, err := h.store.Checks().GetLatest(ctx, monitorID)
	if errors.Is(err, store.ErrNoChecks) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if c.Status == domain.StatusDown || c.Status == domain.StatusDegraded {
		return c, nil
	}
	return nil, nil
}

// allowResolveWhileDown reports whether the org opted in to resolving alerts
// whose monitor is still failing. A missing setting or a store without org
// settings means no.
func (h *AlertHandler) allowResolveWhileDown(ctx context.Context, orgID string) bool {
	sp, ok := h.store.(store.OrgSettingsProvider)
	if !ok {
		return false
	}
	v, err := sp.OrgSettings().Get(ctx, orgID, OrgSettingAllowResolveWhileDown)
	return err == nil && v == "true"
}

// writeMonitorDownConflict answers the 409 for a resolve blocked because
// the monitor is still failing.
func writeMonitorDownConflict(w http.ResponseWriter, alert *domain.Alert, check *domain.MonitorCheck) {
	state := "down"
	if check.Status == domain.StatusDegraded {
		state = "degraded"
	}
	// An acknowledged alert has already stopped paging, so it is not told
	// to acknowledge again.
	next := "Acknowledge it to stop paging, or mute the monitor."
	if alert.Status == domain.AlertAcknowledged {
		next = "Paging already stopped when it was acknowledged. Mute the monitor if you expect it to stay down."
	}
	jsonResponse(w, http.StatusConflict, alertConflict{
		Error: "The monitor is still " + state + ", so this alert cannot be resolved yet. " +
			next + " The alert resolves on its own when the monitor recovers.",
		Code:          alertConflictMonitorDown,
		AlertID:       alert.ID,
		MonitorID:     alert.MonitorID,
		Status:        alert.Status,
		MonitorStatus: check.Status,
	})
}

// resolvedBy returns the user on the alert's latest resolved event. found is
// false when there is no such event. A found event with no user means the
// engine resolved the alert because the monitor recovered.
func (h *AlertHandler) resolvedBy(ctx context.Context, alertID string) (userID string, found bool) {
	events, err := h.store.Alerts().ListEvents(ctx, alertID)
	if err != nil {
		return "", false
	}
	for _, e := range events {
		if e.EventType == domain.EventResolved {
			userID, found = e.TargetUserID, true
		}
	}
	return userID, found
}

// userName returns a display name for a user in orgID, or "" when the user
// is unknown or belongs to another org.
func (h *AlertHandler) userName(ctx context.Context, orgID, userID string) string {
	if userID == "" {
		return ""
	}
	u, err := h.store.Users().GetByID(ctx, userID)
	if err != nil || u == nil || u.OrgID != orgID {
		return ""
	}
	if u.Name != "" {
		return u.Name
	}
	return u.Email
}
