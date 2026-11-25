package escalation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/YipYap-run/YipYap-FOSS/internal/bus"
	"github.com/YipYap-run/YipYap-FOSS/internal/contactmethod"
	"github.com/YipYap-run/YipYap-FOSS/internal/domain"
	"github.com/YipYap-run/YipYap-FOSS/internal/store"
	"github.com/google/uuid"
)

// Person-first paging (core-monitoring decisions 1 and 6, core-notify
// decision 5).
//
// A step's user, on-call and team targets resolve to people. Each person is
// paged through their own verified contact methods, following their
// notification rules: a rule with no delay is sent at once, a delayed rule
// is kept in the escalation state and sent by the tick loop when it falls
// due, unless the alert has been acknowledged or resolved by then. A target's
// channel is an extra destination, sent once per page of the step no matter
// how many people the targets resolve to.
//
// Step timing. A step's targets form groups: the first target starts a
// group, a target marked simultaneous joins the group before it, and one
// that is not starts a new group. One page of the step (a round) pages the
// groups in order, wait_seconds apart. After the last group the step either
// repeats (repeat_count times, repeat_interval_seconds apart, or
// wait_seconds when no interval is set) or, wait_seconds after its last
// page, hands over to the next step. A terminal step never hands over: once
// its pages are done escalation stops, even when the policy loops.

// maxPendingPages caps the delayed deliveries kept per alert so the state
// row stays small.
const maxPendingPages = 500

// pendingPage is one delayed notification rule waiting to be sent.
type pendingPage struct {
	UserID     string    `json:"user_id"`
	RuleID     string    `json:"rule_id"`
	MethodID   string    `json:"method_id"`
	DueAt      time.Time `json:"due_at"`
	StepID     string    `json:"step_id"`
	StepPos    int       `json:"step_position"`
	TargetType string    `json:"target_type,omitempty"`
	TargetID   string    `json:"target_id,omitempty"`
	// Key is the dedupe key of the job this entry becomes.
	Key string `json:"key"`
}

// targetGroups splits a step's targets into the groups paged together.
func targetGroups(targets []domain.StepTarget) [][]domain.StepTarget {
	var groups [][]domain.StepTarget
	for i, t := range targets {
		if i == 0 || !t.Simultaneous {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], t)
	}
	return groups
}

// clock returns the engine's current time in UTC.
func (e *Engine) clock() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) encrypter() contactmethod.Encrypter {
	if e.envelope == nil {
		return nil
	}
	return e.envelope.Encrypt
}

func (e *Engine) cipher() contactmethod.Cipher {
	if e.envelope == nil {
		return contactmethod.Cipher{}
	}
	return contactmethod.Cipher{Enc: e.envelope.Encrypt, Dec: e.envelope.Decrypt}
}

// roundKey identifies one page of one group of a step, so a retried tick
// cannot send the same page twice while a repeat or a loop still can.
func (e *Engine) roundKey(alert *domain.Alert, step *domain.EscalationStep, state *domain.AlertEscalationState, group int) string {
	return fmt.Sprintf("%s-%s-l%d-r%d-g%d", alert.ID, step.ID, e.getLoopCount(state), state.RetryCount, group)
}

