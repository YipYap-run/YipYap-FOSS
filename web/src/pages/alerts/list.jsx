import { useState, useEffect, useMemo } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, Card, SeverityIcon, AlertStatusPill, Tabs, SearchInput, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';

const SEV_MAP = { critical: 'SEV1', warning: 'SEV2', info: 'SEV3' };
const SEV_COLORS = { critical: '#ef4444', warning: '#f59e0b', info: '#3b82f6' };

function SeverityBadge({ severity }) {
  const label = SEV_MAP[severity] || severity;
  const bg = SEV_COLORS[severity] || '#64748b';
  return (
    <span style={{
      display: 'inline-block', padding: '2px 8px', borderRadius: '4px',
      fontSize: '0.7rem', fontWeight: 700, letterSpacing: '0.5px',
      color: '#fff', background: bg, lineHeight: '1.4',
    }}>
      {label}
    </span>
  );
}

function AlertsByDayChart({ alerts }) {
  const data = useMemo(() => {
    const days = {};
    const now = new Date();
    for (let i = 6; i >= 0; i--) {
      const d = new Date(now);
      d.setDate(d.getDate() - i);
      days[d.toISOString().slice(0, 10)] = 0;
    }
    alerts.forEach(a => {
      const day = new Date(a.started_at || a.fired_at || a.created_at).toISOString().slice(0, 10);
      if (day in days) days[day] = (days[day] || 0) + 1;
    });
    return Object.entries(days);
  }, [alerts]);

  const max = Math.max(...data.map(([, v]) => v), 1);

  return (
    <div style={{ display: 'flex', alignItems: 'flex-end', gap: '4px', height: '80px', width: '100%' }}>
      {data.map(([day, count]) => (
        <div key={day} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: '2px' }}>
          <span style={{ fontSize: '0.65rem', color: 'var(--color-text-secondary)' }}>{count || ''}</span>
          <div style={{
            width: '100%', maxWidth: '32px', borderRadius: '3px 3px 0 0',
            background: count > 0 ? '#6366f1' : 'var(--color-border)',
            height: `${Math.max((count / max) * 60, 2)}px`,
          }} />
          <span style={{ fontSize: '0.6rem', color: 'var(--color-text-secondary)' }}>
            {day.slice(5)}
          </span>
        </div>
      ))}
    </div>
  );
}

function StatsCards({ alerts, allAlerts }) {
  const source = allAlerts.length > 0 ? allAlerts : alerts;
  const active = source.filter(a => a.status !== 'resolved');
  const sev1 = active.filter(a => a.severity === 'critical').length;
  const sev2 = active.filter(a => a.severity === 'warning').length;
  const sev3 = active.filter(a => a.severity === 'info').length;

  // MTTA: mean time to acknowledge (for acked/resolved alerts with acked time)
  const ackedAlerts = source.filter(a => a.acknowledged_at && a.started_at);
  const mtta = ackedAlerts.length > 0
    ? ackedAlerts.reduce((sum, a) => sum + (new Date(a.acknowledged_at) - new Date(a.started_at)), 0) / ackedAlerts.length
    : null;

  // MTTR: mean time to resolve
  const resolvedAlerts = source.filter(a => a.resolved_at && a.started_at);
  const mttr = resolvedAlerts.length > 0
    ? resolvedAlerts.reduce((sum, a) => sum + (new Date(a.resolved_at) - new Date(a.started_at)), 0) / resolvedAlerts.length
    : null;

  function fmtDuration(ms) {
    if (ms == null) return '--';
    if (ms < 60000) return `${Math.round(ms / 1000)}s`;
    if (ms < 3600000) return `${Math.round(ms / 60000)}m`;
    return `${(ms / 3600000).toFixed(1)}h`;
  }

  const cards = [
    { label: 'Active', value: active.length, color: 'var(--color-text)' },
    { label: 'SEV1', value: sev1, color: '#ef4444' },
    { label: 'SEV2', value: sev2, color: '#f59e0b' },
    { label: 'SEV3', value: sev3, color: '#3b82f6' },
    { label: 'MTTA', value: fmtDuration(mtta), color: 'var(--color-text-secondary)' },
    { label: 'MTTR', value: fmtDuration(mttr), color: 'var(--color-text-secondary)' },
  ];

  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(100px, 1fr))', gap: '12px', marginBottom: '16px' }}>
      {cards.map(c => (
        <div key={c.label} style={{
          padding: '12px', borderRadius: '8px', textAlign: 'center',
          background: 'var(--color-surface)', border: '1px solid var(--color-border)',
        }}>
          <div style={{ fontSize: '1.5rem', fontWeight: 700, color: c.color }}>{c.value}</div>
          <div style={{ fontSize: '0.75rem', color: 'var(--color-text-secondary)', marginTop: '2px' }}>{c.label}</div>
        </div>
      ))}
    </div>
  );
}

