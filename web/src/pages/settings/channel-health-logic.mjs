// Channel delivery health shown on the channel list. Contract:
// internal/domain/notification.go ChannelHealth, returned as `health` on
// GET /notification-channels and GET /notification-channels/{id}:
//   { status: 'ok' | 'failing' | 'unknown', last_error?, last_failure_at?,
//     last_success_at?, consecutive_failures, owners_notified_at? }
// owners_notified_at is set while the org owners have been emailed about
// the current failure streak.
// last_error is a sanitized failure class written by the server, never a
// provider body or credential. It is still rendered as plain text only.

function ago(ts, now) {
  if (!ts) return '';
  const t = new Date(ts).getTime();
  if (!Number.isFinite(t)) return '';
  const diff = Math.max(0, now - t);
  const min = Math.floor(diff / 60000);
  if (min < 1) return 'just now';
  if (min < 60) return `${min} min ago`;
  const h = Math.floor(min / 60);
  if (h < 24) return `${h} h ago`;
  const d = Math.floor(h / 24);
  return `${d} day${d === 1 ? '' : 's'} ago`;
}

// deliveryReason drops the "Delivery failed" lead-in the server puts on
// every public failure class, since the UI already says it failed.
export function deliveryReason(text) {
  const s = typeof text === 'string' ? text.trim() : '';
  if (!s) return '';
  const m = s.match(/^Delivery failed\s*[:.]\s*(.*)$/i);
  if (!m) return s;
  const rest = m[1].trim();
  if (!rest) return '';
  return rest.charAt(0).toUpperCase() + rest.slice(1);
}

// channelHealthView returns what the list row shows, or null when there is
// nothing worth showing (no deliveries yet).
//   { status, badge, detail, tone }
export function channelHealthView(health, now = Date.now()) {
  if (!health || typeof health !== 'object') return null;
  const n = Number(health.consecutive_failures) || 0;
  if (health.status === 'failing' || n > 0) {
    const count = Math.max(n, 1);
    const when = ago(health.last_failure_at, now);
    const reason = deliveryReason(health.last_error);
    const countText = count === 1 ? 'Last delivery failed' : `Last ${count} deliveries failed`;
    return {
      status: 'failing',
      tone: 'down',
      badge: 'Failing',
      detail: `${countText}${when ? ` (${when})` : ''}.${reason ? ' ' + reason : ''}`
        + (health.owners_notified_at ? ' Owners were emailed.' : ''),
    };
  }
  if (health.status === 'ok') {
    const when = ago(health.last_success_at, now);
    return {
      status: 'ok',
      tone: 'up',
      badge: '',
      detail: when ? `Last sent ${when}` : '',
    };
  }
  return null;
}

// failingChannelsNote is the one-line summary above the list, or '' when
// every channel is fine. canManage picks the next step: people who can edit
// channels fix and test it, everyone else asks an owner or admin.
export function failingChannelsNote(channels, { canManage = false, now = Date.now() } = {}) {
  if (!Array.isArray(channels)) return '';
  const names = channels
    .filter(ch => channelHealthView(ch?.health, now)?.status === 'failing')
    .map(ch => ch.name || 'Unnamed channel');
  if (!names.length) return '';
  const one = names.length === 1;
  const head = one
    ? `${names[0]} is failing to deliver alerts.`
    : `${names.length} channels are failing to deliver alerts: ${names.join(', ')}.`;
  const next = canManage
    ? (one ? ' Check its settings, then send a test.' : ' Check their settings, then send a test.')
    : ' Ask an owner or admin to check the settings.';
  return head + next;
}

// linkedChannel reads the channel the owner failure email links to
// (/settings/channels?channel=ID). Returns { id, found } or null when the
// URL names no channel. found is false when the channel is not in the
// list (deleted, or another org's), so the page can say so.
export function linkedChannel(search, channels) {
  let id = '';
  try {
    id = new URLSearchParams(typeof search === 'string' ? search : '').get('channel') || '';
  } catch {
    return null;
  }
  id = id.trim();
  if (!id) return null;
  const found = Array.isArray(channels) && channels.some(ch => ch && ch.id === id);
  return { id, found };
}

// testSentMessage is the toast after a successful Test. owners_notified
// is true when the test ended a failure streak the owners were emailed
// about, and they are now getting a follow-up.
export function testSentMessage(resp) {
  if (resp && resp.owners_notified === true) {
    return 'Test notification sent. The channel works again, and the owners are getting a follow-up email.';
  }
  return 'Test notification sent';
}

// A CloudEvents test whose Event Types Filter leaves out the type Test sends
// is not delivered. The API answers { filtered: true, event_type } so the
// page can say why nothing arrived. Returns copy, or null for any other
// response.
export function testFilteredMessage(resp) {
  if (!resp || resp.filtered !== true) return null;
  const t = typeof resp.event_type === 'string' && resp.event_type ? resp.event_type : 'the test event';
  return 'Nothing was sent. Test sends ' + t + ', and this channel\'s Event Types Filter does not match it. Add "run.yipyap.alert.*" to the filter to test this channel.';
}

// testFailureNote is the row note for a Test that was sent and failed. It
// stays under the row until dismissed or the next Test, so the reason is
// not lost to a timer.
export function testFailureNote(text) {
  const reason = deliveryReason(typeof text === 'string' ? text : '');
  return {
    kind: 'failed',
    message: 'Test failed. ' + (reason || 'Check the channel settings and try again.'),
    dismissible: true,
  };
}
