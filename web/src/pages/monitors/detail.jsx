import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post } from '../../api/client';
import { PageHeader, Card, StatusBadge, UptimeBar, LoadingPage, ErrorMessage, formatTime, formatDuration } from '../../components/ui';
import { appMeta } from '../../state/auth';
import { safeHref } from '../../utils/url';
import 'uplot/dist/uPlot.min.css';

function HeartbeatCard({ monitorId, token, gracePeriod }) {
  const [copied, setCopied] = useState(false);
  const pingUrl = `${window.location.origin}/api/v1/heartbeat/${monitorId}?token=${token}`;
  const curlCmd = `curl -s -X POST "${pingUrl}"`;

  function copy(text) {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  }

  return (
    <Card class="heartbeat-card">
      <h3>Ping URL</h3>
      <p class="text-muted">
        Send a POST request to this URL on a regular schedule. If no ping is received
        within the grace period{gracePeriod ? ` (${gracePeriod}s)` : ''}, this monitor will alert.
      </p>
      <div class="heartbeat-url-row">
        <code class="heartbeat-url">{pingUrl}</code>
        <button class="btn btn-sm" onClick={() => copy(pingUrl)}>
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <details class="heartbeat-examples">
        <summary>Usage examples</summary>
        <div class="heartbeat-example">
          <h4>cURL</h4>
          <code class="heartbeat-snippet">{curlCmd}</code>
        </div>
        <div class="heartbeat-example">
          <h4>Cron job (every 5 minutes)</h4>
          <code class="heartbeat-snippet">*/5 * * * * {curlCmd}</code>
        </div>
        <div class="heartbeat-example">
          <h4>After a script completes</h4>
          <code class="heartbeat-snippet">#!/bin/bash{'\n'}./my-backup-script.sh && {curlCmd}</code>
        </div>
      </details>
    </Card>
  );
}

const TIME_RANGES = [
  { key: '1h', label: '1h', hours: 1 },
  { key: '6h', label: '6h', hours: 6 },
  { key: '24h', label: '24h', hours: 24 },
  { key: '7d', label: '7d', hours: 168 },
  { key: '30d', label: '30d', hours: 720 },
  { key: 'all', label: 'All', hours: 0 },
];

const CHECK_STATUSES = [
  { key: '', label: 'All statuses' },
  { key: 'up', label: 'Up' },
  { key: 'down', label: 'Down' },
  { key: 'degraded', label: 'Degraded' },
];

const PAGE_SIZE = 50;