const SEVERITY_OPTIONS = [
  { key: '', label: 'All severities' },
  { key: 'critical', label: 'SEV1 - Critical' },
  { key: 'warning', label: 'SEV2 - Warning' },
  { key: 'info', label: 'SEV3 - Info' },
];

export function AlertListPage() {
  const [alerts, setAlerts] = useState([]);
  const [allAlerts, setAllAlerts] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [tab, setTab] = useState('firing');
  const [search, setSearch] = useState('');
  const [sevFilter, setSevFilter] = useState('');

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get(`/alerts?status=${tab}`);
      setAlerts(data.alerts || data || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  // Load all alerts once for rollup stats.
  useEffect(() => {
    get('/alerts?status=firing').then(d => setAllAlerts(d.alerts || d || [])).catch(() => {});
  }, []);

  useEffect(() => { load(); }, [tab]);

  const tabs = [
    { key: 'firing', label: 'Active' },
    { key: 'acknowledged', label: 'Acknowledged' },
    { key: 'resolved', label: 'Resolved' },
  ];

  const filtered = alerts.filter(a => {
    if (sevFilter && a.severity !== sevFilter) return false;
    if (!search) return true;
    const s = search.toLowerCase();
    return (a.monitor_name || '').toLowerCase().includes(s) ||
           (a.severity || '').toLowerCase().includes(s);
  });

  return (
    <div class="alerts-page">
      <PageHeader title="Alerts" subtitle={`${alerts.length} ${tab} alerts`} />

      {/* Rollup summary */}
      <StatsCards alerts={alerts} allAlerts={allAlerts} />

      <Card style={{ marginBottom: '16px' }}>
        <h3 style={{ margin: '0 0 8px 0', fontSize: '0.875rem' }}>Alerts (last 7 days)</h3>
        <AlertsByDayChart alerts={[...alerts, ...allAlerts]} />
      </Card>

      <Tabs tabs={tabs} active={tab} onChange={setTab} />

      <div class="filters-bar" style={{ display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' }}>
        <SearchInput value={search} onInput={setSearch} placeholder="Search alerts..." />
        <select class="filter-select filter-select-sm" value={sevFilter} onChange={e => setSevFilter(e.target.value)}>
          {SEVERITY_OPTIONS.map(o => <option key={o.key} value={o.key}>{o.label}</option>)}
        </select>
      </div>

      {loading ? <LoadingPage /> : error ? <ErrorMessage error={error} onRetry={load} /> : (
        <div class="alert-list">
          {filtered.length === 0 ? (
            <div class="empty-state">
              <h3>No {tab} alerts</h3>
              <p>{tab === 'firing' ? 'All clear!' : `No ${tab} alerts found`}</p>
            </div>
          ) : (
            filtered.map(a => (
              <a key={a.id} href={`/alerts/${a.id}`} class="alert-card">
                <div class="alert-card-left">
                  <SeverityBadge severity={a.severity} />
                  <SeverityIcon severity={a.severity} />
                  <div class="alert-card-info">
                    <h4>{a.monitor_name || `Monitor ${a.monitor_id}`}</h4>
                    <p class="alert-card-detail">{a.error || a.detail || 'Check failed'}</p>
                  </div>
                </div>
                <div class="alert-card-right">
                  <AlertStatusPill status={a.status} />
                  <span class="alert-card-time">{relativeTime(a.started_at || a.fired_at || a.created_at)}</span>
                </div>
              </a>
            ))
          )}
        </div>
      )}
    </div>
  );
}
