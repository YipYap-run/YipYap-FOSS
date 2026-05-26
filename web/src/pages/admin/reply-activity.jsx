import { useState, useEffect, useMemo } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, Card, LoadingPage, ErrorMessage, formatTime } from '../../components/ui';
import { currentUser } from '../../state/auth';

// ReplyActivityPage renders a reverse-chronological feed of every
// state-mutating reply event the admin's org has received, with basic
// filters for channel, alert, type, and time window. Clicking a row
// opens a detail panel with the before/after JSON: the MVP of our
// reply-audit forensic tool.
//
// Data source: GET /api/v1/admin/cloudevents/replies. The server
// enforces the owner/admin role gate; this page additionally bails out
// early on the client to avoid a useless network round-trip for non-
// admins.

const SINCE_OPTIONS = [
  { key: '1h',  label: 'Last hour',   ms: 1 * 60 * 60 * 1000 },
  { key: '24h', label: 'Last 24h',    ms: 24 * 60 * 60 * 1000 },
  { key: '7d',  label: 'Last 7 days', ms: 7 * 24 * 60 * 60 * 1000 },
  { key: '30d', label: 'Last 30 days', ms: 30 * 24 * 60 * 60 * 1000 },
];

// REPLY_TYPE_OPTIONS mirrors the Phase-2 + Phase-5 catalog. We hard-code
// the list so the filter dropdown is populated without an extra API call;
// when the catalog grows we just update this array.
const REPLY_TYPE_OPTIONS = [
  { key: 'run.yipyap.reply.alert.claimed.v1',           label: 'alert.claimed' },
  { key: 'run.yipyap.reply.alert.enriched.v1',          label: 'alert.enriched' },
  { key: 'run.yipyap.reply.alert.linked.v1',            label: 'alert.linked' },
  { key: 'run.yipyap.reply.alert.suppressed.v1',        label: 'alert.suppressed' },
  { key: 'run.yipyap.reply.alert.deduplicated.v1',      label: 'alert.deduplicated' },
  { key: 'run.yipyap.reply.alert.ack_with_context.v1',  label: 'alert.ack_with_context' },
  { key: 'run.yipyap.reply.alert.ownership.v1',         label: 'alert.ownership' },
  { key: 'run.yipyap.reply.alert.remediation_started.v1', label: 'alert.remediation_started' },
  { key: 'run.yipyap.reply.alert.remediation_result.v1',  label: 'alert.remediation_result' },
  { key: 'run.yipyap.reply.alert.status_page.v1',       label: 'alert.status_page' },
  { key: 'run.yipyap.reply.alert.escalated.v1',         label: 'alert.escalated' },
  { key: 'run.yipyap.reply.alert.route.v1',             label: 'alert.route' },
  { key: 'run.yipyap.reply.monitor.deregister.v1',      label: 'monitor.deregister' },
];

// truncateID returns the short form of a ULID / UUID suitable for inline
// display, just enough to tell two rows apart without crowding.
function truncateID(id) {
  if (!id) return '';
  if (id.length <= 10) return id;
  return id.slice(0, 8) + '…';
}

// outcomeColor returns an inline foreground color for the Outcome cell.
function outcomeColor(outcome) {
  if (!outcome) return 'var(--color-muted)';
  if (outcome === 'accepted') return 'var(--color-up, #16a34a)';
  if (outcome.startsWith('rejected:')) return 'var(--color-warning, #f59e0b)';
  if (outcome.startsWith('handler_error:')) return 'var(--color-down, #ef4444)';
  return 'var(--color-muted)';
}