// pageGroup pages one group of a step's targets: every person the targets
// resolve to (each once, even when they appear in several targets) and
// every distinct channel set on the targets (each once).
func (e *Engine) pageGroup(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, group []domain.StepTarget, state *domain.AlertEscalationState, key string, now time.Time) {
	pagedUsers := map[string]bool{}
	sentChannels := map[string]bool{}
	for i := range group {
		target := &group[i]
		if target.TargetType != domain.TargetChannel {
			userIDs, err := e.resolveTarget(ctx, target, monitor)
			switch {
			case err != nil:
				slog.Warn("escalation: resolve target", "target", target.TargetType, "error", err)
				e.notifyFailed(ctx, alert.ID, step, target, "", "", err.Error())
			case len(userIDs) == 0:
				e.notifyFailed(ctx, alert.ID, step, target, "", "", "target resolved to nobody")
			}
			for _, uid := range userIDs {
				if pagedUsers[uid] {
					continue
				}
				pagedUsers[uid] = true
				e.pageUser(ctx, alert, monitor, step, target, uid, state, key, now)
			}
		}

		if target.ChannelID == "" {
			if target.TargetType == domain.TargetChannel {
				e.notifyFailed(ctx, alert.ID, step, target, "", "", "no notification channel set for this target")
			}
			continue
		}
		if sentChannels[target.ChannelID] {
			continue
		}
		sentChannels[target.ChannelID] = true
		e.sendChannel(ctx, alert, monitor, step, target, key+"-c"+target.ChannelID)
	}
}

// pageUser starts a person's notification rules for this page. Rules with
// no delay are sent now; delayed rules are queued in the escalation state.
func (e *Engine) pageUser(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, target *domain.StepTarget, userID string, state *domain.AlertEscalationState, key string, now time.Time) {
	cp, ok := e.store.(store.ContactMethodProvider)
	if !ok {
		e.notifyFailed(ctx, alert.ID, step, target, "", userID, "personal contact methods are not available on this server")
		return
	}
	pts, err := cp.NotificationRules().ListPagingTargets(ctx, userID)
	if err != nil {
		slog.Error("escalation: list paging targets", "user_id", userID, "error", err)
		e.notifyFailed(ctx, alert.ID, step, target, "", userID, "could not load the person's notification rules")
		return
	}

	urgency := domain.UrgencyForSeverity(alert.Severity)
	matched := 0
	for _, pt := range pts {
		if pt.Rule == nil || !pt.Method.Verified() || pt.Method.OrgID != alert.OrgID {
			continue
		}
		if !pt.Rule.Urgency.Matches(urgency) {
			continue
		}
		matched++
		jobKey := fmt.Sprintf("%s-u%s-m%s-n%s", key, userID, pt.Method.ID, pt.Rule.ID)
		if pt.Rule.DelayMinutes <= 0 {
			e.deliverToMethod(ctx, alert, monitor, step, target, pt.Rule, pt.Method, jobKey)
			continue
		}
		e.schedulePending(state, pendingPage{
			UserID:     userID,
			RuleID:     pt.Rule.ID,
			MethodID:   pt.Method.ID,
			DueAt:      now.Add(time.Duration(pt.Rule.DelayMinutes) * time.Minute),
			StepID:     step.ID,
			StepPos:    step.Position,
			TargetType: string(target.TargetType),
			TargetID:   target.TargetID,
			Key:        jobKey,
		})
	}

	if matched == 0 {
		reason := "this person has no verified contact methods with a notification rule"
		if len(pts) > 0 {
			reason = fmt.Sprintf("this person has no notification rule for %s urgency alerts", urgency)
		}
		e.notifyFailed(ctx, alert.ID, step, target, "", userID, reason)
	}
}

// schedulePending queues a delayed rule. When the same rule is already
// queued for this person (a repeat or a later step paged them again), the
// earlier due time wins, so a re-page neither duplicates nor postpones it.
func (e *Engine) schedulePending(state *domain.AlertEscalationState, p pendingPage) {
	meta := readMeta(state)
	for i := range meta.Pending {
		q := &meta.Pending[i]
		if q.UserID == p.UserID && q.RuleID == p.RuleID {
			if p.DueAt.Before(q.DueAt) {
				*q = p
				writeMeta(state, meta)
			}
			return
		}
	}
	if len(meta.Pending) >= maxPendingPages {
		slog.Warn("escalation: too many delayed pages queued, dropping one", "alert_id", state.AlertID, "user_id", p.UserID)
		return
	}
	meta.Pending = append(meta.Pending, p)
	writeMeta(state, meta)
}