export function MonitorDetailPage({ id }) {
  const [monitor, setMonitor] = useState(null);
  const [checks, setChecks] = useState([]);
  const [checksTotal, setChecksTotal] = useState(0);
  const [checksPage, setChecksPage] = useState(0);
  const [checksRange, setChecksRange] = useState('24h');
  const [checksStatus, setChecksStatus] = useState('');
  const [checksLoading, setChecksLoading] = useState(false);
  const [uptime, setUptime] = useState(null);
  const [latency, setLatency] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [chartError, setChartError] = useState(false);
  const chartRef = useRef(null);

  const retentionDays = appMeta.value?.retention_days || (appMeta.value?.edition === 'foss' ? 30 : null);

  async function loadChecks(page, range, status) {
    setChecksLoading(true);
    try {
      let url = `/monitors/${id}/checks?limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`;
      if (range !== 'all') {
        const r = TIME_RANGES.find(t => t.key === range);
        if (r) {
          const since = new Date(Date.now() - r.hours * 3600 * 1000).toISOString();
          url += `&since=${encodeURIComponent(since)}`;
        }
      }
      if (status) url += `&status=${status}`;
      const res = await get(url);
      setChecks(res.checks || []);
      setChecksTotal(res.total || 0);
    } catch (_) {
      setChecks([]);
      setChecksTotal(0);
    } finally {
      setChecksLoading(false);
    }
  }

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const [m, u, l] = await Promise.all([
        get(`/monitors/${id}`),
        get(`/monitors/${id}/uptime`).catch(() => null),
        get(`/monitors/${id}/latency`).catch(() => ({ points: [] })),
      ]);
      setMonitor(m);
      setUptime(u);
      setLatency(l.points || l || []);
      // Load checks separately with filters.
      await loadChecks(0, '24h', '');
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [id]);

  // Latency chart with uPlot (lazy loaded).
  useEffect(() => {
    if (!latency.length || !chartRef.current) return;

    let cleanup = () => {};

    import('uplot')
      .then(({ default: uPlot }) => {
        const timestamps = latency.map(p => new Date(p.time || p.t).getTime() / 1000);
        const values = latency.map(p => p.value || p.latency_ms || p.v || 0);

        // Read theme colors from CSS custom properties.
        const style = getComputedStyle(document.documentElement);
        const textColor = style.getPropertyValue('--color-text-secondary').trim() || '#94a3b8';
        const gridColor = style.getPropertyValue('--color-border').trim() || '#334155';

        const opts = {
          width: chartRef.current.clientWidth,
          height: 200,
          cursor: { stroke: textColor, fill: 'transparent' },
          series: [
            {},
            {
              stroke: '#10b981',
              fill: 'rgba(16, 185, 129, 0.1)',
              label: 'Latency (ms)',
            },
          ],
          axes: [
            {
              stroke: textColor,
              grid: { stroke: gridColor, width: 1 },
              ticks: { stroke: gridColor, width: 1 },
              font: '11px sans-serif',
              space: 80,
            },
            {
              stroke: textColor,
              grid: { stroke: gridColor, width: 1 },
              ticks: { stroke: gridColor, width: 1 },
              font: '11px sans-serif',
              label: 'ms',
              labelFont: '11px sans-serif',
            },
          ],
          scales: { x: { time: true } },
        };

        const chart = new uPlot(opts, [timestamps, values], chartRef.current);

        // Resize chart when container resizes.
        const ro = new ResizeObserver(() => {
          if (chartRef.current) {
            chart.setSize({ width: chartRef.current.clientWidth, height: 200 });
          }
        });
        ro.observe(chartRef.current);

        cleanup = () => { ro.disconnect(); chart.destroy(); };
      })
      .catch(() => {
        setChartError(true);
      });

    return () => cleanup();
  }, [latency]);

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;
  if (!monitor) return <ErrorMessage error="Monitor not found" />;

  const isPaused = !monitor.enabled;

  // Derive current status from the latest check or the enriched response.
  const latestCheck = checks.length > 0 ? checks[0] : null;
  const currentStatus = isPaused ? 'paused' : (monitor.status || latestCheck?.status || 'unknown');
  const statusSince = monitor.status_since ? new Date(monitor.status_since) : null;
  const statusDuration = statusSince ? Date.now() - statusSince.getTime() : null;

  // Build uptime bar data.
  const uptimeBars = {};
  if (uptime) {
    for (const period of ['24h', '7d', '30d', '90d']) {
      const data = uptime[period] || uptime.periods?.[period];
      if (data) {
        uptimeBars[period] = Array.isArray(data)
          ? data.map(d => ({ ok: d.ok ?? d.status === 'up' }))
          : [];
      }
    }
  }

  return (
    <div class="monitor-detail">
      <PageHeader title={monitor.name}
        subtitle={`${monitor.type} monitor`}
        actions={
          <div class="btn-group">
            <button class="btn btn-sm" onClick={async () => {
              await post(`/monitors/${id}/${isPaused ? 'resume' : 'pause'}`, {});
              load();
            }}>
              {isPaused ? 'Resume' : 'Pause'}
            </button>
            <a href={`/monitors?edit=${id}`} class="btn btn-sm">Edit</a>
          </div>
        }
      />

      <div class="monitor-status-row">
        <StatusBadge status={currentStatus} size="lg" />
        {statusSince && (
          <span class="monitor-status-since">
            {currentStatus === 'up' ? 'Up' : currentStatus === 'down' ? 'Down' : currentStatus.charAt(0).toUpperCase() + currentStatus.slice(1)} for {formatDuration(statusDuration)}
            {' '}(since {formatTime(statusSince)})
          </span>
        )}
        {monitor.endpoint && <span class="monitor-endpoint">{monitor.endpoint}</span>}
      </div>
      {monitor.last_error && currentStatus !== 'up' && (
        <div class="monitor-last-error">{monitor.last_error}</div>
      )}

      {monitor.type === 'heartbeat' && monitor.heartbeat_token && (
        <HeartbeatCard monitorId={monitor.id} token={monitor.heartbeat_token} gracePeriod={monitor.config?.grace_period_seconds} />
      )}

      <div class="detail-grid">
        <Card>
          <h3>Uptime</h3>
          {Object.keys(uptimeBars).length > 0 ? (
            Object.entries(uptimeBars).map(([period, data]) => (
              <UptimeBar key={period} checks={data} period={period} />
            ))
          ) : (
            <p class="text-muted">No uptime data yet</p>
          )}
        </Card>

        <Card>
          <h3>Latency</h3>
          <div ref={chartRef} class="latency-chart" />
          {latency.length === 0 && <p class="text-muted">No latency data yet</p>}
          {chartError && <p class="text-muted">Latency chart unavailable</p>}
        </Card>

        <Card class="checks-explorer">
          <div class="checks-header">
            <h3>Check History</h3>
            {retentionDays && (
              <span class="text-muted checks-retention">
                {retentionDays}‑day retention
              </span>
            )}
          </div>

          <div class="checks-filters">
            <div class="checks-range-btns">
              {TIME_RANGES.map(t => (
                <button key={t.key}
                  class={`btn btn-sm ${checksRange === t.key ? 'btn-primary' : ''}`}
                  onClick={() => { setChecksRange(t.key); setChecksPage(0); loadChecks(0, t.key, checksStatus); }}>
                  {t.label}
                </button>
              ))}
            </div>
            <select class="filter-select filter-select-sm" value={checksStatus}
              onChange={e => { setChecksStatus(e.target.value); setChecksPage(0); loadChecks(0, checksRange, e.target.value); }}>
              {CHECK_STATUSES.map(s => <option key={s.key} value={s.key}>{s.label}</option>)}
            </select>
          </div>

          {checksLoading ? (
            <p class="text-muted" style="padding: 16px 0">Loading checks...</p>
          ) : checks.length > 0 ? (
            <>
              <table class="data-table">
                <thead>
                  <tr>
                    <th>Time</th>
                    <th>Status</th>
                    <th>Latency</th>
                    <th>Detail</th>
                  </tr>
                </thead>
                <tbody>
                  {checks.map((c, i) => (
                    <tr key={c.id || i}>
                      <td>{formatTime(c.checked_at || c.created_at)}</td>
                      <td><StatusBadge status={c.status || (c.ok ? 'up' : 'down')} /></td>
                      <td>{c.latency_ms != null ? `${c.latency_ms}ms` : '-'}</td>
                      <td class="check-detail">{c.error || c.detail || '-'}</td>
                    </tr>
                  ))}
                </tbody>
              </table>

              <div class="checks-pagination">
                <span class="text-muted">
                  {checksTotal > 0
                    ? `${checksPage * PAGE_SIZE + 1}-${Math.min((checksPage + 1) * PAGE_SIZE, checksTotal)} of ${checksTotal.toLocaleString()} checks`
                    : `${checks.length} checks`}
                </span>
                <div class="btn-group">
                  <button class="btn btn-sm" disabled={checksPage === 0}
                    onClick={() => { const p = checksPage - 1; setChecksPage(p); loadChecks(p, checksRange, checksStatus); }}>
                    Prev
                  </button>
                  <button class="btn btn-sm" disabled={(checksPage + 1) * PAGE_SIZE >= checksTotal}
                    onClick={() => { const p = checksPage + 1; setChecksPage(p); loadChecks(p, checksRange, checksStatus); }}>
                    Next
                  </button>
                </div>
              </div>
            </>
          ) : (
            <p class="text-muted">No checks found for this time range</p>
          )}
        </Card>

        <Card>
          <h3>Configuration</h3>
          <dl class="config-list">
            <dt>Type</dt><dd>{monitor.type}</dd>
            <dt>Interval</dt><dd>{monitor.interval_seconds || 60}s</dd>
            <dt>Timeout</dt><dd>{monitor.timeout_seconds ? `${monitor.timeout_seconds}s` : '-'}</dd>
            {monitor.endpoint && <><dt>Endpoint</dt><dd>{monitor.endpoint}</dd></>}
            {monitor.method && <><dt>Method</dt><dd>{monitor.method}</dd></>}
            {monitor.expected_status && <><dt>Expected Status</dt><dd>{monitor.expected_status}</dd></>}
            {monitor.runbook_url && (
              <><dt>Runbook</dt><dd>
                {safeHref(monitor.runbook_url)
                  ? <a href={safeHref(monitor.runbook_url)} target="_blank" rel="noopener">{monitor.runbook_url}</a>
                  : <span>{monitor.runbook_url}</span>}
              </dd></>
            )}
            {monitor.service_id && monitor.service_name && (
              <><dt>Service</dt><dd><a href={`/services/${monitor.service_id}`}>{monitor.service_name}</a></dd></>
            )}
          </dl>
          {monitor.labels && Object.keys(monitor.labels).length > 0 && (
            <div class="config-labels">
              <dt>Labels</dt>
              <dd>
                {Object.entries(monitor.labels).map(([k, v]) => (
                  <span key={k} class="label-tag">{k}: {v}</span>
                ))}
              </dd>
            </div>
          )}
        </Card>
      </div>
    </div>
  );
}
