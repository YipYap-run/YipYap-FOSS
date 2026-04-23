import { useState, useEffect } from 'preact/hooks';
import { route } from 'preact-router';
import { get, post } from '../../api/client';
import { appMeta, currentUser } from '../../state/auth';
import { PageHeader, Card, StatusBadge, SeverityIcon, AlertStatusPill, LoadingPage, ErrorMessage, formatTime, formatDuration, relativeTime } from '../../components/ui';
import { safeHref } from '../../utils/url';

export function AlertDetailPage({ id }) {
  const [alert, setAlert] = useState(null);
  const [timeline, setTimeline] = useState([]);
  const [checks, setChecks] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [actionLoading, setActionLoading] = useState(false);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const [a, t] = await Promise.all([
        get(`/alerts/${id}`),
        get(`/alerts/${id}/timeline`).catch(() => ({ events: [] })),
      ]);
      setAlert(a);
      setTimeline(t.events || t || []);

      // Load recent checks for the monitor.
      if (a.monitor_id) {
        const c = await get(`/monitors/${a.monitor_id}/checks`).catch(() => ({ checks: [] }));
        setChecks(c.checks || c || []);
      }
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [id]);

  async function handleAck() {
    setActionLoading(true);
    try {
      await post(`/alerts/${id}/ack`, {});
      await load();
    } catch (err) {
      setError(err);
    } finally {
      setActionLoading(false);
    }
  }

  async function handleResolve() {
    setActionLoading(true);
    try {
      await post(`/alerts/${id}/resolve`, {});
      await load();
    } catch (err) {
      setError(err);
    } finally {
      setActionLoading(false);
    }
  }

  async function handleCreateIncident() {
    setActionLoading(true);
    try {
      const incident = await post(`/alerts/${id}/create-incident`, {});
      route(`/incidents/${incident.id}`);
    } catch (err) {
      setError(err);
    } finally {
      setActionLoading(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;
  if (!alert) return <ErrorMessage error="Alert not found" />;

  const isActive = alert.status === 'firing' || alert.status === 'acknowledged';
  const startTime = alert.started_at || alert.fired_at;
  const firingDuration = startTime
    ? Date.now() - new Date(startTime).getTime()
    : null;

  return (
    <div class="alert-detail">
      <PageHeader title="Alert Detail" />

      {/* Hero section */}
      <div class={`alert-hero severity-${alert.severity}`}>
        <SeverityIcon severity={alert.severity} />
        <div style="flex: 1; min-width: 0">
          <div style="display: flex; align-items: center; gap: 10px; margin-bottom: 4px; flex-wrap: wrap">
            <h2 style="font-family: var(--font-display); font-size: 1.25rem; font-weight: 700; margin: 0">
              {alert.monitor_name || `Monitor ${alert.monitor_id}`}
            </h2>
            <span style={{
              display: 'inline-block', padding: '2px 10px', borderRadius: '4px',
              fontSize: '0.75rem', fontWeight: 700, letterSpacing: '0.5px', color: '#fff',
              background: alert.severity === 'critical' ? '#ef4444' : alert.severity === 'warning' ? '#f59e0b' : '#3b82f6',
            }}>
              {alert.severity === 'critical' ? 'SEV1' : alert.severity === 'warning' ? 'SEV2' : 'SEV3'}
              {' - '}{alert.severity}
            </span>
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

      {/* Action bar: Create Incident on left, Ack/Resolve/Back on right */}
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 1rem; flex-wrap: wrap; gap: 8px;">
        <div style="display: flex; gap: 8px; align-items: center;">
          {appMeta.value?.edition !== 'foss' && (
            alert.incident_id ? (
              <a href={`/incidents/${alert.incident_id}`} class="btn btn-outline">
                View Incident
              </a>
            ) : (
              isActive && currentUser.value?.role !== 'viewer' && (
                <button class="btn btn-primary" onClick={handleCreateIncident} disabled={actionLoading}>
                  Create Incident
                </button>
              )
            )
          )}
        </div>
        <div style="display: flex; gap: 8px; align-items: center;">
          {isActive && alert.status === 'firing' && (
            <button class="btn btn-sm btn-warning" onClick={handleAck} disabled={actionLoading}>
              Acknowledge
            </button>
          )}
          {isActive && (
            <button class="btn btn-sm" onClick={handleResolve} disabled={actionLoading}
              style="color: var(--color-up); border-color: var(--color-up);">
              Resolve
            </button>
          )}
          <a href="/alerts" class="btn btn-sm">Back</a>
        </div>
      </div>

      <div class="alert-detail-body">

        {/* Error detail */}
        <Card>
          <h3>What Failed</h3>
          <p class="alert-error">{alert.error || 'Check returned failure status'}</p>
        </Card>

        {/* Duration */}
        {firingDuration && (
          <Card>
            <h3>Duration</h3>
            <p class="alert-duration">
              Firing for <strong>{formatDuration(firingDuration)}</strong>
            </p>
            <p class="text-muted">Started {formatTime(startTime)}</p>
          </Card>
        )}

        {/* Escalation status */}
        {alert.escalation_policy_id && (
          <Card>
            <h3>Escalation</h3>
            <div class="escalation-info">
              <p>Step {alert.current_step || 1}</p>
              {alert.next_escalation_at && (
                <p class="text-muted">
                  Next escalation {relativeTime(alert.next_escalation_at)}
                </p>
              )}
            </div>
          </Card>
        )}

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
                <div key={i} class="timeline-event">
                  <div class="timeline-dot" />
                  <div class="timeline-content">
                    <p class="timeline-action">{evt.action || evt.type || evt.event}</p>
                    <p class="timeline-meta">
                      {evt.user_email || evt.user || ''} {formatTime(evt.created_at || evt.timestamp)}
                    </p>
                  </div>
                </div>
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