export function ReplyActivityPage() {
  const user = currentUser.value;
  const isAdmin = user && (user.role === 'owner' || user.role === 'admin');

  const [entries, setEntries] = useState([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(null);

  // Filter state. `channel` and `alert` are free-text (exact match). The
  // server accepts empty strings as "no filter" so we pass them through
  // verbatim.
  const [sinceKey, setSinceKey] = useState('24h');
  const [channel, setChannel] = useState('');
  const [alert, setAlert] = useState('');
  const [replyType, setReplyType] = useState('');

  const sinceMs = useMemo(() => {
    const hit = SINCE_OPTIONS.find((s) => s.key === sinceKey);
    return hit ? hit.ms : 24 * 60 * 60 * 1000;
  }, [sinceKey]);

  async function load({ append = false, cursor = null } = {}) {
    if (!isAdmin) return;
    setLoading(true);
    setError(null);
    try {
      const params = new URLSearchParams();
      const since = cursor || new Date(Date.now() - sinceMs).toISOString();
      params.set('since', since);
      if (channel) params.set('channel', channel);
      if (alert) params.set('alert', alert);
      if (replyType) params.set('type', replyType);
      params.set('limit', '100');
      const data = await get('/admin/cloudevents/replies?' + params.toString());
      const rows = data?.entries || [];
      setEntries(append ? (prev) => [...prev, ...rows] : rows);
      setHasMore(!!data?.has_more);
    } catch (e) {
      setError(e);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (!isAdmin) {
      setLoading(false);
      return;
    }
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin, sinceKey, channel, alert, replyType]);

  function loadMore() {
    // "Load more" pages by sliding the since cursor to just-before the
    // oldest row currently in view. The server returns the next window
    // in reverse-chrono order; we append.
    if (entries.length === 0) return;
    const oldest = entries[entries.length - 1];
    const cursor = new Date(new Date(oldest.received_at).getTime() - 1).toISOString();
    // Swap to a manual load: skip the useEffect re-run.
    (async () => {
      setLoading(true);
      try {
        const params = new URLSearchParams();
        params.set('since', new Date(Date.now() - sinceMs).toISOString());
        if (channel) params.set('channel', channel);
        if (alert) params.set('alert', alert);
        if (replyType) params.set('type', replyType);
        // Overfetch the existing window then drop rows already in view.
        // Simpler and correct for the MVP; a proper pagination cursor is
        // a follow-up.
        params.set('limit', String(Math.min(500, entries.length + 100)));
        const data = await get('/admin/cloudevents/replies?' + params.toString());
        const rows = data?.entries || [];
        // Dedup on id: the server may return entries we already have.
        const seen = new Set(entries.map((e) => e.id));
        const fresh = rows.filter((e) => !seen.has(e.id));
        setEntries([...entries, ...fresh]);
        setHasMore(!!data?.has_more);
      } catch (e) {
        setError(e);
      } finally {
        setLoading(false);
      }
    })();
  }

  if (!isAdmin) {
    return (
      <div>
        <PageHeader title="Reply Activity" />
        <Card>
          <p>This dashboard is only visible to org admins and owners.</p>
        </Card>
      </div>
    );
  }

  return (
    <div class="reply-activity-page">
      <PageHeader title="Reply Activity" subtitle="CloudEvent replies received from downstream sinks." />

      <Card>
        <div style="display:flex;flex-wrap:wrap;gap:12px;align-items:flex-end;margin-bottom:12px">
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">Since</span>
            <select value={sinceKey} onChange={(e) => setSinceKey(e.target.value)}>
              {SINCE_OPTIONS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}
            </select>
          </label>
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">Reply type</span>
            <select value={replyType} onChange={(e) => setReplyType(e.target.value)}>
              <option value="">all</option>
              {REPLY_TYPE_OPTIONS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}
            </select>
          </label>
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">Channel ID</span>
            <input type="text" value={channel} onInput={(e) => setChannel(e.target.value)} placeholder="(all)" style="min-width:180px" />
          </label>
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">Alert ID</span>
            <input type="text" value={alert} onInput={(e) => setAlert(e.target.value)} placeholder="(all)" style="min-width:180px" />
          </label>
          <button class="btn btn-sm" onClick={() => load()} disabled={loading}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {error && <ErrorMessage error={error} onRetry={() => load()} />}
        {loading && entries.length === 0 && <LoadingPage />}

        {!loading && entries.length === 0 && !error && (
          <p class="text-muted" style="margin-top:12px">No reply events in this window.</p>
        )}

        {entries.length > 0 && (
          <div style="overflow-x:auto">
            <table class="data-table" style="width:100%;border-collapse:collapse">
              <thead>
                <tr>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Time</th>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Type</th>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Alert</th>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Channel</th>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Outcome</th>
                  <th style="text-align:left;padding:6px 8px;border-bottom:1px solid var(--color-border)">Sub</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e) => {
                  const short = e.reply_type.replace('run.yipyap.', '');
                  const active = selected && selected.id === e.id;
                  return (
                    <tr key={e.id}
                        onClick={() => setSelected(active ? null : e)}
                        style={`cursor:pointer;background:${active ? 'var(--color-bg-hover, #f1f5f9)' : 'transparent'}`}>
                      <td style="padding:6px 8px;font-family:monospace;font-size:0.85em;white-space:nowrap">{formatTime(e.received_at)}</td>
                      <td style="padding:6px 8px"><code style="font-size:0.85em">{short}</code></td>
                      <td style="padding:6px 8px;font-family:monospace;font-size:0.85em" title={e.alert_id}>{truncateID(e.alert_id)}</td>
                      <td style="padding:6px 8px;font-family:monospace;font-size:0.85em" title={e.channel_id}>{truncateID(e.channel_id)}</td>
                      <td style={`padding:6px 8px;font-weight:600;color:${outcomeColor(e.outcome)}`}>{e.outcome}</td>
                      <td style="padding:6px 8px;font-family:monospace;font-size:0.85em" title={e.reply_sub}>{truncateID(e.reply_sub)}</td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        )}

        {hasMore && (
          <div style="margin-top:12px;text-align:center">
            <button class="btn btn-sm" onClick={loadMore} disabled={loading}>
              {loading ? 'Loading…' : 'Load more'}
            </button>
          </div>
        )}
      </Card>

      {selected && (
        <Card style="margin-top:16px">
          <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:8px">
            <h3 style="margin:0">Reply detail</h3>
            <button class="btn btn-sm" onClick={() => setSelected(null)}>Close</button>
          </div>
          <ReplyDetail entry={selected} />
        </Card>
      )}
    </div>
  );
}

function ReplyDetail({ entry }) {
  const before = formatJSON(entry.before);
  const after = formatJSON(entry.after);
  return (
    <div class="reply-detail">
      <dl style="display:grid;grid-template-columns:max-content 1fr;gap:4px 14px;margin:0 0 12px 0;font-size:0.9em">
        <dt class="text-muted">Reply ID</dt><dd><code>{entry.reply_id}</code></dd>
        <dt class="text-muted">Type</dt><dd><code>{entry.reply_type}</code></dd>
        <dt class="text-muted">Source</dt><dd><code>{entry.reply_source}</code></dd>
        <dt class="text-muted">Sub</dt><dd><code>{entry.reply_sub || '(none)'}</code></dd>
        <dt class="text-muted">Alert</dt><dd><code>{entry.alert_id || '(none)'}</code></dd>
        <dt class="text-muted">Monitor</dt><dd><code>{entry.monitor_id || '(none)'}</code></dd>
        <dt class="text-muted">Channel</dt><dd><code>{entry.channel_id}</code></dd>
        <dt class="text-muted">Outcome</dt><dd><strong style={`color:${outcomeColor(entry.outcome)}`}>{entry.outcome}</strong></dd>
        <dt class="text-muted">Received</dt><dd>{formatTime(entry.received_at)}</dd>
      </dl>

      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
        <div>
          <div class="form-help" style="margin-bottom:4px">Before</div>
          <pre style="background:var(--color-bg-alt, #f8fafc);border:1px solid var(--color-border);padding:8px;border-radius:4px;font-size:0.8em;overflow:auto;max-height:260px;margin:0">{before}</pre>
        </div>
        <div>
          <div class="form-help" style="margin-bottom:4px">After</div>
          <pre style="background:var(--color-bg-alt, #f8fafc);border:1px solid var(--color-border);padding:8px;border-radius:4px;font-size:0.8em;overflow:auto;max-height:260px;margin:0">{after}</pre>
        </div>
      </div>
    </div>
  );
}

// formatJSON renders a value for the before/after <pre>. Store columns
// arrive as raw JSON (we typed them as json.RawMessage on the wire so
// the Fetch layer keeps them as already-decoded JS values once parsed).
// If a column is missing we render "(none)" rather than "null" so it is
// obvious the handler didn't emit one.
function formatJSON(v) {
  if (v === undefined || v === null) return '(none)';
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}
