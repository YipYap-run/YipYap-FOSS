package escalation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

func NewResolveFanout(s store.Store, b bus.Bus, cipher contactmethod.Cipher) func(ctx context.Context, alert *domain.Alert) {
	return func(ctx context.Context, alert *domain.Alert) {
		SendResolves(ctx, s, b, cipher, alert, time.Now().UTC())
	}
}

func SendResolves(ctx context.Context, s store.Store, b bus.Bus, cipher contactmethod.Cipher, alert *domain.Alert, now time.Time) {
	if alert == nil || s == nil {
		return
	}
	targets, err := s.Alerts().ListPagedTargets(ctx, alert.ID)
	if err != nil {
		slog.Error("escalation: list paged targets for resolve", "alert_id", alert.ID, "error", err)
		return
	}

	monitorName := ""
	if monitor, err := s.Monitors().GetByID(ctx, alert.MonitorID); err == nil && monitor != nil {
		monitorName = monitor.Name
	}

	for _, t := range targets {
		job, skip := resolveJob(ctx, s, alert, monitorName, t, cipher, now)
		if job == nil {
			recordResolveOutcome(ctx, s, alert.ID, t, skip, now)
			deletePagedTarget(ctx, s, alert.ID, t.TargetKey)
			continue
		}
		if !publishJob(ctx, s, b, job) {
			recordResolveOutcome(ctx, s, alert.ID, t, "could not queue the resolve notification", now)
			continue
		}
		deletePagedTarget(ctx, s, alert.ID, t.TargetKey)
	}
}

func deletePagedTarget(ctx context.Context, s store.Store, alertID, targetKey string) {
	if err := s.Alerts().DeletePagedTarget(ctx, alertID, targetKey); err != nil {
		slog.Warn("escalation: clear paged target after resolve", "alert_id", alertID, "target_key", targetKey, "error", err)
	}
}

func resolveJob(ctx context.Context, s store.Store, alert *domain.Alert, monitorName string, t *domain.AlertPagedTarget, cipher contactmethod.Cipher, now time.Time) (*domain.NotificationJob, string) {
	job := &domain.NotificationJob{
		ID:          uuid.New().String(),
		AlertID:     alert.ID,
		OrgID:       alert.OrgID,
		MonitorName: monitorName,
		Severity:    string(alert.Severity),
		Message:     resolveMessage(monitorName),
		DedupeKey:   "resolve-" + alert.ID + "-" + t.TargetKey,
		EventAction: domain.EventActionResolve,
	}
	switch t.Kind {
	case domain.PagedTargetMethod:
		cp, ok := s.(store.ContactMethodProvider)
		if !ok {
			return nil, "personal contact methods are not available on this server"
		}
		m, err := cp.ContactMethods().Get(ctx, t.UserID, t.MethodID)
		if err != nil || m == nil {
			return nil, "contact method no longer exists"
		}
		if !m.Verified() {
			return nil, "contact method is no longer verified"
		}
		if m.OrgID != alert.OrgID {
			return nil, "contact method belongs to another organization"
		}
		if !contactmethod.TypeSupported(m.Type) {
			return nil, fmt.Sprintf("%s contact methods are not available on this server", m.Type)
		}
		if err := contactmethod.PlanError(ctx, s, alert.OrgID, m.Type); err != nil {
			return nil, err.Error()
		}
		if err := contactmethod.FillJob(ctx, s, m, job, cipher); err != nil {
			return nil, "could not prepare the resolve notification"
		}
		return job, ""

	case domain.PagedTargetChannel:
		ch, err := s.NotificationChannels().GetByID(ctx, t.ChannelID)
		if err != nil || ch == nil {
			return nil, "notification channel no longer exists"
		}
		if ch.OrgID != alert.OrgID {
			return nil, "notification channel belongs to another organization"
		}
		if !ch.Enabled {
			return nil, fmt.Sprintf("notification channel %q is disabled", ch.Name)
		}
		cfg, err := contactmethod.EncodeTargetConfig([]byte(ch.Config), cipher.Enc)
		if err != nil {
			return nil, "could not prepare the resolve notification"
		}
		job.Channel = ch.Type
		job.TargetConfig = cfg
		return job, ""
	}
	return nil, "unknown paged target kind"
}

func resolveMessage(monitorName string) string {
	if monitorName == "" {
		return "Resolved"
	}
	return "Resolved: " + monitorName
}

func recordResolveOutcome(ctx context.Context, s store.Store, alertID string, t *domain.AlertPagedTarget, reason string, now time.Time) {
	detail := map[string]any{
		"reason":      reason,
		"phase":       "resolve",
		"target_key":  t.TargetKey,
		"target_kind": string(t.Kind),
	}
	if t.UserID != "" {
		detail["user_id"] = t.UserID
	}
	if t.MethodID != "" {
		detail["contact_method_id"] = t.MethodID
	}
	if t.ChannelID != "" {
		detail["channel_id"] = t.ChannelID
	}
	raw, _ := json.Marshal(detail)
	err := s.Alerts().CreateEvent(ctx, &domain.AlertEvent{
		ID:        uuid.New().String(),
		AlertID:   alertID,
		EventType: domain.EventNotifyFailed,
		Detail:    raw,
		CreatedAt: now,
	})
	if err != nil {
		slog.Warn("escalation: record resolve outcome", "alert_id", alertID, "error", err)
	}
}
