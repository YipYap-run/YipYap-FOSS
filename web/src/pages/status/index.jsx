import { useState, useEffect } from 'preact/hooks';
import { StatusBadge, UptimeBar, LoadingPage, ErrorMessage, formatTime, Card } from '../../components/ui';

// Public status page - no auth required. Uses raw fetch to avoid auth redirect.
const API_BASE = '/api/v1';

async function publicGet(path) {
  const res = await fetch(`${API_BASE}${path}`);
  if (!res.ok) {
    const body = await res.json().catch(() => ({}));
    throw new Error(body.error || `HTTP ${res.status}`);
  }
  return res.json();
}

export function StatusPage({ slug }) {
  const [status, setStatus] = useState(null);
  const [maintenance, setMaintenance] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const [s, m] = await Promise.all([
        publicGet(`/public/${slug}/status`),
        publicGet(`/public/${slug}/maintenance`).catch(() => ({ windows: [] })),
      ]);
      setStatus(s);
      setMaintenance(m.windows || m || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [slug]);

  if (loading) return <LoadingPage />;
  if (error) return (
    <div class="status-page-public">
      <ErrorMessage error={error} onRetry={load} />
    </div>
  );

  const monitors = status?.monitors || status?.services || [];
  const allUp = monitors.every(m => m.status === 'up');
  const activeMaintenanceWindows = maintenance.filter(w => {
    const now = new Date();
    return new Date(w.start_time) <= now && new Date(w.end_time) >= now;
  });

  return (
    <div class="status-page-public">
      <header class="status-header">
        <h1>{status?.title || status?.org_name || 'Status'}</h1>
        <div class={`status-overall ${allUp ? 'all-up' : 'has-issues'}`}>
          {allUp ? 'All Systems Operational' : 'Some Systems Are Experiencing Issues'}
        </div>
      </header>

      {activeMaintenanceWindows.length > 0 && (
        <div class="maintenance-banner">
          {activeMaintenanceWindows.map(w => (
            <div key={w.id} class="maintenance-item">
              <strong>Scheduled Maintenance:</strong> {w.name}
              <span class="text-muted"> (until {formatTime(w.end_time)})</span>
            </div>
          ))}
        </div>
      )}

      <div class="status-monitors">
        {monitors.map(m => (
          <Card key={m.id} class="status-monitor-card">
            <div class="status-monitor-header">
              <span class="status-monitor-name">{m.name}</span>
              <StatusBadge status={m.status || 'unknown'} />
            </div>
            {m.uptime_90d != null && (
              <div class="status-monitor-uptime">
                <span class="uptime-pct">{m.uptime_90d.toFixed(2)}%</span>
                <span class="text-muted">90-day uptime</span>
              </div>
            )}
            {m.uptime_history && (
              <UptimeBar checks={m.uptime_history.map(h => ({ ok: h.ok ?? h.status === 'up' }))} period="90d" />
            )}
          </Card>
        ))}
        {monitors.length === 0 && <p class="text-muted">No public monitors</p>}
      </div>

      <footer class="status-footer">
        <p>Powered by <strong>YipYap</strong></p>
      </footer>
    </div>
  );
}
