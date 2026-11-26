// Paid channel gating on the channel list. Pure functions so `npm test`
// can pin them against the API contract:
//   - GET /org: plan, promo_expires_at, promo_grace_days, paid_grace_until
//     (internal/domain/org.go, set by the org store on a drop to Free).
//   - POST /notification-channels/{id}/test refusals
//     (internal/api/handlers/notification_channels_testlimit.go and
//     notification_channels_plan_full.go): 429 {retry_after, limit_scope},
//     403 {upgrade_required} or {quota_exceeded, used, limit}, 503.
//   - GET /notification-channels: per channel plan_status
//     ('ok' | 'grace' | 'disabled') and grace_ends_at (RFC 3339), from
//     billing.ChannelPlanStatus. When present these win over the local
//     rules below, which stay as the fallback for an older server.
// The rules mirror billing.PaidChannelsAllowed; the server decides.

import { isPhoneType, PAID_TYPES } from './channel-config-logic.mjs';
import { EVENTING_PAID_KINDS } from '../../edition/eventing.mjs';

export function isPaidChannelType(type) {
  return PAID_TYPES.includes(type);
}

const DAY_MS = 24 * 60 * 60 * 1000;

function parseTime(v) {
  if (typeof v !== 'string' || !v) return null;
  const t = Date.parse(v);
  return Number.isFinite(t) ? t : null;
}

