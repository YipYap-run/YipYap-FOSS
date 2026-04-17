import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { wsMessages } from '../../api/ws';
import { PageHeader, Card, StatusBadge, SeverityBadge, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';

export function DashboardPage() {
  const [monitors, setMonitors] = useState([]);
  const [alerts, setAlerts] = useState([]);
  const [schedules, setSchedules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const [m, a, s] = await Promise.all([
        get('/monitors'),
        get('/alerts?status=firing&status=acknowledged'),
        get('/schedules').catch(() => []),
      ]);
      setMonitors(m.monitors || m || []);
      setAlerts(a.alerts || a || []);
      setSchedules(s.schedules || s || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  // Refresh on WS messages.
  useEffect(() => {
    if (wsMessages.value.length > 0) {
      const msg = wsMessages.value[0];
      if (msg.type === 'alert.fired' || msg.type === 'alert.resolved' || msg.type === 'monitor.status_changed') {
        load();
      }
    }
  }, [wsMessages.value]);

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  const monitorsByStatus = groupBy(monitors, m => m.status || 'unknown');
  const alertsBySeverity = groupBy(alerts, a => a.severity || 'info');
  const upCount = (monitorsByStatus.up || []).length;
  const downCount = (monitorsByStatus.down || []).length;
  const degradedCount = (monitorsByStatus.degraded || []).length;
  const pendingCount = (monitorsByStatus.unknown || []).length;

  return (
    <div class="dashboard">
      <PageHeader title="Dashboard" subtitle="System overview" />

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
          <div class="stat-value">{alerts.length}</div>
          <div class="stat-label">Active Alerts</div>
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
                <span class="monitor-tile-type">{m.type}</span>
              </a>
            ))}
            {monitors.length === 0 && <p class="text-muted">No monitors configured</p>}
          </div>
        </Card>

        <Card class="dashboard-section">
          <h3>Active Alerts</h3>
          <div class="alert-list-compact">
            {alerts.slice(0, 10).map(a => (
              <a key={a.id} href={`/alerts/${a.id}`} class="alert-row">
                <SeverityBadge severity={a.severity} />
                <span class="alert-row-name">{a.monitor_name || `Monitor ${a.monitor_id}`}</span>
                <span class="alert-row-time">{relativeTime(a.fired_at || a.created_at)}</span>
              </a>
            ))}
            {alerts.length === 0 && <p class="text-muted">No active alerts</p>}
          </div>
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
