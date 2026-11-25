package escalation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents"
	"github.com/YipYap-run/YipYap-FOSS/internal/cloudevents/types"
	"github.com/YipYap-run/YipYap-FOSS/internal/crypto"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
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
	// AlertID, ResolvedBy and Channel are set on a recover sent from a chat
	// button: only that alert is resolved, and the timeline names who did it.
	AlertID    string `json:"alert_id,omitempty"`
	ResolvedBy string `json:"resolved_by,omitempty"`
	Channel    string `json:"channel,omitempty"`
}

// Engine is the core escalation state machine. It listens for alert events
// on the bus and manages escalation progression via a ticker goroutine.
type Engine struct {
	store    store.Store
	bus      bus.Bus
	envelope *crypto.Envelope
	oncall   *OnCallResolver
	metrics  EngineMetrics
	emitter  cloudevents.Emitter
	// quotaMailer and quotaBaseURL send SMS/voice usage warnings to org
	// owners and admins (SaaS only). A nil mailer only logs.
	quotaMailer  Mailer
	quotaBaseURL string
	ackLink      AckLinkFunc
	// now is the clock; nil means time.Now. Tests set it.
	now    func() time.Time
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

const (
	// escalationTickInterval is how often the engine advances firing alerts.
	escalationTickInterval = 5 * time.Second
	// escalationLeaderKey is the dedup key used to elect a single engine per
	// tick window. The dedup Claim is atomic first-writer-wins with a TTL;
	// the FOSS stub always claims (single-box).
	escalationLeaderKey = "escalation-leader"
	// escalationLeaderTTL is slightly shorter than the tick interval so each
	// tick re-runs the election instead of being blocked by the prior claim.
	escalationLeaderTTL = escalationTickInterval - time.Second
)

// SetCloudEventEmitter attaches a CloudEvent emitter to the engine. A nil
// emitter disables CloudEvent emission (the default). Must be called before
// Start to avoid races with the handlers.
func (e *Engine) SetCloudEventEmitter(em cloudevents.Emitter) {
	e.emitter = em
}

// Mailer is the subset of the SMTP mailer the engine needs.
type Mailer interface {
	Send(to, subject, body string) error
}

// SetQuotaMailer attaches the mailer used for SMS/voice usage warnings and
// the public base URL used in their links. Must be called before Start.
func (e *Engine) SetQuotaMailer(m Mailer, baseURL string) {
	e.quotaMailer = m
	e.quotaBaseURL = baseURL
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
//
// It returns an error when any subscription fails (a NATS permission change,
// a stream mismatch, a consumer cap), so the process fails startup instead
// of reporting healthy while alerts are created but never escalated. The
// caller must treat the error as fatal. Subscriptions that did succeed are
// left for bus.Close to stop: Unsubscribe would delete the shared durable
// consumers that other running replicas still use. (core-10)
func (e *Engine) Start(ctx context.Context) error {
	e.ctx, e.cancel = context.WithCancel(ctx)

	subs := []struct {
		subject, consumer string
		fn                func(ctx context.Context, subject string, data []byte) error
	}{
		{"alert.trigger", "escalation-trigger", e.handleTrigger},
		{"alert.recover", "escalation-recover", e.handleRecover},
		{"alert.ack", "escalation-ack", e.handleAck},
	}
	for _, sub := range subs {
		if err := e.bus.PullSubscribe(sub.subject, sub.consumer, e.wrapHandler(sub.fn)); err != nil {
			e.cancel()
			return fmt.Errorf("escalation: subscribe %s: %w", sub.subject, err)
		}
	}

	e.done = make(chan struct{})
	go e.tickLoop()
	return nil
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
	existing, err := e.store.Alerts().GetActiveByMonitor(ctx, evt.MonitorID)
	if err != nil && !errors.Is(err, store.ErrAlertNotFound) {
		// A failed lookup is not "no alert". Nak so the trigger is
		// redelivered instead of guessing (mon-13).
		slog.Error("escalation: get active alert", "monitor_id", evt.MonitorID, "error", err)
		return err
	}
	if existing != nil {
		return nil // already firing, skip
	}

	now := e.clock()
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
		if errors.Is(err, store.ErrActiveAlertExists) {
			// Another instance created the active alert first. This is
			// the one expected failure; ack it as a duplicate.
			slog.Debug("escalation: alert already exists for monitor", "monitor_id", evt.MonitorID)
			return nil
		}
		// Anything else (DB down, timeout) must not be acked, or the
		// outage goes unpaged. Returning the error naks the message so the
		// bus redelivers it (mon-13).
		slog.Error("escalation: create alert", "monitor_id", evt.MonitorID, "error", err)
		return err
	}

	if e.metrics != nil {
		e.metrics.AddActiveAlert(ctx, alert.OrgID, 1)
	}

	// Record triggered event.
	e.createEvent(ctx, alertID, domain.EventTriggered, "", "", nil)
	e.announce(ctx, alert)

	// Emit CloudEvent (no-op if no emitter is attached).
	if err := cloudevents.EmitAlertFired(ctx, e.emitter, alert.OrgID, types.AlertFiredV1Data{
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		Severity:  string(alert.Severity),
		Reason:    alert.Error,
		FiredAt:   alert.StartedAt,
	}); err != nil {
		slog.Warn("escalation: emit alert.fired.v1", "alert_id", alert.ID, "error", err)
	}

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
	e.setStepPosition(state, firstStep.Position)
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
	if errors.Is(err, store.ErrAlertNotFound) {
		return nil // no active alert to resolve
	}
	if err != nil {
		slog.Error("escalation: get active alert for recovery", "monitor_id", evt.MonitorID, "error", err)
		return err
	}
	if evt.AlertID != "" && alert.ID != evt.AlertID {
		return nil // the clicked alert is no longer the active one
	}

	now := e.clock()
	alert.Status = domain.AlertResolved
	alert.ResolvedAt = &now

	if err := e.store.Alerts().Update(ctx, alert); err != nil {
		slog.Error("escalation: resolve alert", "error", err)
		return err
	}

	if e.metrics != nil {
		e.metrics.AddActiveAlert(ctx, alert.OrgID, -1)
	}

	SendResolves(ctx, e.store, e.bus, e.cipher(), alert, now)

	// Clean up escalation state.
	_ = e.store.Alerts().DeleteEscalationState(ctx, alert.ID)

	e.createEvent(ctx, alert.ID, domain.EventResolved, evt.Channel, evt.ResolvedBy, nil)
	e.announce(ctx, alert)

	// Emit CloudEvent (no-op if no emitter is attached). resolved_by is empty
	// when a successful check resolved the alert, set for a chat button.
	if err := cloudevents.EmitAlertResolved(ctx, e.emitter, alert.OrgID, types.AlertResolvedV1Data{
		AlertID:    alert.ID,
		MonitorID:  alert.MonitorID,
		ResolvedBy: evt.ResolvedBy,
		ResolvedAt: *alert.ResolvedAt,
	}); err != nil {
		slog.Warn("escalation: emit alert.resolved.v1", "alert_id", alert.ID, "error", err)
	}

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
		now := e.clock()
		active.Status = domain.AlertResolved
		active.ResolvedAt = &now
		if err := e.store.Alerts().Update(ctx, active); err != nil {
			slog.Error("escalation: auto-resolve alert", "alert_id", active.ID, "error", err)
			return
		}
		if e.metrics != nil {
			e.metrics.AddActiveAlert(ctx, active.OrgID, -1)
		}
		SendResolves(ctx, e.store, e.bus, e.cipher(), active, now)
		_ = e.store.Alerts().DeleteEscalationState(ctx, active.ID)
		e.createEvent(ctx, active.ID, domain.EventResolved, "", "", nil)
		e.announce(ctx, active)

		if err := cloudevents.EmitAlertResolved(ctx, e.emitter, active.OrgID, types.AlertResolvedV1Data{
			AlertID:    active.ID,
			MonitorID:  active.MonitorID,
			ResolvedAt: *active.ResolvedAt,
		}); err != nil {
			slog.Warn("escalation: emit alert.resolved.v1", "alert_id", active.ID, "error", err)
		}
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

	// Only a firing alert can be acknowledged. A second ack (a redelivered
	// message, or the same alert acked from two channels) must not add a
	// second timeline entry or emit alert.acknowledged.v1 again. (core-16)
	if alert.Status != domain.AlertFiring {
		return nil
	}

	now := e.clock()
	acked, err := e.store.Alerts().Acknowledge(ctx, alert.ID, evt.UserID, now)
	if err != nil {
		slog.Error("escalation: ack alert", "error", err)
		return err
	}
	if !acked {
		// Another ack or a resolve won the race since the read above.
		return nil
	}
	alert.Status = domain.AlertAcknowledged
	alert.AcknowledgedAt = &now
	alert.AcknowledgedBy = evt.UserID

	var detail json.RawMessage
	if evt.Via != "" {
		detail, _ = json.Marshal(map[string]string{
			"via":          evt.Via,
			"channel_id":   evt.ChannelID,
			"channel_name": evt.ChannelName,
		})
	}
	e.createEvent(ctx, alert.ID, domain.EventAck, evt.Channel, evt.UserID, detail)
	e.broadcastAcked(ctx, alert)
	e.announce(ctx, alert)

	if err := cloudevents.EmitAlertAcknowledged(ctx, e.emitter, alert.OrgID, types.AlertAcknowledgedV1Data{
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		AckedBy:   evt.UserID,
		AckedAt:   *alert.AcknowledgedAt,
	}); err != nil {
		slog.Warn("escalation: emit alert.acknowledged.v1", "alert_id", alert.ID, "error", err)
	}
	return nil
}

// broadcastAcked tells live views (the WebSocket hub) that an alert was
// acknowledged. It is a plain broadcast, not a durable message: a missed
// frame only delays a dashboard refresh.
func (e *Engine) broadcastAcked(ctx context.Context, alert *domain.Alert) {
	msg := domain.AlertAckedMessage{
		OrgID:     alert.OrgID,
		AlertID:   alert.ID,
		MonitorID: alert.MonitorID,
		Status:    string(alert.Status),
	}
	if m, err := e.store.Monitors().GetByID(ctx, alert.MonitorID); err == nil {
		msg.MonitorName = m.Name
	}
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}
	if err := e.bus.Publish(ctx, "alert.acknowledged", data); err != nil {
		slog.Warn("escalation: broadcast alert.acknowledged", "alert_id", alert.ID, "error", err)
	}
}

