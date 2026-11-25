// Alert timeline presentation. Pure functions so `npm test` can pin them
// against the API contract in internal/domain/alert.go (AlertEventType) and
// internal/notify/outcome.go (delivery event detail: job_id, channel_id,
// channel_name, reason).

import { deliveryReason } from '../settings/channel-health-logic.mjs';

export const CHANNEL_TYPE_LABELS = {
  webhook: 'Webhook',
  slack: 'Slack',
  discord: 'Discord',
  telegram: 'Telegram',
  smtp: 'Email',
  telnyx_sms: 'SMS',
  telnyx_voice: 'Voice call',
  twilio_sms: 'SMS',
  twilio_voice: 'Voice call',
  ntfy: 'ntfy',
  pushover: 'Pushover',
  cloudevent_http: 'CloudEvents',
};

export function channelTypeLabel(type) {
  if (!type) return '';
  return CHANNEL_TYPE_LABELS[type] || String(type);
}

export function parseDetail(detail) {
  if (!detail) return null;
  if (typeof detail === 'object') return detail;
  if (typeof detail === 'string') {
    try {
      const v = JSON.parse(detail);
      return v && typeof v === 'object' ? v : null;
    } catch { return null; }
  }
  return null;
}

export function eventType(evt) {
  const raw = evt?.event_type || evt?.action || evt?.type || evt?.event || '';
  return typeof raw === 'string' ? raw : String(raw);
}

// Fixed labels for the event types the backend writes. Anything else is
// humanized from its type string so a new type still reads as text.
const SIMPLE_LABELS = {
  triggered: 'Alert triggered',
  ack: 'Acknowledged',
  ack_with_context: 'Acknowledged with context',
  escalated: 'Escalated to the next step',
  resolved: 'Resolved',
  duplicated: 'Marked as a duplicate',
  ownership_transferred: 'Ownership transferred',
  suppressed: 'Suppressed',
  escalated_reply: 'Escalated by a reply',
  remediation_started: 'Remediation started',
  remediation_result: 'Remediation finished',
  routed: 'Routed',
  status_page_synced: 'Status page updated',
};

const SIMPLE_TONES = {
  triggered: 'down',
  ack: 'warning',
  ack_with_context: 'warning',
  resolved: 'up',
};

export function humanizeType(type) {
  const s = String(type || '').replace(/[._]+/g, ' ').trim();
  if (!s) return 'Event';
  return s.charAt(0).toUpperCase() + s.slice(1);
}

const DELIVERY_TYPES = new Set(['notified', 'notification_queued', 'notification_sent', 'notification_failed']);

export function isDeliveryEvent(type) {
  return DELIVERY_TYPES.has(type);
}

// The name to show for the channel a delivery event belongs to. Prefers
// the channel name from the detail, then the channel type.
function channelText(evt, detail) {
  const typeLabel = channelTypeLabel(evt?.channel);
  const name = detail && typeof detail.channel_name === 'string' ? detail.channel_name.trim() : '';
  if (name && typeLabel) return `${name} (${typeLabel})`;
  return name || typeLabel || 'a channel';
}

// channelSettingsHref is the channel list URL that scrolls to and focuses
// one channel (the same link the owner failure email uses). Empty when
// there is no channel id.
export function channelSettingsHref(channelId) {
  const id = typeof channelId === 'string' ? channelId.trim() : '';
  return id ? '/settings/channels?channel=' + encodeURIComponent(id) : '';
}

// skipReason recognizes the reasons the escalation engine records when it
// did not try a channel at all (internal/escalation/engine_quota_full.go,
// internal/billing/channel_gate.go), as opposed to a send that failed.
// Returns { text, actionHref, actionText } or null.
export function skipReason(reason) {
  const r = typeof reason === 'string' ? reason.trim() : '';
  if (!r) return null;
  if (r === 'requires Pro') {
    return {
      text: 'This channel type needs Pro and the grace period after the paid plan ended has passed.',
      actionHref: '/settings/billing',
      actionText: 'Upgrade to Pro',
    };
  }
  if (/quota reached/i.test(r)) {
    return {
      text: r.charAt(0).toUpperCase() + r.slice(1).replace(/\.$/, '') + '.',
      actionHref: '/settings/billing',
      actionText: 'View plan usage',
    };
  }
  if (/^(plan|SMS quota|Voice call quota) could not be checked$/i.test(r)) {
    return { text: 'The plan or quota could not be checked at the time.', actionHref: '', actionText: '' };
  }
  return null;
}