// processPending sends the delayed rules that have fallen due. It only runs
// for firing alerts, so an acknowledgement or a resolve stops the rest of a
// person's rules.
func (e *Engine) processPending(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, state *domain.AlertEscalationState, now time.Time) {
	meta := readMeta(state)
	if len(meta.Pending) == 0 {
		return
	}
	var due, keep []pendingPage
	for _, p := range meta.Pending {
		if now.Before(p.DueAt) {
			keep = append(keep, p)
		} else {
			due = append(due, p)
		}
	}
	if len(due) == 0 {
		return
	}
	// Persist first: a crash after this point loses at most these sends
	// rather than repeating them on every tick.
	meta.Pending = keep
	writeMeta(state, meta)
	if err := e.store.Alerts().UpsertEscalationState(ctx, state); err != nil {
		slog.Error("escalation: save delayed pages", "alert_id", alert.ID, "error", err)
		return
	}

	defer e.announce(ctx, alert)

	cp, ok := e.store.(store.ContactMethodProvider)
	if !ok {
		return
	}
	cache := map[string][]domain.PagingTarget{}
	for _, p := range due {
		pts, seen := cache[p.UserID]
		if !seen {
			var err error
			pts, err = cp.NotificationRules().ListPagingTargets(ctx, p.UserID)
			if err != nil {
				slog.Error("escalation: list paging targets", "user_id", p.UserID, "error", err)
			}
			cache[p.UserID] = pts
		}
		step := &domain.EscalationStep{ID: p.StepID, Position: p.StepPos}
		target := &domain.StepTarget{TargetType: domain.TargetType(p.TargetType), TargetID: p.TargetID}
		var found *domain.PagingTarget
		for i := range pts {
			if pts[i].Rule != nil && pts[i].Method != nil && pts[i].Rule.ID == p.RuleID && pts[i].Method.ID == p.MethodID {
				found = &pts[i]
				break
			}
		}
		if found == nil || !found.Method.Verified() || found.Method.OrgID != alert.OrgID {
			// The person removed the rule or method, or it is no longer
			// verified, after the page started. Their current settings win.
			slog.Info("escalation: delayed page dropped, rule or method changed", "alert_id", alert.ID, "user_id", p.UserID, "rule_id", p.RuleID)
			continue
		}
		e.deliverToMethod(ctx, alert, monitor, step, target, found.Rule, found.Method, p.Key)
	}
}

// deliverToMethod sends one page to one of a person's contact methods.
func (e *Engine) deliverToMethod(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, target *domain.StepTarget, rule *domain.NotificationRule, m *domain.ContactMethod, key string) {
	extra := map[string]any{
		"contact_method_id":   m.ID,
		"contact_method_type": string(m.Type),
		"rule_id":             rule.ID,
		"delay_minutes":       rule.DelayMinutes,
	}
	fail := func(channel, reason string) {
		e.notifyFailedWith(ctx, alert.ID, step, target, channel, m.UserID, reason, extra)
	}

	if !contactmethod.TypeSupported(m.Type) {
		fail("", fmt.Sprintf("%s contact methods are not available on this server", m.Type))
		return
	}
	// The plan is checked again at page time, so a downgrade stops paid
	// deliveries.
	if err := contactmethod.PlanError(ctx, e.store, alert.OrgID, m.Type); err != nil {
		fail("", err.Error())
		return
	}

	job := e.baseJob(ctx, alert, monitor, key)
	if err := contactmethod.FillJob(ctx, e.store, m, &job, e.cipher()); err != nil {
		slog.Warn("escalation: prepare contact method page", "alert_id", alert.ID, "method_id", m.ID, "error", err)
		fail("", "could not prepare the page: "+err.Error())
		return
	}
	if e.ackLink != nil {
		job.AckURL = e.ackLink(alert.ID, alert.OrgID, "", m.UserID)
	}
	quotaTarget := &domain.NotificationChannel{ID: m.ID, OrgID: m.OrgID, Type: job.Channel, Name: m.Label}
	if !e.reserveSMSVoice(ctx, alert, quotaTarget) {
		return
	}
	if !e.enqueue(ctx, &job) {
		fail(job.Channel, "could not queue the notification for delivery")
		return
	}
	e.recordPagedMethod(ctx, alert.ID, m)
	e.queuedEvent(ctx, alert.ID, step, target, job.Channel, m.UserID, job.ID, extra)
}