func (e *Engine) tickLoop() {
	defer close(e.done)

	ticker := time.NewTicker(escalationTickInterval)
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

	// Leader election: in a multi-instance deployment every engine runs its
	// own ticker, so without this they would all advance the same alerts and
	// double-fire escalations. Claim a short lease; only the winner processes
	// this tick window.
	if leader, err := e.store.Dedup().Claim(ctx, escalationLeaderKey, escalationLeaderTTL); err != nil || !leader {
		return
	}
	e.advanceAlerts(ctx, e.clock())
}

// advanceAlerts runs one tick for every firing alert.
func (e *Engine) advanceAlerts(ctx context.Context, now time.Time) {
	alerts, err := e.store.Alerts().ListFiring(ctx)
	if err != nil {
		slog.Error("escalation: list firing", "error", err)
		return
	}
	for _, alert := range alerts {
		e.advanceAlert(ctx, alert, now)
	}
}

// advanceAlert sends any delayed pages that are due and takes at most one
// step action (page the next target group, repeat the step, move to the
// next step, loop, or stop) when the step's timer has run out.
func (e *Engine) advanceAlert(ctx context.Context, alert *domain.Alert, now time.Time) {
	state, err := e.store.Alerts().GetEscalationState(ctx, alert.ID)
	if err != nil {
		return // no escalation state means no policy
	}

	monitor, err := e.store.Monitors().GetByID(ctx, alert.MonitorID)
	if err != nil || monitor.EscalationPolicyID == "" {
		return
	}

	steps, err := e.store.EscalationPolicies().GetSteps(ctx, monitor.EscalationPolicyID)
	if err != nil || len(steps) == 0 {
		return
	}

	// Find the current step. A policy edit can replace step ids; remap
	// by position so the alert keeps escalating instead of stalling.
	currentStep := e.currentStep(ctx, alert, state, steps)
	if currentStep == nil {
		return
	}

	// Maintenance: hold the alert while a suppressing window covers the
	// monitor, and when the window has ended page only if the monitor is
	// still failing.
	if e.handleMaintenance(ctx, alert, monitor, currentStep, state, now) {
		return
	}

	// Delayed notification rules from earlier pages.
	e.processPending(ctx, alert, monitor, state, now)

	meta := readMeta(state)
	if meta.Stopped {
		return
	}

	targets, err := e.store.EscalationPolicies().GetTargets(ctx, currentStep.ID)
	if err != nil {
		slog.Error("escalation: get targets", "step_id", currentStep.ID, "error", err)
		return
	}
	groups := targetGroups(targets)

	group := meta.Group
	pagedAt := state.StepEnteredAt
	if meta.PagedAt != nil {
		pagedAt = *meta.PagedAt
	} else {
		// State written before step rounds were tracked: the step was
		// paged in full when it was entered.
		group = len(groups)
	}
	// The due time is worked out from the step as it is now, so an edit
	// to the policy's timings applies to open alerts right away.
	if now.Before(pagedAt.Add(stepGap(currentStep, state, group, len(groups)))) {
		return
	}

	// Sequential targets: page the next group of this round.
	if group < len(groups) {
		e.pageNextGroup(ctx, alert, monitor, currentStep, state, now)
		return
	}

	// The round is done. Repeat the step if repeats are left.
	if state.RetryCount < currentStep.RepeatCount {
		state.RetryCount++
		state.StepEnteredAt = now
		e.notifyStep(ctx, alert, monitor, currentStep, state)
		return
	}

	// A terminal step ends escalation once its pages are done.
	if currentStep.IsTerminal {
		e.stopEscalation(ctx, alert, currentStep, state)
		return
	}

	// Retries exhausted. Try to advance to next step.
	nextStep, err := e.store.EscalationPolicies().GetNextStep(ctx, monitor.EscalationPolicyID, currentStep.Position)
	if err != nil {
		return
	}

	if nextStep != nil {
		e.enterStep(ctx, alert, monitor, nextStep, state, now)
		return
	}

	// No next step. Check loop policy.
	policy, err := e.store.EscalationPolicies().GetByID(ctx, monitor.EscalationPolicyID)
	if err != nil || !policy.Loop {
		// Not looping: leave the alert firing with no more escalation.
		return
	}
	loopCount := e.getLoopCount(state)
	if policy.MaxLoops != nil && loopCount >= *policy.MaxLoops {
		return // exhausted loops
	}
	e.setLoopCount(state, loopCount+1)
	e.enterStep(ctx, alert, monitor, &steps[0], state, now)
}