// describeEvent turns one timeline row into display data:
//   { type, label, tone, pill, reason, channelId }
// Failed deliveries also carry cause, the sanitized reason already in label,
// and channelHref, a link to that channel's row on the channel list.
// The label always states the outcome in words, so the pill is a visual
// scanning aid only and can be hidden from screen readers.
// tone is one of neutral, up, down, warning, pending and picks the dot
// color. pill is a short status word shown next to delivery events.
export function describeEvent(evt) {
  const type = eventType(evt);
  const detail = parseDetail(evt?.detail);
  const channelId = detail && typeof detail.channel_id === 'string' ? detail.channel_id : '';

  switch (type) {
    case 'notification_sent':
      return { type, label: `Sent to ${channelText(evt, detail)}`, tone: 'up', pill: 'Sent', reason: '', channelId };
    case 'notification_failed': {
      // Skipped by the plan gate or quota: nothing was sent, so say "Not
      // sent" and point to the plan rather than the channel settings.
      const skip = skipReason(detail && detail.reason);
      if (skip) {
        return { type, label: `Not sent to ${channelText(evt, detail)}: ${skip.text}`, tone: 'down', pill: 'Skipped', reason: '', cause: skip.text, channelId, channelHref: '', actionHref: skip.actionHref, actionText: skip.actionText };
      }
      // One sentence, "Delivery to <channel> failed: <reason>", so the
      // cause is read together with the channel it belongs to.
      const cause = deliveryReason(detail && detail.reason) || 'Every retry failed.';
      return { type, label: `Delivery to ${channelText(evt, detail)} failed: ${cause}`, tone: 'down', pill: 'Failed', reason: '', cause, channelId, channelHref: channelSettingsHref(channelId) };
    }
    case 'notification_queued':
      return { type, label: `Sending to ${channelText(evt, detail)}`, tone: 'pending', pill: 'Sending', reason: '', channelId };
    case 'ack':
      // Written by the escalation engine. detail.via is "link" when the
      // ack came from a signed link in a notification.
      if (detail && detail.via === 'link') {
        const from = detail.channel_name ? ` from ${detail.channel_name}` : '';
        return { type, label: `Acknowledged via link${from}`, tone: 'warning', pill: '', reason: '', channelId };
      }
      // detail.via is "voice" when someone pressed 1 on a voice call.
      if (detail && detail.via === 'voice') {
        return { type, label: 'Acknowledged by phone call', tone: 'warning', pill: '', reason: '', channelId: '' };
      }
      return { type, label: SIMPLE_LABELS.ack, tone: 'warning', pill: '', reason: '', channelId: '' };
    case 'notified':
      // Legacy rows were written before the send, so they never meant
      // the message arrived. Say what is known and no more.
      return { type, label: `Queued for ${channelText(evt, detail)}`, tone: 'neutral', pill: 'Queued', reason: 'Recorded before delivery tracking. Whether it arrived was not recorded.', channelId };
    default:
      return { type, label: SIMPLE_LABELS[type] || humanizeType(type), tone: SIMPLE_TONES[type] || 'neutral', pill: '', reason: '', channelId: '' };
  }
}

// collapseDeliveryEvents hides a notification_queued row once the same job
// has a sent or failed row, so each notification shows as one line with its
// final outcome. A queued row with no outcome yet stays, shown as Sending.
export function collapseDeliveryEvents(events) {
  if (!Array.isArray(events)) return [];
  const settled = new Set();
  for (const evt of events) {
    const t = eventType(evt);
    if (t === 'notification_sent' || t === 'notification_failed') {
      const d = parseDetail(evt.detail);
      if (d && d.job_id) settled.add(d.job_id);
    }
  }
  return events.filter(evt => {
    if (eventType(evt) !== 'notification_queued') return true;
    const d = parseDetail(evt.detail);
    return !(d && d.job_id && settled.has(d.job_id));
  });
}

