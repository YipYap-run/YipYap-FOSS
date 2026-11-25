import { useState, useEffect, useMemo, useRef } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, Card, LoadingPage, formatTime } from '../../components/ui';
import { currentUser } from '../../state/auth';
import {
  FILTER_DEBOUNCE_MS,
  buildReplyQuery,
  createLatestRequest,
  debounce,
  isAbortError,
  outcomeClass,
  statusText,
} from './reply-activity-fetch.mjs';
import '../../styles/reply-activity.css';

// ReplyActivityPage renders a reverse-chronological feed of every
// state-mutating reply the org's sinks have sent back, translated into
// plain language: what happened, on which monitor, through which channel,
// and whether it was accepted or rejected and why. Clicking a row opens
// the before/after JSON for forensic detail. Each row is a button that
// expands its detail panel in place, so it works from the keyboard.
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
  if (!outcome) return 'Unknown';
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
  // channelInput/alertInput follow the text boxes on every keystroke;
  // channel/alert are the debounced values that drive requests.
  const [channelInput, setChannelInput] = useState('');
  const [alertInput, setAlertInput] = useState('');
  const [channel, setChannel] = useState('');
  const [alert, setAlert] = useState('');
  const [replyType, setReplyType] = useState('');

  // One request at a time: a new load aborts the previous one and a late
  // response for an old filter is dropped.
  const latest = useMemo(() => createLatestRequest(), []);
  // Values are trimmed before they become filters, so a stray space does not
  // start a request for the same query.
  const applyText = useMemo(() => debounce((c, a) => {
    setChannel(c.trim());
    setAlert(a.trim());
  }, FILTER_DEBOUNCE_MS), []);
  const rowRefs = useRef({});

  useEffect(() => () => {
    applyText.cancel();
    latest.cancel();
  }, []);

  function onTextFilter(nextChannel, nextAlert) {
    setChannelInput(nextChannel);
    setAlertInput(nextAlert);
    applyText(nextChannel, nextAlert);
  }

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

  // fetchReplies runs one request through the latest-request guard and hands
  // the response to apply only if no newer request has started since.
  // A failed filter load (replace=true) clears the list so rows from the
  // previous filter are never shown under the new one; a failed Load more
  // keeps what is already on screen.
  async function fetchReplies(filters, limit, apply, replace) {
    const req = latest.start();
    setLoading(true);
    setError(null);
    try {
      const qs = buildReplyQuery({
        since: new Date(Date.now() - sinceMs).toISOString(),
        channel: filters.channel,
        alert: filters.alert,
        replyType,
        limit,
      });
      const data = await get('/admin/cloudevents/replies?' + qs, { signal: req.signal });
      if (!req.isCurrent()) return;
      apply(data?.entries || [], !!data?.has_more);
    } catch (e) {
      if (isAbortError(e) || !req.isCurrent()) return;
      if (replace) {
        setEntries([]);
        setHasMore(false);
      }
      setError(e);
    } finally {
      if (req.isCurrent()) setLoading(false);
    }
  }

  function load(filters = { channel, alert }) {
    if (!isAdmin) return;
    fetchReplies(filters, 100, (rows, more) => {
      setEntries(rows);
      setHasMore(more);
    }, true);
  }

  useEffect(() => {
    if (!isAdmin) {
      setLoading(false);
      return;
    }
    setSelected(null);
    load();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [isAdmin, sinceKey, channel, alert, replyType]);

  // Refresh applies any text still waiting on the debounce right away.
  function refresh() {
    applyText.cancel();
    if (channelInput.trim() !== channel || alertInput.trim() !== alert) {
      setChannel(channelInput.trim());
      setAlert(alertInput.trim());
      return; // the filter effect loads
    }
    load();
  }

  function loadMore() {
    if (entries.length === 0) return;
    const current = entries;
    fetchReplies({ channel, alert }, Math.min(500, current.length + 100), (rows, more) => {
      const seen = new Set(current.map((e) => e.id));
      setEntries([...current, ...rows.filter((e) => !seen.has(e.id))]);
      setHasMore(more);
    }, false);
  }

  function toggle(e) {
    setSelected(selected && selected.id === e.id ? null : e);
  }

  // closeDetail collapses the open row and puts focus back on its button so
  // keyboard users do not lose their place.
  function closeDetail() {
    const id = selected && selected.id;
    setSelected(null);
    if (id && rowRefs.current[id]) rowRefs.current[id].focus();
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
      <div class="reply-activity">
        <PageHeader title="Reply Activity" />
        <Card>
          <p>Only org owners and admins can see reply activity.</p>
        </Card>
      </div>
    );
  }

  const textPending = channelInput.trim() !== channel || alertInput.trim() !== alert;

  return (
    <div class="reply-activity">
      <PageHeader
        title="Reply Activity"
        subtitle="CloudEvent replies sent back by your connected sinks, the inbound half of the YipYap eventing loop."
      />

      <Card>
        <p class="text-muted ra-intro">
          A <code>cloudevent_http</code> channel connects an alert to a downstream sink over the
          CNCF CloudEvents protocol. That sink can answer with its own CloudEvent, typed{' '}
          <code>run.yipyap.reply.*</code>. Each entry below is one such reply, with the action it
          took, the monitor it targeted, and whether it was accepted or rejected.
        </p>
        <div class="ra-filters" role="group" aria-label="Filter replies">
          <label class="ra-filter">
            <span class="form-help">Since</span>
            <select value={sinceKey} onChange={(e) => setSinceKey(e.target.value)}>
              {SINCE_OPTIONS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}
            </select>
          </label>
          <label class="ra-filter">
            <span class="form-help">Reply type</span>
            <select value={replyType} onChange={(e) => setReplyType(e.target.value)}>
              <option value="">All types</option>
              {REPLY_TYPE_OPTIONS.map((o) => <option key={o.key} value={o.key}>{o.label}</option>)}
            </select>
          </label>
          <label class="ra-filter">
            <span class="form-help">Channel ID</span>
            <input
              type="text"
              value={channelInput}
              onInput={(e) => onTextFilter(e.target.value, alertInput)}
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); refresh(); } }}
              placeholder="All channels"
              autocomplete="off"
              spellcheck={false}
            />
          </label>
          <label class="ra-filter">
            <span class="form-help">Alert ID</span>
            <input
              type="text"
              value={alertInput}
              onInput={(e) => onTextFilter(channelInput, e.target.value)}
              onKeyDown={(e) => { if (e.key === 'Enter') { e.preventDefault(); refresh(); } }}
              placeholder="All alerts"
              autocomplete="off"
              spellcheck={false}
            />
          </label>
          <button type="button" class="btn btn-sm" onClick={refresh} disabled={loading && !textPending}>
            {loading ? 'Refreshing…' : 'Refresh'}
          </button>
        </div>

        {/* The count shows once rows are on screen; while loading or empty the
            spinner and empty message cover it, so the region is for screen
            readers only. */}
        <p
          class={'ra-status' + (entries.length > 0 && !loading ? '' : ' sr-only')}
          role="status"
          aria-live="polite"
        >
          {statusText({ loading, error, count: entries.length, hasMore })}
        </p>

        {error && (
          <div class="ra-error" role="alert">
            <p>Could not load replies. {error?.message || String(error)}</p>
            <button type="button" class="btn btn-sm" onClick={() => load()}>Try again</button>
          </div>
        )}
        {loading && entries.length === 0 && <LoadingPage />}

        {!loading && entries.length === 0 && !error && (
          <p class="text-muted" style="margin-top:12px">
            {channel || alert || replyType
              ? 'No replies match these filters in this time window. Clear a filter or pick a longer window.'
              : <>No CloudEvent replies in this window. Replies appear once a
                connected sink answers an alert with a <code>run.yipyap.reply.*</code> CloudEvent.</>}
          </p>
        )}

        {entries.length > 0 && (
          <ul class="ra-list" aria-label="CloudEvent replies" aria-busy={loading ? 'true' : 'false'}>
            {entries.map((e) => {
              const active = selected && selected.id === e.id;
              const detailId = 'reply-detail-' + e.id;
              return (
                <li key={e.id}>
                  <button
                    type="button"
                    ref={(el) => { if (el) rowRefs.current[e.id] = el; else delete rowRefs.current[e.id]; }}
                    onClick={() => toggle(e)}
                    onKeyDown={(ev) => { if (ev.key === 'Escape' && active) { ev.preventDefault(); closeDetail(); } }}
                    aria-expanded={active ? 'true' : 'false'}
                    aria-controls={detailId}
                    class="reply-row"
                  >
                    <span class={'ra-badge ' + outcomeClass(e.outcome)}>
                      {outcomeLabel(e.outcome)}
                    </span>
                    <span class="ra-main">
                      <span class="ra-title">
                        {replyLabel(e.reply_type)}{' '}
                        <span aria-hidden="true" class="ra-sep">·</span>{' '}
                        {subjectName(e)}
                      </span>
                      <span class="ra-meta">
                        via {channelLabel(e)} · {formatTime(e.received_at)}
                      </span>
                    </span>
                    <span aria-hidden="true" class="ra-chevron">
                      {active ? '×' : '›'}
                    </span>
                  </button>
                  <div
                    id={detailId}
                    role="region"
                    aria-label={'Reply detail: ' + replyLabel(e.reply_type)}
                    hidden={!active}
                    onKeyDown={(ev) => { if (ev.key === 'Escape') { ev.preventDefault(); closeDetail(); } }}
                    class="ra-detail"
                  >
                    {active && (
                      <>
                        <div class="ra-detail-head">
                          <h3>Reply detail</h3>
                          <button type="button" class="btn btn-sm" onClick={closeDetail}>Close</button>
                        </div>
                        <ReplyDetail entry={e} />
                      </>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        )}

        {hasMore && (
          <div class="ra-more">
            <button type="button" class="btn btn-sm" onClick={loadMore} disabled={loading}>
              {loading ? 'Loading…' : 'Load more'}
            </button>
          </div>
        )}
      </Card>

    </div>
  );
}

function ReplyDetail({ entry }) {
  const before = formatJSON(entry.before);
  const after = formatJSON(entry.after);
  return (
    <div class="reply-detail">
      <p style="margin:0 0 12px 0">
        <strong>{replyLabel(entry.reply_type)}</strong> ·{' '}
        <span class={outcomeClass(entry.outcome)}>{outcomeLabel(entry.outcome)}</span>
      </p>

      <dl class="ra-fields">
        <dt>Reply ID</dt><dd><code>{entry.reply_id}</code></dd>
        <dt>CloudEvent type</dt><dd><code>{entry.reply_type}</code></dd>
        <dt>Source</dt><dd><code>{entry.reply_source}</code></dd>
        <dt>Sender</dt><dd><code>{entry.reply_sub || '(none)'}</code></dd>
        <dt>Alert</dt><dd><code>{entry.alert_id || '(none)'}</code></dd>
        <dt>Monitor</dt><dd><code>{entry.monitor_id || '(none)'}</code></dd>
        <dt>Channel</dt><dd><code>{entry.channel_id}</code></dd>
        <dt>Received</dt><dd>{formatTime(entry.received_at)}</dd>
      </dl>

      <div class="ra-json">
        <div>
          <div class="form-help" style="margin-bottom:4px">Before</div>
          <pre tabIndex={0} aria-label="State before the reply">{before}</pre>
        </div>
        <div>
          <div class="form-help" style="margin-bottom:4px">After</div>
          <pre tabIndex={0} aria-label="State after the reply">{after}</pre>
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