// stepGap is how long after the last page of a step its next action is
// due. Between groups of a round and before moving on it is the step's
// wait; between repeats it is the repeat interval when one is set.
func stepGap(step *domain.EscalationStep, state *domain.AlertEscalationState, group, groups int) time.Duration {
	if group >= groups && state.RetryCount < step.RepeatCount && step.RepeatIntervalSeconds > 0 {
		return time.Duration(step.RepeatIntervalSeconds) * time.Second
	}
	return time.Duration(step.WaitSeconds) * time.Second
}

// enterStep moves the alert onto step and pages it.
func (e *Engine) enterStep(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, state *domain.AlertEscalationState, now time.Time) {
	state.CurrentStepID = step.ID
	state.StepEnteredAt = now
	state.RetryCount = 0
	e.setStepPosition(state, step.Position)
	if e.metrics != nil {
		e.metrics.RecordEscalationStep(ctx, alert.OrgID, int64(step.Position))
	}
	_ = e.store.Alerts().UpsertEscalationState(ctx, state)

	alert.CurrentEscalationStep = step.ID
	_ = e.store.Alerts().Update(ctx, alert)

	// The timeline shows "Escalated to step N"; loop counts restarts.
	escDetail, _ := json.Marshal(map[string]any{
		"step_id":       step.ID,
		"step_position": step.Position,
		"loop":          readMeta(state).LoopCount,
	})
	e.createEvent(ctx, alert.ID, domain.EventEscalated, "", "", escDetail)

	if err := cloudevents.EmitAlertEscalated(ctx, e.emitter, alert.OrgID, types.AlertEscalatedV1Data{
		AlertID:     alert.ID,
		MonitorID:   alert.MonitorID,
		StepIndex:   int64(step.Position),
		ToChannel:   e.stepChannelHint(ctx, step.ID),
		EscalatedAt: now,
	}); err != nil {
		slog.Warn("escalation: emit alert.escalated.v1", "alert_id", alert.ID, "error", err)
	}

	e.notifyStep(ctx, alert, monitor, step, state)
}

