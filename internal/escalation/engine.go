package escalation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
)

// EngineMetrics records escalation metrics. Pass nil to disable.
type EngineMetrics interface {
	AddActiveAlert(ctx context.Context, orgID string, delta int64)
	RecordEscalationStep(ctx context.Context, orgID string, position int64)
}

// AlertTriggerEvent is the payload from the checker on alert.trigger / alert.recover.
type AlertTriggerEvent struct {
	MonitorID   string             `json:"monitor_id"`
	MonitorName string             `json:"monitor_name"`
	Status      domain.CheckStatus `json:"status"`
	LatencyMS   int                `json:"latency_ms"`
	StatusCode  int                `json:"status_code,omitempty"`
	Error       string             `json:"error,omitempty"`
	CheckedAt   time.Time          `json:"checked_at"`
}

// Engine is the core escalation state machine. It listens for alert events
// on the bus and manages escalation progression via a ticker goroutine.
type Engine struct {
	store    store.Store
	bus      bus.Bus
	envelope *crypto.Envelope
	oncall   *OnCallResolver
	metrics  EngineMetrics
	ctx      context.Context
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewEngine creates a new escalation engine.
func NewEngine(s store.Store, b bus.Bus, envelope *crypto.Envelope, metrics EngineMetrics) *Engine {
	return &Engine{
		store:    s,
		bus:      b,
		envelope: envelope,
		oncall:   NewOnCallResolver(s),
		metrics:  metrics,
	}
}

// Start subscribes to bus subjects and launches the ticker goroutine.
// All subscriptions use a queue group so that in a multi-instance deployment
// only one escalation engine processes each event.
func (e *Engine) Start(ctx context.Context) {
	e.ctx, e.cancel = context.WithCancel(ctx)
	e.done = make(chan struct{})

	_ = e.bus.PullSubscribe("alert.trigger", "escalation-trigger", e.wrapHandler(e.handleTrigger))
	_ = e.bus.PullSubscribe("alert.recover", "escalation-recover", e.wrapHandler(e.handleRecover))
	_ = e.bus.PullSubscribe("alert.ack", "escalation-ack", e.wrapHandler(e.handleAck))

	go e.tickLoop()
}

// Stop cancels the engine context and waits for the ticker to finish.
func (e *Engine) Stop() {
	if e.cancel != nil {
		e.cancel()
	}
	if e.done != nil {
		<-e.done
	}
}

// wrapHandler adapts a plain Handler to an AckHandler for PullSubscribe.
// On success the message is acked; on error the fetchLoop auto-naks.
func (e *Engine) wrapHandler(fn func(ctx context.Context, subject string, data []byte) error) bus.AckHandler {
	return func(ctx context.Context, msg *bus.Msg) error {
		if err := fn(ctx, msg.Subject, msg.Data); err != nil {
			return err // fetchLoop will auto-nak
		}
		return msg.Ack()
	}
}

func (e *Engine) handleTrigger(ctx context.Context, subject string, data []byte) error {
	var evt AlertTriggerEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		slog.Error("escalation: unmarshal trigger event", "error", err)
		return err
	}

	// Look up the monitor to get org_id and escalation_policy_id.
	monitor, err := e.store.Monitors().GetByID(ctx, evt.MonitorID)
	if err != nil {
		slog.Error("escalation: get monitor", "monitor_id", evt.MonitorID, "error", err)
		return err
	}

	// Check if there's already an active alert for this monitor.
	existing, _ := e.store.Alerts().GetActiveByMonitor(ctx, evt.MonitorID)
	if existing != nil {
		return nil // already firing, skip
	}

	now := time.Now().UTC()
	alertID := uuid.New().String()

	// Determine severity from monitor config based on check status.
	severity := monitor.DownSeverity
	if severity == "" {
		severity = domain.SeverityCritical
	}
	if evt.Status == domain.StatusDegraded {
		severity = monitor.DegradedSeverity
		if severity == "" {
			severity = domain.SeverityWarning
		}
	}

	alert := &domain.Alert{
		ID:        alertID,
		MonitorID: evt.MonitorID,
		OrgID:     monitor.OrgID,
		Status:    domain.AlertFiring,
		Severity:  severity,
		Error:     evt.Error,
		StartedAt: now,
	}

	if err := e.store.Alerts().Create(ctx, alert); err != nil {
		// Unique constraint on active alerts prevents duplicates at the DB level.
		// If another instance already created one, this is expected  - not an error.
		slog.Debug("escalation: alert already exists for monitor", "monitor_id", evt.MonitorID)
		return nil
	}

	if e.metrics != nil {
		e.metrics.AddActiveAlert(ctx, alert.OrgID, 1)
	}

	// Record triggered event.
	e.createEvent(ctx, alertID, domain.EventTriggered, "", "", nil)

	// If the monitor is muted, the alert is recorded but notifications are suppressed.
	if monitor.Muted {
		slog.Info("monitor muted, skipping notification", "monitor_id", monitor.ID)
		return nil
	}

	// If the monitor has an escalation policy, start escalation.
	if monitor.EscalationPolicyID == "" {
		return nil
	}

	steps, err := e.store.EscalationPolicies().GetSteps(ctx, monitor.EscalationPolicyID)
	if err != nil || len(steps) == 0 {
		return nil
	}

	firstStep := steps[0]
	alert.CurrentEscalationStep = firstStep.ID
	_ = e.store.Alerts().Update(ctx, alert)

	state := &domain.AlertEscalationState{
		AlertID:       alertID,
		CurrentStepID: firstStep.ID,
		StepEnteredAt: now,
		RetryCount:    0,
	}
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: upsert state", "error", err)
		return err
	}

	// Immediately process step 1  - send notifications.
	e.notifyStep(ctx, alert, monitor, &firstStep, state)

	return nil
}