// sendChannel sends one page to a shared notification channel.
func (e *Engine) sendChannel(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, step *domain.EscalationStep, target *domain.StepTarget, key string) {
	ch, err := e.store.NotificationChannels().GetByID(ctx, target.ChannelID)
	if err != nil || ch == nil {
		slog.Error("escalation: get notification channel", "channel_id", target.ChannelID, "error", err)
		e.notifyFailed(ctx, alert.ID, step, target, "", "", "notification channel not found")
		return
	}
	if ch.OrgID != monitor.OrgID {
		slog.Error("escalation: channel org mismatch", "channel", ch.ID, "monitor_org", monitor.OrgID, "channel_org", ch.OrgID)
		e.notifyFailed(ctx, alert.ID, step, target, "", "", "notification channel not found")
		return
	}
	if !ch.Enabled {
		slog.Warn("escalation: notification channel disabled", "channel_id", ch.ID, "name", ch.Name)
		e.notifyFailed(ctx, alert.ID, step, target, ch.Type, "", fmt.Sprintf("notification channel %q is disabled", ch.Name))
		return
	}
	// Unverified SMS/voice numbers get nothing (SaaS only, no-op in FOSS).
	// Checked before the quota so a skipped send is not counted.
	if !e.checkPhoneVerified(ctx, ch, alert.ID) {
		return
	}
	// A slack_app channel whose workspace was disconnected (SaaS only).
	if !e.checkSlackInstall(ctx, ch, alert.ID) {
		return
	}
	// A discord_app channel whose server was disconnected (SaaS only).
	if !e.checkDiscordInstall(ctx, ch, alert.ID) {
		return
	}

	// Plan gate (SaaS only, no-op in FOSS). A skipped delivery is a failed
	// page from the responder's side, so it goes on the timeline instead
	// of only the server log.
	if reason := e.checkChannelAllowed(ctx, ch, monitor.OrgID); reason != "" {
		skipped, _ := json.Marshal(map[string]string{
			"channel_id":   ch.ID,
			"channel_name": ch.Name,
			"reason":       reason,
		})
		e.createEvent(ctx, alert.ID, domain.EventNotificationFailed, ch.Type, "", skipped)
		return
	}
	if !e.reserveSMSVoice(ctx, alert, ch) {
		return
	}

	tc, err := contactmethod.EncodeTargetConfig([]byte(ch.Config), e.encrypter())
	if err != nil {
		slog.Error("escalation: encrypt channel config", "error", err)
		e.notifyFailed(ctx, alert.ID, step, target, ch.Type, "", "could not prepare the notification")
		return
	}
	job := e.baseJob(ctx, alert, monitor, key)
	job.Channel = ch.Type
	job.TargetConfig = tc
	job.ChannelID = ch.ID
	job.ChannelName = ch.Name
	if e.ackLink != nil {
		job.AckURL = e.ackLink(alert.ID, alert.OrgID, ch.ID, "")
	}
	if !e.enqueue(ctx, &job) {
		e.notifyFailed(ctx, alert.ID, step, target, ch.Type, "", "could not queue the notification for delivery")
		return
	}
	e.recordPagedChannel(ctx, alert.ID, ch.ID)
	e.queuedEvent(ctx, alert.ID, step, target, ch.Type, "", job.ID, map[string]any{"channel_id": ch.ID, "channel_name": ch.Name})
}