// stopEscalation records that a terminal step finished. The alert stays
// open; delayed rules already started still go out until it is
// acknowledged or resolved.
func (e *Engine) stopEscalation(ctx context.Context, alert *domain.Alert, step *domain.EscalationStep, state *domain.AlertEscalationState) {
	meta := readMeta(state)
	meta.Stopped = true
	writeMeta(state, meta)
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: record stop", "alert_id", alert.ID, "error", err)
		return
	}
	detail, _ := json.Marshal(map[string]any{
		"reason":        "terminal_step",
		"step_id":       step.ID,
		"step_position": step.Position,
	})
	e.createEvent(ctx, alert.ID, domain.EventEscalationStopped, "", "", detail)
	e.announce(ctx, alert)
}

// notifyStep starts a round of the step: it pages the first target group
// and schedules the rest.
func (e *Engine) notifyStep(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, state *domain.AlertEscalationState) {
	now := e.clock()
	// A suppressing maintenance window holds the page. The tick loop pages
	// the current step once the window ends if the monitor is still down.
	if windowID := e.suppressingWindow(ctx, monitor.ID, now); windowID != "" {
		e.markSuppressed(ctx, alert, state, windowID)
		return
	}
	meta := readMeta(state)
	meta.Group = 0
	meta.Stopped = false
	writeMeta(state, meta)
	e.pageNextGroup(ctx, alert, monitor, step, state, now)
}

