import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { useLiveRefresh } from '../../state/live';
import { PageHeader, Card, SeverityBadge, AlertStatusPill, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';
import { countLabel } from '../alerts/list.jsx';

// The dashboard asks for at most this many open alerts; a full page shows
// as "200+" so the count does not read as exact.
const OPEN_ALERT_CAP = 200;
const STATUS_TEXT = { up: 'up', down: 'down', degraded: 'degraded', paused: 'paused', unknown: 'pending' };

export function DashboardPage() {
  const [monitors, setMonitors] = useState([]);
  const [alerts, setAlerts] = useState([]);
  const [schedules, setSchedules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // quiet is a background refresh after a live event: no full-page
  // spinner, and a failed refresh keeps what is on screen.
  async function load(quiet = false) {
    if (!quiet) {
      setLoading(true);
      setError(null);
    }
    try {
      const [m, a, s] = await Promise.all([
        get('/monitors'),
        get(`/alerts?status=firing&status=acknowledged&limit=${OPEN_ALERT_CAP}`),
        get('/schedules').catch(() => []),
      ]);
      setMonitors(m.monitors || m || []);
      setAlerts(a.alerts || a || []);
      setSchedules(s.schedules || s || []);
    } catch (err) {
      if (!quiet) setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  // Every live frame (monitor down/up, alert opened, acked, resolved,
  // escalated) can change the counts here, so any frame refreshes.
  useLiveRefresh(() => load(true));

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={() => load()} />;

  const monitorsByStatus = groupBy(monitors, m => m.status || 'unknown');
  const alertsBySeverity = groupBy(alerts, a => a.severity || 'info');
  const upCount = (monitorsByStatus.up || []).length;
  const downCount = (monitorsByStatus.down || []).length;
  const degradedCount = (monitorsByStatus.degraded || []).length;
  const pendingCount = (monitorsByStatus.unknown || []).length;

  return (
    <div class="dashboard">
      <PageHeader title="Dashboard" subtitle="Your monitors, alerts, and on-call at a glance" />

      <div class="stat-grid">
        <Card class="stat-card stat-up">
          <div class="stat-value">{upCount}</div>
          <div class="stat-label">Monitors Up</div>
        </Card>
        <Card class="stat-card stat-down">
          <div class="stat-value">{downCount}</div>
          <div class="stat-label">Monitors Down</div>
        </Card>
        <Card class="stat-card stat-degraded">
          <div class="stat-value">{degradedCount}</div>
          <div class="stat-label">Monitors Degraded</div>
        </Card>
        <Card class="stat-card stat-alerts">
          <div class="stat-value">{countLabel(alerts.length, OPEN_ALERT_CAP)}</div>
          <div class="stat-label">Open Alerts</div>
        </Card>
        {pendingCount > 0 && (
          <Card class="stat-card stat-pending">
            <div class="stat-value">{pendingCount}</div>
            <div class="stat-label">Pending</div>
          </Card>
        )}
      </div>

      <div class="dashboard-grid">
        <Card class="dashboard-section">
          <h3>Monitor Status</h3>
          <div class="monitor-grid">
            {monitors.map(m => (
              <a key={m.id} href={`/monitors/${m.id}`} class={`monitor-tile status-${m.status || 'unknown'}`}>
                <span class="monitor-tile-name">{m.name}</span>
                <span class="monitor-tile-type">
                  {m.type}
                  <span class="sr-only">, {STATUS_TEXT[m.status || 'unknown'] || m.status}</span>
                </span>
              </a>
            ))}
            {monitors.length === 0 && <p class="text-muted">No monitors configured</p>}
          </div>
        </Card>

        <Card class="dashboard-section">
          <h3>Open Alerts</h3>
          <div class="alert-list-compact">
            {alerts.slice(0, 10).map(a => (
              <a key={a.id} href={`/alerts/${a.id}`} class="alert-row">
                <SeverityBadge severity={a.severity} />
                <span class="alert-row-name">{a.monitor_name || `Monitor ${a.monitor_id}`}</span>
                {a.status === 'acknowledged' && <AlertStatusPill status={a.status} />}
                <span class="alert-row-time">{relativeTime(a.started_at)}</span>
              </a>
            ))}
            {alerts.length === 0 && <p class="text-muted">No open alerts. Nothing is firing.</p>}
          </div>
          {alerts.length > 10 && (
            <a href="/alerts" class="alert-list-more">
              View all {countLabel(alerts.length, OPEN_ALERT_CAP)} open alerts
            </a>
          )}
        </Card>

        <Card class="dashboard-section">
          <h3>On-Call Now</h3>
          <div class="oncall-compact">
            {schedules.map(s => (
              <div key={s.id} class="oncall-row">
                <span class="oncall-team">{s.name}</span>
                <span class="oncall-user">{s.current_on_call || '-'}</span>
              </div>
            ))}
            {schedules.length === 0 && <p class="text-muted">No schedules configured</p>}
          </div>
        </Card>
      </div>
    </div>
  );
}

function groupBy(arr, fn) {
  return arr.reduce((acc, item) => {
    const key = fn(item);
    (acc[key] = acc[key] || []).push(item);
    return acc;
  }, {});
}