func (e *Engine) handleRecover(ctx context.Context, subject string, data []byte) error {
	var evt AlertTriggerEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}

	alert, err := e.store.Alerts().GetActiveByMonitor(ctx, evt.MonitorID)
	if err != nil {
		return nil // no active alert to resolve
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now

	if err := e.store.Alerts().Update(ctx, alert); err != nil {
		slog.Error("escalation: resolve alert", "error", err)
		return err
	}

	if e.metrics != nil {
		e.metrics.AddActiveAlert(ctx, alert.OrgID, -1)
	}

	// Clean up escalation state.
	_ = e.store.Alerts().DeleteEscalationState(ctx, alert.ID)

	e.createEvent(ctx, alert.ID, domain.EventResolved, "", "", nil)

	// Auto-resolve: if the monitor has auto_resolve enabled, resolve any
	// remaining active (firing or acknowledged) alerts for this monitor.
	monitor, err := e.store.Monitors().GetByID(ctx, evt.MonitorID)
	if err == nil && monitor.AutoResolve {
		e.autoResolveAlerts(ctx, monitor)
	}

	// Check if the incident linked to this alert should be auto-resolved.
	e.maybeAutoResolveIncident(ctx, alert)

	return nil
}

// autoResolveAlerts resolves all active alerts (firing or acknowledged) for
// the given monitor. Called when the monitor recovers and auto_resolve is on.
func (e *Engine) autoResolveAlerts(ctx context.Context, monitor *domain.Monitor) {
	// Keep resolving until no more active alerts remain.
	for {
		active, err := e.store.Alerts().GetActiveByMonitor(ctx, monitor.ID)
		if err != nil || active == nil {
			return
		}
		now := time.Now().UTC()
		active.Status = domain.AlertResolved
		active.ResolvedAt = &now
		if err := e.store.Alerts().Update(ctx, active); err != nil {
			slog.Error("escalation: auto-resolve alert", "alert_id", active.ID, "error", err)
			return
		}
		if e.metrics != nil {
			e.metrics.AddActiveAlert(ctx, active.OrgID, -1)
		}
		_ = e.store.Alerts().DeleteEscalationState(ctx, active.ID)
		e.createEvent(ctx, active.ID, domain.EventResolved, "", "", nil)
	}
}

func (e *Engine) handleAck(ctx context.Context, subject string, data []byte) error {
	var evt domain.AckEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return err
	}

	alert, err := e.store.Alerts().GetByID(ctx, evt.AlertID)
	if err != nil {
		return err
	}

	if alert.Status == domain.AlertResolved {
		return nil
	}

	now := time.Now().UTC()
	alert.Status = domain.AlertAcknowledged
	alert.AcknowledgedAt = &now
	alert.AcknowledgedBy = evt.UserID

	if err := e.store.Alerts().Update(ctx, alert); err != nil {
		slog.Error("escalation: ack alert", "error", err)
		return err
	}

	e.createEvent(ctx, alert.ID, domain.EventAck, evt.Channel, evt.UserID, nil)
	return nil
}

func (e *Engine) tickLoop() {
	defer close(e.done)

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-e.ctx.Done():
			return
		case <-ticker.C:
			e.processFiringAlerts()
		}
	}
}