func (e *Engine) recordPagedMethod(ctx context.Context, alertID string, m *domain.ContactMethod) {
	t := &domain.AlertPagedTarget{
		AlertID:   alertID,
		TargetKey: "method:" + m.ID,
		Kind:      domain.PagedTargetMethod,
		UserID:    m.UserID,
		MethodID:  m.ID,
		CreatedAt: e.clock(),
	}
	if err := e.store.Alerts().RecordPagedTarget(ctx, t); err != nil {
		slog.Warn("escalation: record paged contact method", "alert_id", alertID, "method_id", m.ID, "error", err)
	}
}

func (e *Engine) recordPagedChannel(ctx context.Context, alertID, channelID string) {
	t := &domain.AlertPagedTarget{
		AlertID:   alertID,
		TargetKey: "channel:" + channelID,
		Kind:      domain.PagedTargetChannel,
		ChannelID: channelID,
		CreatedAt: e.clock(),
	}
	if err := e.store.Alerts().RecordPagedTarget(ctx, t); err != nil {
		slog.Warn("escalation: record paged notification channel", "alert_id", alertID, "channel_id", channelID, "error", err)
	}
}

func (e *Engine) baseJob(ctx context.Context, alert *domain.Alert, monitor *domain.Monitor, key string) domain.NotificationJob {
	job := domain.NotificationJob{
		ID:          uuid.New().String(),
		AlertID:     alert.ID,
		OrgID:       alert.OrgID,
		MonitorID:   monitor.ID,
		MonitorName: monitor.Name,
		Status:      string(alert.Status),
		Severity:    string(alert.Severity),
		Message:     fmt.Sprintf("Alert: %s is %s", monitor.Name, alert.Status),
		DedupeKey:   key,
		EventAction: domain.EventActionTrigger,
	}
	e.enrichJobWithServiceContext(ctx, &job, monitor, alert)
	return job
}

// enqueue dual-writes a job: the bus for speed, the outbox for durability.
// If the worker dies before completing it, the outbox poller reclaims the
// stale job. It reports false only when both writes failed.
func (e *Engine) enqueue(ctx context.Context, job *domain.NotificationJob) bool {
	return publishJob(ctx, e.store, e.bus, job)
}

func publishJob(ctx context.Context, s store.Store, b bus.Bus, job *domain.NotificationJob) bool {
	data, err := json.Marshal(job)
	if err != nil {
		return false
	}
	outboxOK := false
	if outbox := s.Outbox(); outbox != nil {
		if err := outbox.Enqueue(ctx, job.ID, string(data)); err != nil {
			slog.Error("escalation: outbox enqueue failed", "alert_id", job.AlertID, "error", err)
		} else {
			outboxOK = true
		}
	}
	busOK := false
	if b != nil {
		if err := b.PublishDurable(ctx, "notify.request", data, job.ID); err != nil {
			slog.Error("escalation: publish notify.request failed", "alert_id", job.AlertID, "error", err)
		} else {
			busOK = true
		}
	}
	return outboxOK || busOK
}

// queuedEvent records one queued delivery on the alert timeline. The
// notifier writes notification_sent or notification_failed when the
// provider call settles.
func (e *Engine) queuedEvent(ctx context.Context, alertID string, step *domain.EscalationStep, target *domain.StepTarget, channel, userID, jobID string, extra map[string]any) {
	detail := map[string]any{}
	for k, v := range extra {
		detail[k] = v
	}
	if jobID != "" {
		detail["job_id"] = jobID
	}
	if step != nil {
		detail["step_id"] = step.ID
		detail["step_position"] = step.Position
	}
	if target != nil {
		detail["target_type"] = string(target.TargetType)
		if target.TargetID != "" {
			detail["target_id"] = target.TargetID
		}
	}
	raw, _ := json.Marshal(detail)
	e.createEvent(ctx, alertID, domain.EventNotificationQueued, channel, userID, raw)
}