// pageNextGroup pages the step's next target group, then sets when the
// next action is due and saves the state.
func (e *Engine) pageNextGroup(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, state *domain.AlertEscalationState, now time.Time) {
	targets, err := e.store.EscalationPolicies().GetTargets(ctx, step.ID)
	if err != nil {
		slog.Error("escalation: get targets", "step_id", step.ID, "error", err)
		e.notifyFailed(ctx, alert.ID, step, nil, "", "", "could not load the step's targets")
	}
	groups := targetGroups(targets)
	state.LastNotifiedAt = &now

	group := readMeta(state).Group
	switch {
	case err != nil:
		group = len(groups)
	case len(groups) == 0:
		e.notifyFailed(ctx, alert.ID, step, nil, "", "", "escalation step has no targets")
	case group < len(groups):
		e.pageGroup(ctx, alert, monitor, step, groups[group], state, e.roundKey(alert, step, state, group), now)
		group++
	}

	// pageGroup may have queued delayed rules, so read the meta again.
	meta := readMeta(state)
	meta.Group = group
	meta.PagedAt = &now
	writeMeta(state, meta)
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: save step state", "alert_id", alert.ID, "error", err)
	}
	e.announce(ctx, alert)
}

// notifyFailed records a notify_failed timeline event so a dropped page is
// visible on the alert instead of only in server logs. target may be nil
// when the whole step failed.
func (e *Engine) notifyFailed(ctx context.Context, alertID string, step *domain.EscalationStep, target *domain.StepTarget, channel, userID, reason string) {
	e.notifyFailedWith(ctx, alertID, step, target, channel, userID, reason, nil)
}