func (e *Engine) processFiringAlerts() {
	ctx := e.ctx

	alerts, err := e.store.Alerts().ListFiring(ctx)
	if err != nil {
		slog.Error("escalation: list firing", "error", err)
		return
	}

	now := time.Now().UTC()

	for _, alert := range alerts {
		state, err := e.store.Alerts().GetEscalationState(ctx, alert.ID)
		if err != nil {
			continue // no escalation state means no policy
		}

		// Get the current step.
		monitor, err := e.store.Monitors().GetByID(ctx, alert.MonitorID)
		if err != nil {
			continue
		}

		if monitor.EscalationPolicyID == "" {
			continue
		}

		steps, err := e.store.EscalationPolicies().GetSteps(ctx, monitor.EscalationPolicyID)
		if err != nil || len(steps) == 0 {
			continue
		}

		// Find the current step.
		var currentStep *domain.EscalationStep
		for i := range steps {
			if steps[i].ID == state.CurrentStepID {
				currentStep = &steps[i]
				break
			}
		}
		if currentStep == nil {
			continue
		}

		// Check if wait_seconds has elapsed since step_entered_at.
		elapsed := now.Sub(state.StepEnteredAt)
		waitDuration := time.Duration(currentStep.WaitSeconds) * time.Second

		if elapsed < waitDuration {
			continue
		}

		// Time has elapsed. Check repeat logic.
		if state.RetryCount < currentStep.RepeatCount {
			// Retry: increment retry_count and re-notify.
			state.RetryCount++
			state.StepEnteredAt = now
			_ = e.store.Alerts().UpsertEscalationState(ctx, state)
			e.notifyStep(ctx, alert, monitor, currentStep, state)
			continue
		}

		// Retries exhausted. Try to advance to next step.
		nextStep, err := e.store.EscalationPolicies().GetNextStep(ctx, monitor.EscalationPolicyID, currentStep.Position)
		if err != nil {
			continue
		}

		if nextStep != nil {
			// Advance to next step.
			state.CurrentStepID = nextStep.ID
			state.StepEnteredAt = now
			state.RetryCount = 0
			if e.metrics != nil {
				e.metrics.RecordEscalationStep(ctx, alert.OrgID, int64(nextStep.Position))
			}
			_ = e.store.Alerts().UpsertEscalationState(ctx, state)

			alert.CurrentEscalationStep = nextStep.ID
			_ = e.store.Alerts().Update(ctx, alert)

			e.createEvent(ctx, alert.ID, domain.EventEscalated, "", "", nil)
			e.notifyStep(ctx, alert, monitor, nextStep, state)
			continue
		}

		// No next step. Check loop policy.
		policy, err := e.store.EscalationPolicies().GetByID(ctx, monitor.EscalationPolicyID)
		if err != nil {
			continue
		}

		if policy.Loop {
			// Check max_loops. We track loop count by counting how many times
			// we've cycled. Use notifications_sent to track loop count.
			loopCount := e.getLoopCount(state)
			if policy.MaxLoops != nil && loopCount >= *policy.MaxLoops {
				continue // exhausted loops
			}

			// Restart from step 1.
			firstStep := steps[0]
			state.CurrentStepID = firstStep.ID
			state.StepEnteredAt = now
			state.RetryCount = 0
			e.setLoopCount(state, loopCount+1)
			_ = e.store.Alerts().UpsertEscalationState(ctx, state)

			alert.CurrentEscalationStep = firstStep.ID
			_ = e.store.Alerts().Update(ctx, alert)

			e.createEvent(ctx, alert.ID, domain.EventEscalated, "", "", nil)
			e.notifyStep(ctx, alert, monitor, &firstStep, state)
		}
		// else: terminal  - leave alert in firing state, no more escalation.
	}
}

