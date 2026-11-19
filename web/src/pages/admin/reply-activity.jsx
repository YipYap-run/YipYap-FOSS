import { useState, useEffect, useMemo } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, Card, LoadingPage, ErrorMessage, formatTime } from '../../components/ui';
import { currentUser } from '../../state/auth';

// ReplyActivityPage renders a reverse-chronological feed of every
// state-mutating reply the org's sinks have sent back, translated into
// plain language: what happened, on which monitor, through which channel,
// and whether it was accepted or rejected and why. Clicking a row opens
// the before/after JSON for forensic detail.
//
// Data source: GET /api/v1/admin/cloudevents/replies (owner/admin only).

const SINCE_OPTIONS = [
  { key: '1h',  label: 'Last hour',   ms: 1 * 60 * 60 * 1000 },
  { key: '24h', label: 'Last 24h',    ms: 24 * 60 * 60 * 1000 },
  { key: '7d',  label: 'Last 7 days', ms: 7 * 24 * 60 * 60 * 1000 },
  { key: '30d', label: 'Last 30 days', ms: 30 * 24 * 60 * 60 * 1000 },
];

const REPLY_TYPE_OPTIONS = [
  { key: 'run.yipyap.reply.alert.claimed.v1',           label: 'Claimed alert' },
  { key: 'run.yipyap.reply.alert.enriched.v1',          label: 'Enriched alert' },
  { key: 'run.yipyap.reply.alert.linked.v1',            label: 'Linked alert to incident' },
  { key: 'run.yipyap.reply.alert.suppressed.v1',        label: 'Suppressed alert' },
  { key: 'run.yipyap.reply.alert.deduplicated.v1',      label: 'Deduplicated alert' },
  { key: 'run.yipyap.reply.alert.ack_with_context.v1',  label: 'Acknowledged alert' },
  { key: 'run.yipyap.reply.alert.ownership.v1',         label: 'Changed alert ownership' },
  { key: 'run.yipyap.reply.alert.remediation_started.v1', label: 'Started remediation' },
  { key: 'run.yipyap.reply.alert.remediation_result.v1',  label: 'Recorded remediation result' },
  { key: 'run.yipyap.reply.alert.status_page.v1',       label: 'Updated status page' },
  { key: 'run.yipyap.reply.alert.escalated.v1',         label: 'Escalated alert' },
  { key: 'run.yipyap.reply.alert.route.v1',             label: 'Routed alert' },
  { key: 'run.yipyap.reply.monitor.deregister.v1',      label: 'Deregistered monitor' },
];

// Human-readable verb for a reply type. Falls back to a trimmed type name.
const REPLY_TYPE_LABELS = Object.fromEntries(
  REPLY_TYPE_OPTIONS.map((o) => [o.key, o.label])
);

// Human-readable outcome + badge styling.
const OUTCOME_LABELS = {
  'accepted': 'Accepted',
  'rejected:org_mismatch': 'Rejected · wrong org',
  'rejected:channel_mismatch': 'Rejected · wrong channel',
  'rejected:trust_required': 'Rejected · trust not established',
  'rejected:rate_limited': 'Rejected · rate limited',
  'rejected:unknown_alertref': 'Rejected · unknown alert',
  'rejected:alertref_expired': 'Rejected · reference expired',
  'rejected:unknown_type': 'Rejected · unknown reply type',
};

function outcomeStyle(outcome) {
  if (outcome === 'accepted') {
    return { color: 'var(--color-up)', bg: 'var(--color-up-bg)' };
  }
  if (outcome.startsWith('handler_error:')) {
    return { color: 'var(--color-down)', bg: 'var(--color-down-bg)' };
  }
  if (outcome.startsWith('rejected:')) {
    return { color: 'var(--color-warning)', bg: 'var(--color-warning-bg)' };
  }
  return { color: 'var(--color-muted)', bg: 'var(--color-muted-bg)' };
}

function truncateID(id) {
  if (!id) return '';
  if (id.length <= 10) return id;
  return id.slice(0, 8) + '…';
}

// replyLabel turns a reply type into its human verb, falling back to the
// trimmed type name for types not yet in the catalog.
function replyLabel(type) {
  if (!type) return 'Unknown reply';
  return REPLY_TYPE_LABELS[type] || type.replace('run.yipyap.', '');
}

