import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, Card, SeverityIcon, AlertStatusPill, Tabs, SearchInput, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';

export function AlertListPage() {
  const [alerts, setAlerts] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [tab, setTab] = useState('firing');
  const [search, setSearch] = useState('');

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

  useEffect(() => { load(); }, [tab]);

  const tabs = [
    { key: 'firing', label: 'Active' },
    { key: 'acknowledged', label: 'Acknowledged' },
    { key: 'resolved', label: 'Resolved' },
  ];

  const filtered = alerts.filter(a => {
    if (!search) return true;
    const s = search.toLowerCase();
    return (a.monitor_name || '').toLowerCase().includes(s) ||
           (a.severity || '').toLowerCase().includes(s);
  });

  return (
    <div class="alerts-page">
      <PageHeader title="Alerts" subtitle={`${alerts.length} ${tab} alerts`} />

      <Tabs tabs={tabs} active={tab} onChange={setTab} />

      <div class="filters-bar">
        <SearchInput value={search} onInput={setSearch} placeholder="Search alerts..." />
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