// deliverySummary counts final outcomes for the banner above the timeline.
export function deliverySummary(events) {
  const out = { sent: 0, failed: 0, pending: 0, failedChannels: [], failedChannelIds: [] };
  const seen = new Set();
  const seenIds = new Set();
  for (const evt of collapseDeliveryEvents(events)) {
    const t = eventType(evt);
    if (t === 'notification_sent') out.sent++;
    else if (t === 'notification_queued') out.pending++;
    else if (t === 'notification_failed') {
      out.failed++;
      const d = parseDetail(evt.detail);
      const name = (d && d.channel_name) || channelTypeLabel(evt.channel) || '';
      if (name && !seen.has(name)) { seen.add(name); out.failedChannels.push(name); }
      const cid = d && typeof d.channel_id === 'string' ? d.channel_id : '';
      if (cid && !seenIds.has(cid)) { seenIds.add(cid); out.failedChannelIds.push(cid); }
    }
  }
  return out;
}

// failureSummaryLink is where the banner above the timeline points: the
// one failing channel when only one failed, else the whole channel list.
export function failureSummaryLink(summary) {
  const ids = summary && Array.isArray(summary.failedChannelIds) ? summary.failedChannelIds : [];
  if (ids.length === 1) {
    return { href: channelSettingsHref(ids[0]), text: 'Check this channel' };
  }
  return { href: '/settings/channels', text: 'Check notification channels' };
}

export function failureSummaryText(summary) {
  if (!summary || summary.failed === 0) return '';
  const n = summary.failed;
  const head = `${n} notification${n === 1 ? '' : 's'} could not be sent`;
  const names = summary.failedChannels;
  if (!names.length) return head + '.';
  if (names.length <= 3) return `${head} (${names.join(', ')}).`;
  return `${head} (${names.slice(0, 3).join(', ')} and ${names.length - 3} more).`;
}

// ackSummary says who acknowledged the alert and how, for the detail page.
// It reads the alert (acknowledged_at, acknowledged_by_name) and the newest
// ack row on the timeline (detail.via, detail.channel_name). A link ack
// sent to a whole channel has no person, so it names the channel instead.
// A voice-call ack has no person either and says "by phone call".
// Returns null when the alert has not been acknowledged.
export function ackSummary(alert, events) {
  if (!alert || !alert.acknowledged_at) return null;
  let ackEvt = null;
  if (Array.isArray(events)) {
    for (const evt of events) {
      const t = eventType(evt);
      if (t === 'ack' || t === 'ack_with_context') ackEvt = evt;
    }
  }
  const detail = ackEvt ? parseDetail(ackEvt.detail) : null;
  const who = typeof alert.acknowledged_by_name === 'string' ? alert.acknowledged_by_name.trim() : '';
  const viaLink = !!(detail && detail.via === 'link');
  const channel = viaLink && typeof detail.channel_name === 'string' ? detail.channel_name.trim() : '';
  const viaVoice = !!(detail && detail.via === 'voice');
  let text = who ? `Acknowledged by ${who}` : 'Acknowledged';
  if (viaLink) text += channel ? ` via link from ${channel}` : ' via link';
  else if (viaVoice) text += who ? ' on a phone call' : ' by phone call';
  return { text, at: alert.acknowledged_at, viaLink };
}

const LIVE_ALERT_TYPES = new Set(['alert.fired', 'alert.resolved', 'alert.acknowledged']);

// isLiveAlertUpdate reports whether a WebSocket frame changes alert data.
// With an alert given, it matches frames for that alert (by alert_id when
// the frame has one, else by monitor_id).
export function isLiveAlertUpdate(msg, alert) {
  if (!msg || !LIVE_ALERT_TYPES.has(msg.type)) return false;
  if (!alert) return true;
  if (msg.alert_id) return msg.alert_id === alert.id;
  return !!msg.monitor_id && msg.monitor_id === alert.monitor_id;
}

// liveStatusText is the screen reader announcement for a live change to
// the alert being viewed. Empty when the status did not change.
export function liveStatusText(before, after) {
  if (!before || !after || before === after) return '';
  if (after === 'acknowledged') return 'This alert was acknowledged.';
  if (after === 'resolved') return 'This alert was resolved.';
  if (after === 'firing') return 'This alert is firing again.';
  return '';
}