// outcomeLabel turns a raw outcome into human copy.
function outcomeLabel(outcome) {
  if (!outcome) return '—';
  return OUTCOME_LABELS[outcome] || outcome;
}

export function ReplyActivityPage() {
  const user = currentUser.value;
  const isAdmin = user && (user.role === 'owner' || user.role === 'admin');

  const [entries, setEntries] = useState([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [selected, setSelected] = useState(null);

  const [sinceKey, setSinceKey] = useState('24h');
  const [channel, setChannel] = useState('');
  const [alert, setAlert] = useState('');
  const [replyType, setReplyType] = useState('');

  // ID -> name maps, populated once so rows show real names instead of UUIDs.
  const [monitorNames, setMonitorNames] = useState({});
  const [channels, setChannels] = useState({});
  const [alertMonitors, setAlertMonitors] = useState({});

  const sinceMs = useMemo(() => {
    const hit = SINCE_OPTIONS.find((s) => s.key === sinceKey);
    return hit ? hit.ms : 24 * 60 * 60 * 1000;
  }, [sinceKey]);

  useEffect(() => {
    if (!isAdmin) return;
    (async () => {
      try {
        const [m, c, a] = await Promise.all([
          get('/monitors'),
          get('/notification-channels'),
          get('/alerts'),
        ]);
        setMonitorNames(Object.fromEntries((m || []).map((x) => [x.id, x.name])));
        setChannels(Object.fromEntries((c || []).map((x) => [x.id, x])));
        setAlertMonitors(Object.fromEntries((a || []).map((x) => [x.id, x.monitor_id])));
      } catch (_) {
        // Names are cosmetic; keep going if any lookup fails.
      }
    })();
  }, [isAdmin]);

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
    if (entries.length === 0) return;
    const oldest = entries[entries.length - 1];
    const cursor = new Date(new Date(oldest.received_at).getTime() - 1).toISOString();
    (async () => {
      setLoading(true);
      try {
        const params = new URLSearchParams();
        params.set('since', new Date(Date.now() - sinceMs).toISOString());
        if (channel) params.set('channel', channel);
        if (alert) params.set('alert', alert);
        if (replyType) params.set('type', replyType);
        params.set('limit', String(Math.min(500, entries.length + 100)));
        const data = await get('/admin/cloudevents/replies?' + params.toString());
        const rows = data?.entries || [];
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

  // Resolve an entry's subject: the monitor name if present, else the
  // alert's monitor, else a fallback.
  function subjectName(e) {
    const mid = e.monitor_id || alertMonitors[e.alert_id];
    return monitorNames[mid] || (mid ? 'Monitor ' + truncateID(mid) : 'a monitor');
  }

  function channelLabel(e) {
    const c = channels[e.channel_id];
    if (c) return `${c.name} · ${c.type}`;
    return e.channel_id ? 'Channel ' + truncateID(e.channel_id) : 'unknown channel';
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
      <PageHeader
        title="Reply Activity"
        subtitle="CloudEvent replies sent back by your connected sinks — the inbound half of yipyap's cloud-native eventing loop."
      />

      <Card>
        <p class="text-muted" style="margin:0 0 14px 0;font-size:0.9em">
          A <code>cloudevent_http</code> channel connects an alert to a downstream sink over the
          CNCF CloudEvents protocol. That sink can answer with its own CloudEvent, typed{' '}
          <code>run.yipyap.reply.*</code>. Each entry below is one such reply, with the action it
          took, the monitor it targeted, and whether it was accepted or rejected.
        </p>
        <div style="display:flex;flex-wrap:wrap;gap:12px;align-items:flex-end;margin-bottom:16px">
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">Since</span>
            <select value={sinceKey} onChange={(e) => setSinceKey(e.target.value)}>
              {SINCE_OPTIONS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}
            </select>
          </label>
          <label style="display:flex;flex-direction:column;gap:2px">
            <span class="form-help">CloudEvent type</span>
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
          <p class="text-muted" style="margin-top:12px">
            No CloudEvent replies in this window. Replies appear once a
            connected sink answers an alert with a <code>run.yipyap.reply.*</code> CloudEvent.
          </p>
        )}

        {entries.length > 0 && (
          <div style="display:flex;flex-direction:column;gap:8px">
            {entries.map((e) => {
              const active = selected && selected.id === e.id;
              const badge = outcomeStyle(e.outcome);
              return (
                <div
                  key={e.id}
                  onClick={() => setSelected(active ? null : e)}
                  style={{
                    cursor: 'pointer',
                    display: 'flex',
                    alignItems: 'flex-start',
                    gap: '12px',
                    padding: '12px 14px',
                    borderRadius: '8px',
                    border: `1px solid ${active ? 'var(--color-primary)' : 'var(--color-border)'}`,
                    background: active ? 'var(--color-surface-elevated)' : 'var(--color-surface)',
                  }}
                >
                  <span
                    style={{
                      flexShrink: 0,
                      marginTop: 1,
                      fontSize: '0.72em',
                      fontWeight: 700,
                      letterSpacing: '0.02em',
                      padding: '3px 8px',
                      borderRadius: 999,
                      color: badge.color,
                      background: badge.bg,
                      whiteSpace: 'nowrap',
                    }}
                  >
                    {outcomeLabel(e.outcome)}
                  </span>
                  <div style={{ flex: 1, minWidth: 0 }}>
                    <div style={{ fontWeight: 600 }}>
                      {replyLabel(e.reply_type)}{' '}
                      <span style="color:var(--color-text-muted, #94a3b8);font-weight:400">·</span>{' '}
                      {subjectName(e)}
                    </div>
                    <div style="font-size:0.85em;color:var(--color-text-muted, #94a3b8);margin-top:2px">
                      via {channelLabel(e)} · {formatTime(e.received_at)}
                    </div>
                  </div>
                  <span style="color:var(--color-text-muted, #94a3b8);font-size:1.2em;line-height:1;margin-top:2px">
                    {active ? '×' : '›'}
                  </span>
                </div>
              );
            })}
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
  const badge = outcomeStyle(entry.outcome);
  return (
    <div class="reply-detail">
      <p style="margin:0 0 12px 0">
        <strong>{replyLabel(entry.reply_type)}</strong> ·{' '}
        <span style={`color:${badge.color};font-weight:600`}>{outcomeLabel(entry.outcome)}</span>
      </p>

      <dl style="display:grid;grid-template-columns:max-content 1fr;gap:4px 14px;margin:0 0 12px 0;font-size:0.9em">
        <dt class="text-muted">Reply ID</dt><dd><code>{entry.reply_id}</code></dd>
        <dt class="text-muted">CloudEvent type</dt><dd><code>{entry.reply_type}</code></dd>
        <dt class="text-muted">Source</dt><dd><code>{entry.reply_source}</code></dd>
        <dt class="text-muted">Sub</dt><dd><code>{entry.reply_sub || '(none)'}</code></dd>
        <dt class="text-muted">Alert</dt><dd><code>{entry.alert_id || '(none)'}</code></dd>
        <dt class="text-muted">Monitor</dt><dd><code>{entry.monitor_id || '(none)'}</code></dd>
        <dt class="text-muted">Channel</dt><dd><code>{entry.channel_id}</code></dd>
        <dt class="text-muted">Received</dt><dd>{formatTime(entry.received_at)}</dd>
      </dl>

      <div style="display:grid;grid-template-columns:1fr 1fr;gap:12px">
        <div>
          <div class="form-help" style="margin-bottom:4px">Before</div>
          <pre style="background:var(--color-surface-elevated);border:1px solid var(--color-border);padding:8px;border-radius:4px;font-size:0.8em;overflow:auto;max-height:260px;margin:0">{before}</pre>
        </div>
        <div>
          <div class="form-help" style="margin-bottom:4px">After</div>
          <pre style="background:var(--color-surface-elevated);border:1px solid var(--color-border);padding:8px;border-radius:4px;font-size:0.8em;overflow:auto;max-height:260px;margin:0">{after}</pre>
        </div>
      </div>
    </div>
  );
}

// formatJSON renders a value for the before/after <pre>. Store columns
// arrive as raw JSON; if a column is missing we render "(none)" so it is
// obvious the handler didn't emit one.
function formatJSON(v) {
  if (v === undefined || v === null) return '(none)';
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return String(v);
  }
}
