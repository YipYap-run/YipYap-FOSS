import { useState, useEffect, useMemo, useRef } from 'preact/hooks';
import { get } from '../../api/client';
import { useLiveRefresh } from '../../state/live';
import { PageHeader, Card, SeverityIcon, AlertStatusPill, Tabs, SearchInput, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';
import { SeverityTag } from './severity.jsx';

// STATS_WINDOW is how many recent alerts the rollup stats and the 7-day
// chart are computed from (the API's largest page). The labels say so.
const STATS_WINDOW = 200;

// localDayKey is the viewer's local calendar date as YYYY-MM-DD, so an
// alert at 23:30 local time lands on that day, not the UTC one.
export function localDayKey(d) {
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// dedupeById keeps the first alert seen for each id.
export function dedupeById(alerts) {
  const seen = new Set();
  return alerts.filter(a => {
    if (!a || !a.id || seen.has(a.id)) return false;
    seen.add(a.id);
    return true;
  });
}

// alertsPerDay counts alerts by local start date over the last 7 days,
// oldest first.
export function alertsPerDay(alerts, now = new Date()) {
  const days = {};
  for (let i = 6; i >= 0; i--) {
    const d = new Date(now);
    d.setDate(d.getDate() - i);
    days[localDayKey(d)] = 0;
  }
  dedupeById(alerts).forEach(a => {
    if (!a.started_at) return;
    const started = new Date(a.started_at);
    if (Number.isNaN(started.getTime())) return;
    const day = localDayKey(started);
    if (day in days) days[day] += 1;
  });
  return Object.entries(days);
}

// dayLabels turns a YYYY-MM-DD key into a short visible label ("Mon 23")
// and a full one for the data table ("Monday, November 23").
function dayLabels(key) {
  const [y, m, d] = key.split('-').map(Number);
  const date = new Date(y, m - 1, d);
  return {
    short: date.toLocaleDateString(undefined, { weekday: 'short', day: 'numeric' }),
    long: date.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' }),
  };
}

// AlertsByDayChart draws the bars for sighted users and a visually hidden
// table with the same numbers for screen readers (WCAG 1.1.1).
function AlertsByDayChart({ alerts }) {
  const data = useMemo(() => alertsPerDay(alerts).map(([day, count]) => ({ day, count, ...dayLabels(day) })), [alerts]);
  const max = Math.max(...data.map(d => d.count), 1);
  const total = data.reduce((n, d) => n + d.count, 0);

  return (
    <figure class="alerts-chart">
      <div class="alerts-chart-bars" aria-hidden="true">
        {data.map(d => (
          <div key={d.day} class="alerts-chart-col">
            <span class="alerts-chart-count">{d.count || ''}</span>
            <div class={`alerts-chart-bar${d.count > 0 ? ' has-alerts' : ''}`}
              style={{ height: `${Math.max((d.count / max) * 60, 2)}px` }} />
            <span class="alerts-chart-day">{d.short}</span>
          </div>
        ))}
      </div>
      <table class="sr-only">
        <caption>Alerts started per day, last 7 days, {total} in total</caption>
        <thead><tr><th scope="col">Day</th><th scope="col">Alerts</th></tr></thead>
        <tbody>
          {data.map(d => <tr key={d.day}><th scope="row">{d.long}</th><td>{d.count}</td></tr>)}
        </tbody>
      </table>
    </figure>
  );
}

function fmtDuration(ms) {
  if (ms == null) return '--';
  if (ms < 60000) return `${Math.round(ms / 1000)}s`;
  if (ms < 3600000) return `${Math.round(ms / 60000)}m`;
  return `${(ms / 3600000).toFixed(1)}h`;
}

function meanMs(list, from, to) {
  const rows = list.filter(a => a[from] && a[to]);
  if (rows.length === 0) return null;
  return rows.reduce((sum, a) => sum + (new Date(a[to]) - new Date(a[from])), 0) / rows.length;
}

// countLabel shows "200+" when a list came back full, so a capped count
// does not read as exact.
export function countLabel(n, cap) {
  return n >= cap ? `${cap}+` : String(n);
}

// StatsCards: the active and SEV counts come from the open alerts
// (firing or acknowledged, up to STATS_WINDOW); MTTA and MTTR come from the
// last STATS_WINDOW alerts of any status, and say so.
function StatsCards({ open, recent }) {
  const active = dedupeById(open);
  const sev = (s) => active.filter(a => a.severity === s).length;
  const source = dedupeById(recent);
  const mtta = meanMs(source, 'started_at', 'acknowledged_at');
  const mttr = meanMs(source, 'started_at', 'resolved_at');

  const cards = [
    { label: 'Open', value: countLabel(active.length, STATS_WINDOW), color: 'var(--color-text)' },
    { label: 'SEV1', value: sev('critical'), color: 'var(--color-text)' },
    { label: 'SEV2', value: sev('warning'), color: 'var(--color-text)' },
    { label: 'SEV3', value: sev('info'), color: 'var(--color-text)' },
    { label: 'MTTA', title: 'Mean time to acknowledge', note: `last ${STATS_WINDOW} alerts`, value: fmtDuration(mtta), color: 'var(--color-text-secondary)' },
    { label: 'MTTR', title: 'Mean time to resolve', note: `last ${STATS_WINDOW} alerts`, value: fmtDuration(mttr), color: 'var(--color-text-secondary)' },
  ];

  return (
    <dl class="alerts-stats">
      {cards.map(c => (
        <div key={c.label} class="alerts-stat">
          <dt class="alerts-stat-label">
            {c.title ? <abbr title={c.title}>{c.label}</abbr> : c.label}
            {c.label.startsWith('SEV') ? <span class="sr-only"> open</span> : null}
          </dt>
          <dd class="alerts-stat-value" style={{ color: c.color, margin: 0 }}>{c.value}</dd>
          {c.note && <dd class="alerts-stat-note" style={{ margin: 0 }}>{c.note}</dd>}
        </div>
      ))}
    </dl>
  );
}

const SEVERITY_OPTIONS = [
  { key: '', label: 'All severities' },
  { key: 'critical', label: 'SEV1 - Critical' },
  { key: 'warning', label: 'SEV2 - Warning' },
  { key: 'info', label: 'SEV3 - Info' },
];

const TABS = [
  { key: 'firing', label: 'Active', empty: 'No active alerts', emptyNote: 'Nothing is firing right now.' },
  { key: 'acknowledged', label: 'Acknowledged', empty: 'No acknowledged alerts', emptyNote: 'Alerts someone has acknowledged but not resolved show up here.' },
  { key: 'resolved', label: 'Resolved', empty: 'No resolved alerts', emptyNote: 'Resolved alerts show up here.' },
];

export function AlertListPage() {
  const [alerts, setAlerts] = useState([]);
  const [recent, setRecent] = useState([]);
  const [open, setOpen] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [tab, setTab] = useState('firing');
  const [search, setSearch] = useState('');
  const [sevFilter, setSevFilter] = useState('');

  // tabRef tracks the open tab so a slow response for a tab the user
  // already left is dropped instead of overwriting the list.
  const tabRef = useRef(tab);
  tabRef.current = tab;

  // quiet is a background refresh after a live event: no spinner, and a
  // failed refresh keeps the current list.
  async function load(quiet = false) {
    const forTab = tab;
    if (!quiet) {
      setLoading(true);
      setError(null);
    }
    try {
      const data = await get(`/alerts?status=${encodeURIComponent(forTab)}`);
      if (tabRef.current !== forTab) return;
      setAlerts(data.alerts || data || []);
    } catch (err) {
      if (!quiet && tabRef.current === forTab) setError(err);
    } finally {
      if (tabRef.current === forTab) setLoading(false);
    }
  }

  // Rollup data: open alerts for the counts, and the most recent alerts of
  // any status for MTTA, MTTR and the 7-day chart. A failure keeps the
  // previous numbers.
  function loadStats() {
    return Promise.all([
      get(`/alerts?status=firing,acknowledged&limit=${STATS_WINDOW}`).then(d => setOpen(d.alerts || d || [])).catch(() => {}),
      get(`/alerts?limit=${STATS_WINDOW}`).then(d => setRecent(d.alerts || d || [])).catch(() => {}),
    ]);
  }

  useEffect(() => { loadStats(); }, []);

  useEffect(() => { load(); }, [tab]);

  useLiveRefresh(() => Promise.all([load(true), loadStats()]));

  const current = TABS.find(t => t.key === tab) || TABS[0];

  const filtered = alerts.filter(a => {
    if (sevFilter && a.severity !== sevFilter) return false;
    if (!search) return true;
    const s = search.toLowerCase();
    return (a.monitor_name || '').toLowerCase().includes(s) ||
           (a.severity || '').toLowerCase().includes(s);
  });
  const narrowed = !!search || !!sevFilter;
  const n = alerts.length;

  return (
    <div class="alerts-page">
      <PageHeader title="Alerts" subtitle={`${n} ${current.label.toLowerCase()} alert${n === 1 ? '' : 's'}`} />

      <StatsCards open={open} recent={recent} />

      <Card style={{ marginBottom: '16px' }}>
        <h3 style={{ margin: '0 0 8px 0', fontSize: '0.875rem' }}>Alerts started, last 7 days</h3>
        <p class="text-muted alerts-chart-caption">
          From the last {STATS_WINDOW} alerts, by the day they started in your time zone.
        </p>
        <AlertsByDayChart alerts={recent} />
      </Card>

      <Tabs tabs={TABS} active={tab} onChange={setTab} />

      <div class="filters-bar" style={{ display: 'flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' }}>
        <SearchInput value={search} onInput={setSearch} placeholder="Search alerts..." label="Search alerts by monitor or severity" />
        <select class="filter-select filter-select-sm" aria-label="Severity" value={sevFilter} onChange={e => setSevFilter(e.target.value)}>
          {SEVERITY_OPTIONS.map(o => <option key={o.key} value={o.key}>{o.label}</option>)}
        </select>
      </div>

      {loading ? <LoadingPage /> : error ? <ErrorMessage error={error} onRetry={() => load()} /> : (
        <div class="alert-list">
          {filtered.length === 0 ? (
            <div class="empty-state">
              {narrowed && n > 0 ? (
                <>
                  <h3>No matching alerts</h3>
                  <p>
                    None of the {n} {current.label.toLowerCase()} alert{n === 1 ? '' : 's'} match your search or severity filter.{' '}
                    <button type="button" class="btn btn-sm" onClick={() => { setSearch(''); setSevFilter(''); }}>Clear filters</button>
                  </p>
                </>
              ) : (
                <>
                  <h3>{current.empty}</h3>
                  <p>{current.emptyNote}</p>
                </>
              )}
            </div>
          ) : (
            filtered.map(a => (
              <a key={a.id} href={`/alerts/${a.id}`} class="alert-card">
                <div class="alert-card-left">
                  <SeverityTag severity={a.severity} />
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