func (e *Engine) notifyStep(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, state *domain.AlertEscalationState) {
	// Check maintenance window suppression.
	mws, err := e.store.MaintenanceWindows().ListActiveByMonitor(ctx, monitor.ID, time.Now().UTC())
	if err == nil {
		for _, mw := range mws {
			if mw.SuppressAlerts {
				return // suppressed
			}
		}
	}

	// Get targets for this step.
	targets, err := e.store.EscalationPolicies().GetTargets(ctx, step.ID)
	if err != nil {
		slog.Error("escalation: get targets", "step_id", step.ID, "error", err)
		return
	}

	now := time.Now().UTC()
	state.LastNotifiedAt = &now
	_ = e.store.Alerts().UpsertEscalationState(ctx, state)

	for _, target := range targets {
		userIDs, err := e.resolveTarget(ctx, &target, monitor)
		if err != nil {
			slog.Error("escalation: resolve target", "target", target.TargetType, "error", err)
			continue
		}

		// Look up the notification channel config from DB.
		ch, err := e.store.NotificationChannels().GetByID(ctx, target.ChannelID)
		if err != nil {
			slog.Error("escalation: get notification channel", "channel_id", target.ChannelID, "error", err)
			continue
		}
		if ch.OrgID != monitor.OrgID {
			slog.Error("escalation: channel org mismatch", "channel", ch.ID, "monitor_org", monitor.OrgID, "channel_org", ch.OrgID)
			continue
		}
		if !ch.Enabled {
			slog.Warn("escalation: notification channel disabled", "channel_id", ch.ID, "name", ch.Name)
			continue
		}

		// SMS/voice quota enforcement (SaaS only, no-op in FOSS).
		if !e.checkSMSVoiceQuota(ctx, ch, monitor.OrgID) {
			continue
		}

		// Encrypt the channel config for transit.
		// Prepend version byte: 0x00 = plaintext, 0x01 = AES-256-GCM encrypted.
		var versionedConfig []byte
		if e.envelope != nil {
			encConfig, err := e.envelope.Encrypt([]byte(ch.Config))
			if err != nil {
				slog.Error("escalation: encrypt channel config", "error", err)
				continue
			}
			versionedConfig = append([]byte{0x01}, encConfig...)
		} else {
			versionedConfig = append([]byte{0x00}, []byte(ch.Config)...)
		}

		for _, userID := range userIDs {
			job := domain.NotificationJob{
				ID:           uuid.New().String(),
				AlertID:      alert.ID,
				OrgID:        alert.OrgID,
				MonitorName:  monitor.Name,
				Severity:     string(alert.Severity),
				Channel:      ch.Type, // "slack", "webhook", etc.  - for dispatcher routing
				TargetConfig: base64.StdEncoding.EncodeToString(versionedConfig),
				Message:      fmt.Sprintf("Alert: %s is %s", monitor.Name, alert.Status),
				DedupeKey:    fmt.Sprintf("%s-%s-%s", alert.ID, step.ID, userID),
			}

			e.enrichJobWithServiceContext(ctx, &job, monitor, alert)

			data, err := json.Marshal(job)
			if err != nil {
				continue
			}

			// Dual-write: bus for speed, outbox for durability.
			// The bus delivers near-instantly; if the dispatcher handles
			// it, it marks the outbox row complete. If the worker dies
			// before completing, the outbox poller reclaims the stale job.
			_ = e.store.Outbox().Enqueue(ctx, job.ID, string(data))
			_ = e.bus.PublishDurable(ctx, "notify.request", data, job.ID)

			e.createEvent(ctx, alert.ID, domain.EventNotified, ch.Type, userID, nil)
		}
	}
}

func (e *Engine) resolveTarget(ctx context.Context, target *domain.StepTarget, monitor *domain.Monitor) ([]string, error) {
	switch target.TargetType {
	case domain.TargetOnCallPrimary:
		userID, err := e.oncall.Resolve(ctx, target.TargetID, 0, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		return []string{userID}, nil

	case domain.TargetOnCallSecondary:
		userID, err := e.oncall.Resolve(ctx, target.TargetID, 1, time.Now().UTC())
		if err != nil {
			return nil, err
		}
		return []string{userID}, nil

	case domain.TargetUser:
		return []string{target.TargetID}, nil

	case domain.TargetTeam:
		members, err := e.store.Teams().ListMembers(ctx, target.TargetID)
		if err != nil {
			return nil, err
		}
		var ids []string
		for _, m := range members {
			ids = append(ids, m.UserID)
		}
		return ids, nil

	case domain.TargetChannel:
		// Channel targets broadcast to the channel itself  - no specific user.
		return []string{"_channel"}, nil

	default:
		return nil, fmt.Errorf("unknown target type: %s", target.TargetType)
	}
}

func (e *Engine) createEvent(ctx context.Context, alertID string, eventType domain.AlertEventType, channel, targetUserID string, detail json.RawMessage) {
	evt := &domain.AlertEvent{
		ID:           uuid.New().String(),
		AlertID:      alertID,
		EventType:    eventType,
		Channel:      channel,
		TargetUserID: targetUserID,
		Detail:       detail,
		CreatedAt:    time.Now().UTC(),
	}
	if err := e.store.Alerts().CreateEvent(ctx, evt); err != nil {
		slog.Error("escalation: create event", "error", err)
	}
}

// getLoopCount extracts the loop count from the notifications_sent JSON field.
func (e *Engine) getLoopCount(state *domain.AlertEscalationState) int {
	if len(state.NotificationsSent) == 0 {
		return 0
	}
	var meta struct {
		LoopCount int `json:"loop_count"`
	}
	if err := json.Unmarshal(state.NotificationsSent, &meta); err != nil {
		return 0
	}
	return meta.LoopCount
}

// setLoopCount stores the loop count in the notifications_sent JSON field.
func (e *Engine) setLoopCount(state *domain.AlertEscalationState, count int) {
	meta := struct {
		LoopCount int `json:"loop_count"`
	}{LoopCount: count}
	data, _ := json.Marshal(meta)
	state.NotificationsSent = data
}
