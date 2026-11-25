import { useState, useEffect, useRef } from 'preact/hooks';
import { route } from 'preact-router';
import { get, post } from '../../api/client';
import { appMeta, currentUser } from '../../state/auth';
import { PageHeader, Card, StatusBadge, SeverityIcon, AlertStatusPill, LoadingPage, ErrorMessage, formatTime, formatDuration, relativeTime } from '../../components/ui';
import { canWrite, canAckAlert, isOrgAdmin } from '../../state/permissions.mjs';
import { usageLinkHref } from '../settings/billing-view.mjs';
import { safeHref } from '../../utils/url';
import { useLiveRefresh } from '../../state/live';
import { typeLabel as methodTypeLabel } from '../settings/contact-methods.jsx';
import { POLICY_ANCHOR_PREFIX } from '../settings/escalation.jsx';
import { SeverityTag } from './severity.jsx';
import { describeEvent } from './timeline-logic.mjs';

export function AlertDetailPage({ id }) {
  const [alert, setAlert] = useState(null);
  const [timeline, setTimeline] = useState([]);
  const [checks, setChecks] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [actionLoading, setActionLoading] = useState(false);
  // actionError is a failed Ack/Resolve/Create Incident. It is shown inline
  // above the action bar instead of replacing the page, so a 409 ("already
  // resolved by X", "monitor is still down") keeps the alert in view.
  const [actionError, setActionError] = useState(null);
  // actionDone is a short confirmation for screen readers after a
  // successful Ack or Resolve (the button that was pressed goes away).
  const [actionDone, setActionDone] = useState('');
  // liveNote says when the alert changed status because of someone or
  // something else (another responder, the monitor recovering). It is a
  // polite live region and only speaks on a status change, not on every
  // background refresh.
  const [liveNote, setLiveNote] = useState('');
  const statusRef = useRef(null);
  // allowResolveWhileDown mirrors the org setting so Resolve can say up
  // front that it waits for the monitor. The server still decides.
  const [allowResolveWhileDown, setAllowResolveWhileDown] = useState(false);
  const noticeRef = useRef(null);
  const headingRef = useRef(null);

  // quiet skips the full-page spinner and keeps the page on a failed
  // refresh, for refreshes after an action.
  async function load(quiet = false) {
    if (!quiet) {
      setLoading(true);
      setError(null);
    }
    try {
      const [a, t, settings] = await Promise.all([
        get(`/alerts/${id}`),
        get(`/alerts/${id}/timeline`).catch(() => ({ events: [] })),
        get('/org/settings').catch(() => ({})),
      ]);
      if (quiet && statusRef.current && a.status && a.status !== statusRef.current && !actionLoadingRef.current) {
        setLiveNote(liveStatusNote(a));
      }
      statusRef.current = a.status || null;
      setAlert(a);
      setTimeline(t.events || t || []);
      setAllowResolveWhileDown(settings?.allow_resolve_while_down === 'true');

      // Load recent checks for the monitor.
      if (a.monitor_id) {
        const c = await get(`/monitors/${a.monitor_id}/checks`).catch(() => ({ checks: [] }));
        setChecks(c.checks || c || []);
      }
    } catch (err) {
      if (!quiet) setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    setActionError(null);
    setActionDone('');
    setLiveNote('');
    statusRef.current = null;
    load();
  }, [id]);

  // Refresh quietly when this alert or its monitor changes elsewhere
  // (another responder acks, the engine pages the next step, the monitor
  // recovers). Skipped while an action is in flight; runAction reloads.
  const monitorIdRef = useRef(null);
  monitorIdRef.current = alert?.monitor_id || null;
  const actionLoadingRef = useRef(false);
  actionLoadingRef.current = actionLoading;
  useLiveRefresh(() => (actionLoadingRef.current ? null : load(true)), {
    match: (m) => m.alert_id === id || (!!m.monitor_id && m.monitor_id === monitorIdRef.current),
  });

  // Re-render every 15s so "next step in 4m" counts down between refreshes.
  const [, setTick] = useState(0);
  const counting = !!alert?.next_escalation_at || !!alert?.next_page_at;
  useEffect(() => {
    if (!counting) return undefined;
    const t = setInterval(() => setTick((n) => n + 1), 15000);
    return () => clearInterval(t);
  }, [counting]);

  // Move focus to the notice when one appears, so keyboard users land next
  // to its actions instead of on <body> after the pressed button unmounts.
  useEffect(() => {
    if (actionError && noticeRef.current) noticeRef.current.focus();
  }, [actionError]);

  // runAction posts an alert action and refreshes in place. A 409 means the
  // alert moved under us (resolved or acknowledged elsewhere) or the monitor
  // is still failing; the server sends a sentence to show and a code.
  async function runAction(action) {
    setActionLoading(true);
    setActionError(null);
    setActionDone('');
    try {
      await post(`/alerts/${id}/${action}`, {});
      setLiveNote('');
      await load(true);
      setActionDone(action === 'ack' ? 'Alert acknowledged.' : 'Alert resolved.');
      if (headingRef.current) headingRef.current.focus();
    } catch (err) {
      setActionError({
        message: err.message || 'Action failed',
        conflict: err.status === 409,
        code: err.body?.code,
        monitorId: err.body?.monitor_id,
      });
      if (err.status === 409) await load(true);
    } finally {
      setActionLoading(false);
    }
  }

  const handleAck = () => runAction('ack');
  const handleResolve = () => runAction('resolve');

  async function handleCreateIncident() {
    setActionLoading(true);
    setActionError(null);
    try {
      const incident = await post(`/alerts/${id}/create-incident`, {});
      route(`/incidents/${incident.id}`);
    } catch (err) {
      setActionError({ message: err.message || 'Failed to create incident', conflict: err.status === 409 });
    } finally {
      setActionLoading(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={() => load()} />;
  if (!alert) return <ErrorMessage error="Alert not found" />;

  const isActive = alert.status === 'firing' || alert.status === 'acknowledged';
  const role = currentUser.value?.role;
  const writer = canWrite(role);
  const isViewer = role === 'viewer';
  // Viewers may only ack alerts paged or routed to them; the server enforces
  // the same rule from the timeline.
  const ackAllowed = canAckAlert(role, currentUser.value?.id, timeline);
  const showAck = isActive && alert.status === 'firing' && ackAllowed;
  const viewerAckNote = isActive && alert.status === 'firing' && !ackAllowed && role === 'viewer';
  // checks are newest first. Resolve waits for a passing check unless the
  // org allows resolving while down (mirrors the server rule).
  const latestStatus = checks[0] ? (checks[0].status || (checks[0].ok ? 'up' : 'down')) : null;
  const monitorFailing = latestStatus === 'down' || latestStatus === 'degraded';
  const resolveWaits = isActive && monitorFailing && !allowResolveWhileDown;
  const showDownHint = resolveWaits && actionError?.code !== 'monitor_still_down';
  const startTime = alert.started_at || alert.fired_at;
  // A resolved alert lasted from start to resolve; an open one is still
  // running, so it counts up to now.
  const endTime = alert.status === 'resolved' && alert.resolved_at ? alert.resolved_at : null;
  const firingDuration = startTime
    ? (endTime ? new Date(endTime).getTime() : Date.now()) - new Date(startTime).getTime()
    : null;

  return (
    <div class="alert-detail">
      <PageHeader title="Alert Detail" />

      {/* Hero section */}
      <div class={`alert-hero severity-${alert.severity}`}>
        <SeverityIcon severity={alert.severity} />
        <div style="flex: 1; min-width: 0">
          <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 4px; flex-wrap: wrap">
            <h2 ref={headingRef} tabIndex={-1} style="font-family: var(--font-display); font-size: 1.25rem; font-weight: 700; margin: 0; outline-offset: 2px">
              {alert.monitor_name || `Monitor ${alert.monitor_id}`}
            </h2>
            <SeverityTag severity={alert.severity} withName large />
            <AlertStatusPill status={alert.status} />
          </div>
          <p style="font-size: 0.8125rem; opacity: 0.8; margin: 0">
            {alert.monitor_type || 'Monitor'}
            {alert.endpoint && ` · ${alert.endpoint}`}
            {alert.service_name && ` · ${alert.service_name}`}
          </p>
        </div>
        {alert.runbook_url && (
          safeHref(alert.runbook_url)
            ? <a href={safeHref(alert.runbook_url)} target="_blank" rel="noopener" class="btn btn-sm" style="flex-shrink: 0">Runbook</a>
            : <span class="btn btn-sm" style="flex-shrink: 0">Runbook</span>
        )}
      </div>

      <p class="sr-only" role="status">{actionDone}</p>
      {/* Always rendered so screen readers have the live region before the
          text arrives; visible only while it has something to say. */}
      <p class={liveNote ? 'form-help' : 'sr-only'} role="status" style={liveNote ? 'margin: 0 0 1rem 0' : undefined}>{liveNote}</p>

      {actionError && (
        <AlertActionNotice
          noticeRef={noticeRef}
          error={actionError}
          canAck={alert.status === 'firing' && !isViewer}
          busy={actionLoading}
          onAck={handleAck}
          onDismiss={() => setActionError(null)}
        />
      )}

      {/* Action bar: Create Incident on left, Ack/Resolve/Back on right */}
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; flex-wrap: wrap; gap: 8px;">
        <div style="display: flex; gap: 8px; align-items: center;">
          {appMeta.value?.edition !== 'foss' && (
            alert.incident_id ? (
              <a href={`/incidents/${alert.incident_id}`} class="btn btn-outline">
                View Incident
              </a>
            ) : (
              isActive && writer && (
                <button class="btn btn-primary" onClick={handleCreateIncident} disabled={actionLoading}>
                  Create Incident
                </button>
              )
            )
          )}
        </div>
        <div style="display: flex; gap: 8px; align-items: center; flex-wrap: wrap;">
          {viewerAckNote && (
            <span class="text-muted" style="font-size: 0.8125rem">
              Viewers can acknowledge only alerts paged to them.
            </span>
          )}
          {showAck && (
            <button class="btn btn-sm btn-warning" onClick={handleAck} disabled={actionLoading}>
              Acknowledge
            </button>
          )}
          {isActive && writer && (
            // aria-disabled, not disabled: the button stays focusable and a
            // press still asks the server, which has the current check.
            <button class="btn btn-sm" onClick={handleResolve} disabled={actionLoading}
              aria-disabled={resolveWaits ? 'true' : undefined}
              aria-describedby={showDownHint ? 'resolve-waits-hint' : undefined}
              style={resolveWaits
                ? 'opacity: 0.6;'
                : 'color: var(--color-up); border-color: var(--color-up);'}>
              Resolve
            </button>
          )}
          <a href="/alerts" class="btn btn-sm">Back</a>
        </div>
      </div>

      {showDownHint && (
        <p id="resolve-waits-hint" class="text-muted" style="font-size: 0.8125rem; margin: -0.5rem 0 1rem 0">
          The monitor's latest check is {latestStatus}, so Resolve waits until it recovers.
          {alert.status === 'firing'
            ? ' Acknowledge to stop paging, or mute the monitor if you expect it to stay down.'
            : ' Paging already stopped. Mute the monitor if you expect it to stay down.'}
          {' '}The alert resolves on its own when the monitor passes a check.
        </p>
      )}

      <div class="alert-detail-body">

        {/* Error detail */}
        <Card>
          <h3>What Failed</h3>
          <p class="alert-error">{alert.error || 'Check returned failure status'}</p>
        </Card>

        {/* Duration */}
        {firingDuration > 0 && (
          <Card>
            <h3>Duration</h3>
            <p class="alert-duration">
              {endTime ? 'Lasted ' : alert.status === 'acknowledged' ? 'Open for ' : 'Firing for '}
              <strong>{formatDuration(firingDuration)}</strong>
            </p>
            <p class="text-muted">Started {formatTime(startTime)}</p>
            {alert.acknowledged_at && <p class="text-muted">Acknowledged {formatTime(alert.acknowledged_at)}</p>}
            {endTime && <p class="text-muted">Resolved {formatTime(endTime)}</p>}
          </Card>
        )}

        {/* Escalation status */}
        {alert.escalation_policy_id && <EscalationCard alert={alert} />}

        {/* Recent check results */}
        {checks.length > 0 && (
          <Card>
            <h3>Recent Checks</h3>
            <div class="check-list-compact">
              {checks.slice(0, 5).map((c, i) => (
                <div key={i} class="check-row-compact">
                  <StatusBadge status={c.status || (c.ok ? 'up' : 'down')} />
                  <span>{c.latency_ms != null ? `${c.latency_ms}ms` : '-'}</span>
                  <span class="text-muted">{relativeTime(c.checked_at || c.created_at)}</span>
                  {c.error && <span class="check-error">{c.error}</span>}
                </div>
              ))}
            </div>
          </Card>
        )}

        {/* Notification timeline */}
        {timeline.length > 0 && (
          <Card>
            <h3>Timeline</h3>
            <div class="timeline">
              {timeline.map((evt, i) => (
                <TimelineEvent key={i} evt={evt} formatTime={formatTime} />
              ))}
            </div>
          </Card>
        )}

        {/* Monitor config summary */}
        {alert.monitor_id && (
          <Card>
            <h3>Monitor</h3>
            <a href={`/monitors/${alert.monitor_id}`} class="btn btn-sm">View Monitor</a>
          </Card>
        )}
      </div>

    </div>
  );
}

// untilTime is "in 4m" for a future time, "now" once it has passed.
function untilTime(ts) {
  const diff = new Date(ts).getTime() - Date.now();
  if (!Number.isFinite(diff)) return '';
  if (diff <= 0) return 'now';
  return `in ${formatDuration(diff)}`;
}

// clockTime is the local wall-clock time ("14:32"), with the date when it
// is not today, so the countdown also has a fixed time next to it.
function clockTime(ts) {
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return '';
  const time = d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit' });
  if (d.toDateString() === new Date().toDateString()) return time;
  return `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}`;
}

// When renders "in 4m (at 14:32)" with the full timestamp in the markup.
function When({ ts }) {
  const at = clockTime(ts);
  return (
    <time class="esc-when" dateTime={ts} title={formatTime(ts)}>
      {untilTime(ts)}{at ? ` (at ${at})` : ''}
    </time>
  );
}

// liveStatusNote is the sentence shown when a background refresh finds the
// alert in a new status.
function liveStatusNote(a) {
  if (a.status === 'acknowledged') return 'This alert was just acknowledged elsewhere. Paging has stopped.';
  if (a.status === 'resolved') return 'This alert was just resolved.';
  if (a.status === 'firing') return 'This alert is firing again.';
  return `This alert is now ${a.status}.`;
}

const NEXT_ACTION_TEXT = {
  page_next_targets: 'Pages the next target in this step',
  repeat_step: 'Repeats this step',
  next_step: 'Escalates to the next step',
  restart_policy: 'Starts the policy again from step 1',
  stop: 'Stops escalating after this step',
};

const ESCALATION_STATE_TEXT = {
  held: 'Paging is held by a maintenance window. If the monitor is still down when the window ends, this step pages then.',
  stopped: 'Escalation stopped: this step is set to stop escalating. The alert stays open until someone acknowledges or resolves it.',
  exhausted: 'Every step has paged and the policy does not repeat. Nobody else will be paged.',
  acknowledged: 'Acknowledged, so escalation has stopped.',
  resolved: 'Resolved, so escalation has stopped.',
  none: 'This alert is not being escalated. The policy was probably attached to the monitor after the alert opened, or the monitor was muted.',
};

// EscalationCard shows where the alert stands in its escalation policy,
// from the escalation fields alert Get returns (see alertEscalationView in
// internal/api/handlers/alerts.go).
function EscalationCard({ alert }) {
  const state = alert.escalation_state || 'none';
  const step = alert.current_step;
  const count = alert.step_count;
  const repeat = Number(alert.escalation_repeat) || 0;
  const loop = Number(alert.escalation_loop) || 0;
  return (
    <Card>
      <h3>Escalation</h3>
      <div class="escalation-info">
        <p>
          <a href={`/settings/escalation#${POLICY_ANCHOR_PREFIX}${encodeURIComponent(alert.escalation_policy_id)}`}>
            {alert.escalation_policy_name || 'Escalation policy'}
          </a>
          {step ? ` · Step ${step}${count ? ` of ${count}` : ''}` : ''}
          {repeat > 0 ? ` · repeat ${repeat}` : ''}
          {loop > 0 ? ` · pass ${loop + 1}` : ''}
        </p>
        {step && count > 1 && (
          // Decorative: the "Step N of M" text above says the same thing.
          <ol class="esc-progress" aria-hidden="true">
            {Array.from({ length: Math.min(count, 20) }, (_, i) => (
              <li key={i} class={i + 1 < step ? 'done' : i + 1 === step ? 'current' : ''} />
            ))}
          </ol>
        )}
        {state === 'active' && alert.next_escalation_at && (
          <p class="text-muted">
            {NEXT_ACTION_TEXT[alert.next_escalation_action] || 'Next escalation'}{' '}
            <When ts={alert.next_escalation_at} />
            {alert.next_escalation_action === 'stop' ? '.' : ' unless someone acknowledges.'}
          </p>
        )}
        {state !== 'active' && ESCALATION_STATE_TEXT[state] && (
          <p class="text-muted">{ESCALATION_STATE_TEXT[state]}</p>
        )}
        {alert.next_page_at && (state === 'active' || state === 'stopped' || state === 'exhausted') && (
          <p class="text-muted">
            A delayed notification rule pages again <When ts={alert.next_page_at} />.
          </p>
        )}
      </div>
    </Card>
  );
}

// AlertActionNotice shows a failed alert action inline. A 409 is a state
// conflict (someone else moved the alert, or the monitor is still down), so
// it reads as a notice with next steps rather than an error.
function AlertActionNotice({ noticeRef, error, canAck, busy, onAck, onDismiss }) {
  const stillDown = error.code === 'monitor_still_down';
  return (
    <div ref={noticeRef} tabIndex={-1} role="alert"
      class={error.conflict ? 'form-warning alert-action-notice' : 'form-error alert-action-notice'}>
      <p style="margin: 0">{error.message}</p>
      <div class="alert-action-notice-actions">
        {stillDown && canAck && (
          <button type="button" class="btn btn-sm btn-warning" onClick={onAck} disabled={busy}>
            Acknowledge
          </button>
        )}
        {stillDown && error.monitorId && (
          <a href={`/monitors/${encodeURIComponent(error.monitorId)}`} class="btn btn-sm">
            Go to monitor to mute
          </a>
        )}
        <button type="button" class="btn btn-sm" onClick={onDismiss}>Dismiss</button>
      </div>
    </div>
  );
}

/* ─── Timeline event rendering ─── */

const DELIVERY_TYPES = new Set(['notification_queued', 'notification_sent', 'notification_failed']);

const REPLY_TYPE_BY_EVENT = {
  'reply.claimed': { label: 'Claimed', className: 'timeline-event-reply timeline-event-claimed' },
  'reply.enriched': { label: 'Enriched', className: 'timeline-event-reply timeline-event-enriched' },
  'reply.linked': { label: 'Linked', className: 'timeline-event-reply timeline-event-linked' },
};

function parseDetail(detail) {
  if (!detail) return null;
  if (typeof detail === 'object') return detail;
  if (typeof detail === 'string') {
    try { return JSON.parse(detail); } catch { return null; }
  }
  return null;
}

function TimelineEvent({ evt, formatTime }) {
  const rawType = evt.event_type || evt.action || evt.type || evt.event || '';
  const typeStr = typeof rawType === 'string' ? rawType : String(rawType);
  const replyMeta = REPLY_TYPE_BY_EVENT[typeStr];
  const detail = parseDetail(evt.detail);

  if (replyMeta) {
    return (
      <div class={`timeline-event ${replyMeta.className}`}>
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">{replyMeta.label}</span>
            {' '}{renderReplyHeadline(typeStr, detail)}
          </p>
          {renderReplyBody(typeStr, detail)}
          <p class="timeline-meta">
            {renderReplyAttribution(detail)} {formatTime(evt.created_at || evt.timestamp)}
          </p>
        </div>
      </div>
    );
  }

  // A channel the engine skipped (for example an SMS or voice quota was
  // reached). The server writes a readable message in the detail.
  if (typeStr === 'notify_skipped') {
    const message = detail && typeof detail.message === 'string' ? detail.message : 'Channel skipped';
    const channelName = detail && typeof detail.channel_name === 'string' ? detail.channel_name : '';
    const isQuota = detail && typeof detail.reason === 'string' && detail.reason.endsWith('_quota');
    const role = currentUser.value?.role;
    const canSeeUsage = appMeta.value?.billing_enabled && isOrgAdmin(role);
    return (
      <div class="timeline-event timeline-event-skipped">
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">Skipped</span>{' '}{message}
          </p>
          <p class="timeline-detail">
            {channelName ? `Channel: ${channelName}. ` : ''}
            The other channels in this step still paged. A step with no other channel sends
            nothing until the quota resets.
            {isQuota && canSeeUsage && <>{' '}<a href={usageLinkHref(appMeta.value?.plan)}>View SMS and voice usage</a>{' or '}<a href="/settings/escalation">edit escalation steps</a>.</>}
            {isQuota && !canSeeUsage && ' An owner or admin can check usage in Billing.'}
          </p>
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  if (typeStr === 'notify_failed') {
    return (
      <div class="timeline-event timeline-event-failed">
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">Page not sent</span>
            {' '}{notifyFailedTarget(detail, evt)}
          </p>
          {detail && detail.reason && <p class="timeline-note">{String(detail.reason)}</p>}
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  if (typeStr === 'notified') {
    const n = notifiedText(detail, evt);
    return (
      <div class="timeline-event">
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">{n.pill}</span>
            {' '}{n.headline}
          </p>
          {n.note && <p class="timeline-note">{n.note}</p>}
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  if (typeStr === 'escalation_stopped') {
    const pos = detail && detail.step_position != null ? Number(detail.step_position) + 1 : null;
    return (
      <div class="timeline-event timeline-event-held">
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">Stopped</span>
            {' '}{pos ? `Escalation stopped after step ${pos}` : 'Escalation stopped'}
          </p>
          <p class="timeline-note">
            This step is set to stop escalating. Nobody else is paged. The alert stays open until someone acknowledges or resolves it.
          </p>
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  if (typeStr === 'suppressed') {
    const s = suppressedText(detail);
    return (
      <div class="timeline-event timeline-event-held">
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            <span class="timeline-pill">{s.pill}</span>
            {' '}{s.headline}
          </p>
          {s.note && <p class="timeline-note">{s.note}</p>}
          {s.maintenance && (
            <p class="timeline-note">
              <a href={s.windowId ? `/settings/maintenance#mw-${encodeURIComponent(s.windowId)}` : '/settings/maintenance'}>
                {s.windowId ? 'View the maintenance window' : 'View maintenance windows'}
              </a>
            </p>
          )}
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  if (DELIVERY_TYPES.has(typeStr)) {
    const d = describeEvent(evt);
    return (
      <div class={`timeline-event timeline-event-delivery timeline-tone-${d.tone}`}>
        <div class="timeline-dot" />
        <div class="timeline-content">
          <p class="timeline-action">
            {d.pill && <span class="timeline-pill">{d.pill}</span>}
            {d.pill ? ' ' : ''}{d.label}
          </p>
          {d.reason && <p class="timeline-note">{d.reason}</p>}
          {d.channelHref && (
            <p class="timeline-note">
              <a href={d.channelHref}>
                View channel<span class="sr-only"> {detail && detail.channel_name ? String(detail.channel_name) : ''}</span>
              </a>
            </p>
          )}
          {d.actionHref && <p class="timeline-note"><a href={d.actionHref}>{d.actionText}</a></p>}
          <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
        </div>
      </div>
    );
  }

  const g = genericEventText(typeStr, detail, evt);
  return (
    <div class="timeline-event">
      <div class="timeline-dot" />
      <div class="timeline-content">
        <p class="timeline-action">
          <span class="timeline-pill">{g.pill}</span>
          {g.headline ? ` ${g.headline}` : ''}
        </p>
        {g.note && <p class="timeline-note">{g.note}</p>}
        <p class="timeline-meta">{formatTime(evt.created_at || evt.timestamp)}</p>
      </div>
    </div>
  );
}

// Human labels for the remaining event types. Unknown types fall back to
// the type name with dots and underscores turned into spaces.
const EVENT_PILLS = {
  triggered: 'Opened',
  ack: 'Acknowledged',
  ack_with_context: 'Acknowledged',
  escalated: 'Escalated',
  escalated_reply: 'Escalated',
  resolved: 'Resolved',
  duplicated: 'Duplicate',
  ownership_transferred: 'Owner changed',
  remediation_started: 'Remediation',
  remediation_result: 'Remediation',
  routed: 'Routed',
  status_page_synced: 'Status page',
};

function genericEventText(type, detail, evt) {
  const who = evt.target_user_name ? String(evt.target_user_name) : '';
  const via = evt.channel ? ` via ${methodTypeLabel(String(evt.channel))}` : '';
  const pill = EVENT_PILLS[type] || type.replace(/[._]/g, ' ');
  const str = (v) => (v == null ? '' : String(v));
  switch (type) {
    case 'triggered':
      return { pill, headline: 'The monitor check failed and this alert opened.' };
    case 'ack': {
      const viaAck = detail && detail.via === 'link'
        ? ` via a signed link${detail.channel_name ? ` from ${String(detail.channel_name)}` : ''}`
        : detail && detail.via === 'voice' ? ' by phone call' : via;
      return { pill, headline: who ? `by ${who}${viaAck}` : (viaAck ? viaAck.trim() : '') };
    }
    case 'ack_with_context':
      return { pill, headline: who ? `by ${who}${via}` : '', note: [str(detail?.reason_code), str(detail?.note)].filter(Boolean).join(': ') };
    case 'escalated': {
      const pos = detail && detail.step_position != null ? Number(detail.step_position) + 1 : null;
      const loop = detail ? Number(detail.loop) || 0 : 0;
      return {
        pill,
        headline: pos ? `to step ${pos}` : 'to the next step',
        note: loop > 0 && pos === 1 ? `The policy started again (pass ${loop + 1}).` : '',
      };
    }
    case 'resolved':
      return { pill, headline: who ? `by ${who}${via}` : 'automatically when the monitor recovered' };
    case 'duplicated':
      return { pill, headline: detail?.duplicate_of ? `of alert ${str(detail.duplicate_of)}` : '', note: str(detail?.reason) };
    case 'ownership_transferred':
      return { pill, headline: detail?.owner_id ? `to ${str(detail.owner_kind)} ${str(detail.owner_id)}`.trim() : '', note: str(detail?.reason) };
    default:
      return { pill, headline: who ? `${who}${via}` : '', note: detail && typeof detail.reason === 'string' ? detail.reason : '' };
  }
}

const NOTIFY_TARGET_LABELS = {
  on_call_primary: 'on-call primary',
  on_call_secondary: 'on-call secondary',
  user: 'user',
  team: 'team',
  channel: 'channel',
};

// suppressedText describes a "suppressed" event. The escalation engine
// writes one when a maintenance window holds paging and another when the
// window ends with the monitor up; a suppression reply writes its own
// reason and duration.
function suppressedText(detail) {
  const reason = detail && typeof detail.reason === 'string' ? detail.reason : '';
  const windowId = detail && typeof detail.window_id === 'string' ? detail.window_id : '';
  if (reason === 'maintenance_window') {
    return {
      pill: 'Held',
      headline: 'Paging paused by a maintenance window',
      note: 'If the monitor is still down when the window ends, this alert pages then.',
      maintenance: true,
      windowId,
    };
  }
  if (reason === 'maintenance_ended_monitor_up') {
    return {
      pill: 'Not paged',
      headline: 'Maintenance ended with the monitor up',
      note: 'Nobody was paged. A new failure while this alert is open pages as usual.',
      maintenance: true,
      windowId,
    };
  }
  const secs = detail && Number(detail.duration_seconds);
  const mins = Number.isFinite(secs) && secs > 0 ? Math.round(secs / 60) : 0;
  return {
    pill: 'Suppressed',
    headline: mins ? `for ${mins} minute${mins === 1 ? '' : 's'}` : '',
    note: reason,
    maintenance: false,
  };
}

// Short "who we tried to page" text for a notify_failed event.
function notifyFailedTarget(detail, evt) {
  const parts = [];
  if (detail && detail.step_position != null) parts.push(`step ${Number(detail.step_position) + 1}`);
  if (detail && detail.target_type) parts.push(NOTIFY_TARGET_LABELS[detail.target_type] || String(detail.target_type));
  if (evt.target_user_name) parts.push(String(evt.target_user_name));
  if (detail && detail.contact_method_type) parts.push(`by ${methodTypeLabel(String(detail.contact_method_type))}`);
  else if (evt.channel_name) parts.push(`to ${evt.channel_name}`);
  else if (evt.channel) parts.push(`via ${methodTypeLabel(String(evt.channel))}`);
  return parts.join(', ');
}

// notifiedText describes a "notified" event. The escalation engine writes
// one per person per contact method (target_user_id plus
// detail.contact_method_type) and one per step channel message
// (detail.channel_id, no person). Older events carry only the channel type.
function notifiedText(detail, evt) {
  const where = [];
  if (detail && detail.step_position != null) where.push(`Step ${Number(detail.step_position) + 1}`);
  if (detail && detail.target_type) where.push(NOTIFY_TARGET_LABELS[detail.target_type] || String(detail.target_type));
  const delay = detail ? Number(detail.delay_minutes) : 0;
  if (Number.isFinite(delay) && delay > 0) where.push(`notification rule after ${delay} minute${delay === 1 ? '' : 's'}`);
  const note = where.join(', ');

  if (detail && detail.contact_method_type) {
    const who = evt.target_user_name || 'a team member';
    return { pill: 'Paged', headline: `${who} by ${methodTypeLabel(String(detail.contact_method_type))}`, note };
  }
  if (detail && detail.channel_id) {
    const name = evt.channel_name || (evt.channel ? `a ${methodTypeLabel(String(evt.channel))} channel` : 'a channel');
    return { pill: 'Posted', headline: `to ${name}`, note };
  }
  const who = evt.target_user_name ? ` ${evt.target_user_name}` : '';
  return { pill: 'Notified', headline: `${who}${evt.channel ? ` via ${methodTypeLabel(String(evt.channel))}` : ''}`.trim(), note };
}

function renderReplyHeadline(type, detail) {
  if (!detail) return '';
  if (type === 'reply.claimed') {
    return `claimed by ${detail.who || 'unknown'}`;
  }
  if (type === 'reply.enriched') {
    const n = Array.isArray(detail.attachments) ? detail.attachments.length : 0;
    return `context added (${n} attachment${n === 1 ? '' : 's'})`;
  }
  if (type === 'reply.linked') {
    const kind = detail.kind || 'external';
    const title = detail.title || detail.id || '';
    return `linked to ${kind}${title ? `: ${title}` : ''}`;
  }
  return '';
}

function renderReplyBody(type, detail) {
  if (!detail) return null;
  if (type === 'reply.claimed' && detail.note) {
    return <p class="timeline-note">{String(detail.note)}</p>;
  }
  if (type === 'reply.enriched' && Array.isArray(detail.attachments)) {
    return (
      <ul class="timeline-attachments">
        {detail.attachments.slice(0, 20).map((a, i) => (
          <li key={i}>
            {a.href ? (
              <a href={safeHref(a.href)} target="_blank" rel="noopener noreferrer">
                {a.label || a.href}
              </a>
            ) : (
              <span>{a.label || '(untitled)'}</span>
            )}
            {a.preview ? <span class="timeline-preview">: {String(a.preview)}</span> : null}
          </li>
        ))}
      </ul>
    );
  }
  if (type === 'reply.linked' && detail.url) {
    return (
      <p class="timeline-link">
        <a href={safeHref(detail.url)} target="_blank" rel="noopener noreferrer">
          {detail.title || detail.id || detail.url}
        </a>
      </p>
    );
  }
  return null;
}

function renderReplyAttribution(detail) {
  if (!detail) return '';
  if (detail.sub) return `verified sub: ${detail.sub}`;
  return '';
}