// notifyFailedWith is notifyFailed with extra detail fields (such as the
// contact method that could not be paged).
func (e *Engine) notifyFailedWith(ctx context.Context, alertID string, step *domain.EscalationStep, target *domain.StepTarget, channel, userID, reason string, extra map[string]any) {
	detail := map[string]any{}
	for k, v := range extra {
		detail[k] = v
	}
	detail["reason"] = reason
	if step != nil {
		detail["step_id"] = step.ID
		detail["step_position"] = step.Position
	}
	if target != nil {
		detail["target_type"] = string(target.TargetType)
		if target.TargetID != "" {
			detail["target_id"] = target.TargetID
		}
		if target.ChannelID != "" {
			detail["channel_id"] = target.ChannelID
		}
	}
	raw, _ := json.Marshal(detail)
	e.createEvent(ctx, alertID, domain.EventNotifyFailed, channel, userID, raw)
}

func (e *Engine) resolveTarget(ctx context.Context, target *domain.StepTarget, monitor *domain.Monitor) ([]string, error) {
	switch target.TargetType {
	case domain.TargetOnCallPrimary:
		userID, err := e.oncall.Resolve(ctx, monitor.OrgID, target.TargetID, 0, e.clock())
		if err != nil {
			return nil, err
		}
		return []string{userID}, nil

	case domain.TargetOnCallSecondary:
		userID, err := e.oncall.Resolve(ctx, monitor.OrgID, target.TargetID, 1, e.clock())
		if err != nil {
			return nil, err
		}
		return []string{userID}, nil

	case domain.TargetUser:
		if target.TargetID == "" {
			return nil, fmt.Errorf("user target has no user selected")
		}
		u, err := e.store.Users().GetByID(ctx, target.TargetID)
		if err != nil || u == nil || u.OrgID != monitor.OrgID {
			return nil, fmt.Errorf("user %s not found", target.TargetID)
		}
		return []string{u.ID}, nil

	case domain.TargetTeam:
		if target.TargetID == "" {
			return nil, fmt.Errorf("team target has no team selected")
		}
		team, err := e.store.Teams().GetByID(ctx, target.TargetID)
		if err != nil || team == nil || team.OrgID != monitor.OrgID {
			return nil, fmt.Errorf("team %s not found", target.TargetID)
		}
		members, err := e.store.Teams().ListMembers(ctx, target.TargetID)
		if err != nil {
			return nil, fmt.Errorf("could not list members of team %q", team.Name)
		}
		var ids []string
		for _, m := range members {
			ids = append(ids, m.UserID)
		}
		if len(ids) == 0 {
			return nil, fmt.Errorf("team %q has no members", team.Name)
		}
		return ids, nil

	default:
		return nil, fmt.Errorf("unknown target type: %s", target.TargetType)
	}
}

// announce publishes an alert.changed hint so open dashboards and alert
// pages refresh. It is best effort: a lost hint only delays a refresh.
func (e *Engine) announce(ctx context.Context, alert *domain.Alert) {
	if e.bus == nil || alert == nil {
		return
	}
	data, err := json.Marshal(domain.NewAlertChanged(alert))
	if err != nil {
		return
	}
	if err := e.bus.Publish(ctx, domain.SubjectAlertChanged, data); err != nil {
		slog.Debug("escalation: publish alert.changed", "alert_id", alert.ID, "error", err)
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
		CreatedAt:    e.clock(),
	}
	if err := e.store.Alerts().CreateEvent(ctx, evt); err != nil {
		slog.Error("escalation: create event", "error", err)
	}
}

// stepChannelHint returns the notification channel type of the first target
// on the step that has a channel, or "" if there is none. A step may fan out
// to multiple channels; the CloudEvent to_channel field is a hint rather
// than an exhaustive list.
func (e *Engine) stepChannelHint(ctx context.Context, stepID string) string {
	targets, err := e.store.EscalationPolicies().GetTargets(ctx, stepID)
	if err != nil {
		return ""
	}
	for _, t := range targets {
		if t.ChannelID == "" {
			continue
		}
		ch, err := e.store.NotificationChannels().GetByID(ctx, t.ChannelID)
		if err != nil || ch == nil {
			return ""
		}
		return ch.Type
	}
	return ""
}