export function defaultFormatDate(ms) {
  return new Date(ms).toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

// paidGraceDeadline returns when paid channel types stop delivering for
// org, in ms. Returns undefined when they deliver with no end (a paid
// plan, or a promo with no expiry) and null when they already cannot
// (Free with no grace on record).
export function paidGraceDeadline(org) {
  if (!org) return undefined;
  const plan = org.plan || 'free';
  if (plan === 'free') return parseTime(org.paid_grace_until);
  if (plan === 'promo') {
    const expiry = parseTime(org.promo_expires_at);
    if (expiry == null) return undefined;
    return expiry + (Number(org.promo_grace_days) || 0) * DAY_MS;
  }
  return undefined;
}

// paidChannelStatus says whether a paid channel on the list still sends
// alerts. Returns null when there is nothing to say: a free channel type,
// the FOSS edition (no plan gating), a paid plan, or a promo still before
// its expiry (the promo banner covers that). Otherwise:
//   { status: 'grace' | 'paused', badge, detail, until }
// The badge is a word, never color alone.
export function paidChannelStatus(type, org, { edition, now = Date.now(), formatDate = defaultFormatDate } = {}) {
  if (edition === 'foss' || !isPaidChannelType(type) || !org) return null;
  const plan = org.plan || 'free';
  if (plan === 'promo') {
    const expiry = parseTime(org.promo_expires_at);
    if (expiry == null || now < expiry) return null;
  } else if (plan !== 'free') {
    return null;
  }
  const deadline = paidGraceDeadline(org);
  if (deadline != null && now < deadline) {
    return {
      status: 'grace',
      badge: 'Pro only',
      until: deadline,
      detail: `Your org is no longer on a paid plan. This channel keeps sending alerts until ${formatDate(deadline)}, then stops. Upgrade to Pro to keep it.`,
    };
  }
  return {
    status: 'paused',
    badge: 'Requires Pro',
    until: deadline ?? null,
    detail: 'Not sending alerts. This channel type needs Pro and the grace period after the paid plan ended has passed. Upgrade to Pro to turn it back on.',
  };
}

// channelPlanView is paidChannelStatus for a channel row from the API.
// It uses the server's plan_status and grace_ends_at when the row has
// them, and the local rules from org otherwise.
export function channelPlanView(channel, org, { edition, formatDate = defaultFormatDate, ...rest } = {}) {
  if (edition === 'foss' || !channel) return null;
  const status = channel.plan_status;
  if (typeof status !== 'string' || !status) {
    return paidChannelStatus(channel.type, org, { edition, formatDate, ...rest });
  }
  const until = parseTime(channel.grace_ends_at);
  if (status === 'grace') {
    return {
      status: 'grace',
      badge: 'Pro only',
      until,
      detail: until != null
        ? `Your org is no longer on a paid plan. This channel keeps sending alerts until ${formatDate(until)}, then stops. Upgrade to Pro to keep it.`
        : 'Your org is no longer on a paid plan. This channel stops sending alerts when the grace period ends. Upgrade to Pro to keep it.',
    };
  }
  if (status === 'disabled') {
    return {
      status: 'paused',
      badge: 'Requires Pro',
      until,
      detail: 'Not sending alerts. This channel type needs Pro and the grace period after the paid plan ended has passed. Upgrade to Pro to turn it back on.',
    };
  }
  return null;
}

// pausedChannelsNote is the one-line summary above the list when at least
// one paid channel has stopped, or '' when none has. Alerts silently not
// reaching people is the outcome to avoid, so it names the channels.
export function pausedChannelsNote(channels, org, opts = {}) {
  if (!Array.isArray(channels)) return '';
  const names = channels
    .filter(ch => channelPlanView(ch, org, opts)?.status === 'paused')
    .map(ch => ch.name || 'Unnamed channel');
  if (!names.length) return '';
  return names.length === 1
    ? `${names[0]} is not sending alerts because it needs Pro.`
    : `${names.length} channels are not sending alerts because they need Pro: ${names.join(', ')}.`;
}

const KIND_ORDER = ['SMS', 'voice', 'Telegram', ...Object.values(EVENTING_PAID_KINDS)];

function paidKind(type) {
  if (type === 'twilio_sms' || type === 'telnyx_sms') return 'SMS';
  if (type === 'twilio_voice' || type === 'telnyx_voice') return 'voice';
  if (type === 'telegram') return 'Telegram';
  return EVENTING_PAID_KINDS[type] || null;
}

// paidKinds names the channel kinds in a fixed order: "SMS and voice",
// "SMS, voice and Telegram". Matches the owner email wording.
export function paidKinds(channels) {
  const seen = new Set(channels.map(ch => paidKind(ch?.type)).filter(Boolean));
  const kinds = KIND_ORDER.filter(k => seen.has(k));
  if (!kinds.length) return 'Paid';
  if (kinds.length === 1) return kinds[0];
  return kinds.slice(0, -1).join(', ') + ' and ' + kinds[kinds.length - 1];
}

// paidChannelsBanner is the page banner on the channel list after the org
// left its paid plan, or null when no channel is affected. Disabled wins
// over grace: once any paid channel has stopped, that is the news, and
// channels still in their grace get a second sentence.
//   {
//     state: 'grace' | 'disabled',
//     label,        // leading word, so the state does not rely on color
//     text,         // what happens and when
//     names,        // disabled only: the channels that stopped
//     alsoGrace,    // disabled only: '' or when the rest stop
//     until,
//     action,       // { text, href } or null when there is nowhere to go
//     note,         // '' or who can act, for people who cannot upgrade
//     upgradeHref,
//   }
// opts.canUpgrade: owners and admins (billing changes need that role).
// opts.billingEnabled: false hides the billing link (no billing tab).
export function paidChannelsBanner(channels, org, opts = {}) {
  if (!Array.isArray(channels)) return null;
  const formatDate = opts.formatDate || defaultFormatDate;
  const canUpgrade = opts.canUpgrade !== false;
  const billing = opts.billingEnabled !== false;
  const rows = channels
    .filter(ch => ch && ch.enabled !== false)
    .map(ch => ({ ch, v: channelPlanView(ch, org, opts) }))
    .filter(r => r.v);
  const disabled = rows.filter(r => r.v.status === 'paused');
  const grace = rows.filter(r => r.v.status === 'grace');
  const earliest = list => {
    const times = list.map(r => r.v.until).filter(t => t != null);
    return times.length ? Math.min(...times) : null;
  };
  const graceText = () => {
    const until = earliest(grace);
    const when = until != null ? `on ${formatDate(until)}` : 'when the grace period ends';
    return { until, text: `${paidKinds(grace.map(r => r.ch))} channels stop delivering ${when} unless you upgrade.` };
  };
  const actionFor = state => {
    if (!billing) return null;
    if (!canUpgrade) return { text: 'View plan', href: '/settings/billing' };
    return { text: state === 'disabled' ? 'Upgrade to Pro to turn them back on' : 'Upgrade to Pro', href: '/settings/billing' };
  };
  const note = canUpgrade ? '' : 'Ask an owner or admin to upgrade to Pro.';
  if (disabled.length) {
    const kinds = paidKinds(disabled.map(r => r.ch));
    const until = earliest(disabled);
    const when = until != null ? ` on ${formatDate(until)}` : '';
    return {
      state: 'disabled',
      label: 'Disabled',
      until,
      names: disabled.map(r => r.ch.name || 'Unnamed channel'),
      alsoGrace: grace.length ? graceText().text : '',
      action: actionFor('disabled'),
      note,
      upgradeHref: '/settings/billing',
      text: `${kinds} channels are disabled. They stopped delivering alerts${when} because the grace period after your paid plan ended has passed. Alerts routed only to them do not reach anyone.`,
    };
  }
  if (grace.length) {
    const g = graceText();
    return {
      state: 'grace',
      label: 'Grace period',
      until: g.until,
      names: [],
      alsoGrace: '',
      action: actionFor('grace'),
      note,
      upgradeHref: '/settings/billing',
      text: g.text,
    };
  }
  return null;
}

// durationText reads a wait in seconds as words for a message that is not
// updated live, so it rounds up past a minute.
export function durationText(secs) {
  const s = Math.max(1, Math.ceil(Number(secs) || 0));
  if (s < 60) return s === 1 ? '1 second' : `${s} seconds`;
  const m = Math.ceil(s / 60);
  return m === 1 ? '1 minute' : `${m} minutes`;
}

function retryAfterSeconds(err) {
  const fromBody = Number(err?.body?.retry_after);
  if (Number.isFinite(fromBody) && fromBody > 0) return Math.ceil(fromBody);
  const m = typeof err?.message === 'string' && err.message.match(/(\d+)\s*seconds?/);
  if (m) return Number(m[1]);
  return 60;
}

// testRefusal maps a refused Test onto what the row shows. Returns null
// for any other error, so the caller keeps its existing handling.
//   {
//     kind: 'rate' | 'upgrade' | 'quota' | 'unavailable',
//     message,           // plain text for the row and the toast
//     cooldown,          // rate only: { scope: 'org' | 'phone', until }
//     upgradeHref,       // upgrade and quota: where to change plan
//     refreshOrg,        // upgrade: the org's plan may have changed
//   }
export function testRefusal(err, channelType, now = Date.now()) {
  const status = err?.status;
  const body = err?.body || {};
  if (status === 429) {
    const secs = retryAfterSeconds(err);
    const scope = body.limit_scope === 'phone' || body.limit_scope === 'org'
      ? body.limit_scope
      : (isPhoneType(channelType) ? 'phone' : 'org');
    const rule = scope === 'phone'
      ? 'SMS and voice tests are limited to 3 every 10 minutes, since each one is a real text or call.'
      : 'Tests are limited to 10 a minute for your org.';
    return {
      kind: 'rate',
      message: `${rule} Try again in ${durationText(secs)}.`,
      cooldown: { scope, until: now + secs * 1000 },
    };
  }
  if (status === 403 && body.quota_exceeded) {
    const base = typeof body.error === 'string' && body.error ? body.error.replace(/\.$/, '') : 'Quota reached';
    const what = /voice/i.test(base) ? 'Voice alerts' : 'SMS alerts';
    return {
      kind: 'quota',
      message: `${base}. The test was not sent. ${what} also stop until the quota resets with the next billing period.`,
      upgradeHref: '/settings/billing',
    };
  }
  if (status === 403 && body.upgrade_required) {
    return {
      kind: 'upgrade',
      message: typeof body.error === 'string' && body.error ? body.error : 'This channel type needs Pro.',
      upgradeHref: '/settings/billing',
      refreshOrg: true,
    };
  }
  if (status === 503) {
    return {
      kind: 'unavailable',
      message: 'The plan or quota for this channel could not be checked, so the test was not sent. Try again in a moment.',
    };
  }
  return null;
}

// testCooldown returns the active cooldown that covers channelType, or
// null. cooldowns is { org?: until, phone?: until } in ms.
export function testCooldown(cooldowns, channelType, now = Date.now()) {
  if (!cooldowns) return null;
  const hits = [];
  if (cooldowns.org && cooldowns.org > now) hits.push({ scope: 'org', until: cooldowns.org });
  if (isPhoneType(channelType) && cooldowns.phone && cooldowns.phone > now) hits.push({ scope: 'phone', until: cooldowns.phone });
  if (!hits.length) return null;
  return hits.reduce((a, b) => (b.until > a.until ? b : a));
}