// escalationMeta is the JSON kept in AlertEscalationState.NotificationsSent.
// StepPosition lets the engine find its place again when a policy edit
// replaces the step ids.
type escalationMeta struct {
	LoopCount    int  `json:"loop_count"`
	StepPosition *int `json:"step_position,omitempty"`
	// Suppressed is set when a maintenance window held a page for this
	// alert. It is cleared once the held page goes out after the window.
	Suppressed bool `json:"suppressed,omitempty"`
	// SuppressedWindowID is the window that held the page.
	SuppressedWindowID string `json:"suppressed_window_id,omitempty"`
	// RecoveredAfterWindow records that the window ended with the monitor
	// passing, so the "not paged" timeline event is written once.
	RecoveredAfterWindow bool `json:"recovered_after_window,omitempty"`
	// Group is the next target group to page in the current round of the
	// step. It equals the group count once the round is done.
	Group int `json:"group,omitempty"`
	// PagedAt is when the step last paged a group. The next action is due
	// stepGap after it. Nil for state written before rounds were tracked.
	PagedAt *time.Time `json:"paged_at,omitempty"`
	// Stopped is set once a terminal step has finished.
	Stopped bool `json:"stopped,omitempty"`
	// Pending holds delayed notification rules not yet sent.
	Pending []pendingPage `json:"pending,omitempty"`
}

func readMeta(state *domain.AlertEscalationState) escalationMeta {
	var meta escalationMeta
	if len(state.NotificationsSent) > 0 {
		_ = json.Unmarshal(state.NotificationsSent, &meta)
	}
	return meta
}

func writeMeta(state *domain.AlertEscalationState, meta escalationMeta) {
	data, _ := json.Marshal(meta)
	state.NotificationsSent = data
}

// getLoopCount extracts the loop count from the notifications_sent JSON field.
func (e *Engine) getLoopCount(state *domain.AlertEscalationState) int {
	return readMeta(state).LoopCount
}

// setLoopCount stores the loop count in the notifications_sent JSON field.
func (e *Engine) setLoopCount(state *domain.AlertEscalationState, count int) {
	meta := readMeta(state)
	meta.LoopCount = count
	writeMeta(state, meta)
}

// setStepPosition records the position of the step the alert is on.
func (e *Engine) setStepPosition(state *domain.AlertEscalationState, position int) {
	meta := readMeta(state)
	meta.StepPosition = &position
	writeMeta(state, meta)
}

// currentStep returns the step the alert is on. When the stored step id no
// longer exists (the policy's steps were replaced), it picks the step at the
// recorded position, or the last step if the policy got shorter, or the
// first step for state written before positions were recorded. The remapped
// step keeps the original entry time and retry count, so the wait carries on
// rather than restarting. Returns nil when the policy has no steps.
func (e *Engine) currentStep(ctx context.Context, alert *domain.Alert, state *domain.AlertEscalationState, steps []domain.EscalationStep) *domain.EscalationStep {
	for i := range steps {
		if steps[i].ID == state.CurrentStepID {
			return &steps[i]
		}
	}
	if len(steps) == 0 {
		return nil
	}

	idx := 0
	if pos := readMeta(state).StepPosition; pos != nil {
		idx = len(steps) - 1
		for i := range steps {
			if steps[i].Position >= *pos {
				idx = i
				break
			}
		}
	}
	step := &steps[idx]
	slog.Info("escalation: remapped alert to replaced step", "alert_id", alert.ID, "old_step", state.CurrentStepID, "new_step", step.ID, "position", step.Position)

	state.CurrentStepID = step.ID
	e.setStepPosition(state, step.Position)
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: upsert remapped state", "alert_id", alert.ID, "error", err)
	}
	alert.CurrentEscalationStep = step.ID
	_ = e.store.Alerts().Update(ctx, alert)
	return step
}
