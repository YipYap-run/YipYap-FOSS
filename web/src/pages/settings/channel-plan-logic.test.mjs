// Run with: cd web && npm test
// Contract: GET /org (internal/domain/org.go paid_grace_until, promo fields)
// and the channel Test refusals in
// internal/api/handlers/notification_channels_testlimit.go and
// notification_channels_plan_full.go.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { paidChannelStatus, pausedChannelsNote, paidGraceDeadline, testRefusal, testCooldown, durationText, channelPlanView, paidChannelsBanner, paidKinds } from './channel-plan-logic.mjs';

const NOW = Date.parse('2026-11-24T12:00:00Z');
const DAY = 24 * 60 * 60 * 1000;
const fmt = ms => new Date(ms).toISOString().slice(0, 10);
const opts = { now: NOW, formatDate: fmt };

test('free and paid plans', () => {
  assert.equal(paidChannelStatus('telegram', { plan: 'pro' }, opts), null);
  assert.equal(paidChannelStatus('telegram', { plan: 'enterprise' }, opts), null);
  // Free channel types are never gated.
  assert.equal(paidChannelStatus('webhook', { plan: 'free' }, opts), null);
  // FOSS has no plan gating.
  assert.equal(paidChannelStatus('telegram', { plan: 'free' }, { ...opts, edition: 'foss' }), null);
  assert.equal(paidChannelStatus('telegram', null, opts), null);
});

test('downgraded org inside the grace keeps delivering and says until when', () => {
  const org = { plan: 'free', paid_grace_until: new Date(NOW + 10 * DAY).toISOString() };
  const v = paidChannelStatus('telnyx_sms', org, opts);
  assert.equal(v.status, 'grace');
  assert.equal(v.badge, 'Pro only');
  assert.match(v.detail, /until 2026-12-04, then stops/);
  assert.match(v.detail, /Upgrade to Pro/);
});

test('after the grace, or with none on record, the channel is paused', () => {
  const past = { plan: 'free', paid_grace_until: new Date(NOW - DAY).toISOString() };
  assert.equal(paidChannelStatus('telegram', past, opts).status, 'paused');
  assert.equal(paidChannelStatus('telegram', past, opts).badge, 'Requires Pro');
});

test('promo past expiry follows expiry plus grace days, like the dispatcher', () => {
  const live = { plan: 'promo', promo_expires_at: new Date(NOW + DAY).toISOString(), promo_grace_days: 30 };
  assert.equal(paidChannelStatus('telegram', live, opts), null);
  const grace = { plan: 'promo', promo_expires_at: new Date(NOW - DAY).toISOString(), promo_grace_days: 30 };
  assert.equal(paidChannelStatus('telegram', grace, opts).status, 'grace');
  assert.equal(paidGraceDeadline(grace), NOW + 29 * DAY);
  const over = { plan: 'promo', promo_expires_at: new Date(NOW - 31 * DAY).toISOString(), promo_grace_days: 30 };
  assert.equal(paidChannelStatus('telegram', over, opts).status, 'paused');
  // An open-ended promo never pauses.
  assert.equal(paidChannelStatus('telegram', { plan: 'promo' }, opts), null);
});

test('paused note names the channels', () => {
  const org = { plan: 'free' };
  const chans = [
    { name: 'On-call SMS', type: 'telnyx_sms' },
    { name: 'Ops hook', type: 'webhook' },
    { name: 'Team TG', type: 'telegram' },
  ];
  assert.equal(pausedChannelsNote(chans, org, opts), '2 channels are not sending alerts because they need Pro: On-call SMS, Team TG.');
  assert.equal(pausedChannelsNote([chans[0]], org, opts), 'On-call SMS is not sending alerts because it needs Pro.');
  assert.equal(pausedChannelsNote(chans, { plan: 'pro' }, opts), '');
});

test('429 uses the body wait and scope', () => {
  const org = testRefusal({ status: 429, body: { retry_after: 42, limit_scope: 'org' } }, 'webhook', NOW);
  assert.equal(org.kind, 'rate');
  assert.deepEqual(org.cooldown, { scope: 'org', until: NOW + 42000 });
  assert.match(org.message, /10 a minute.*Try again in 42 seconds\./);
  const phone = testRefusal({ status: 429, body: { retry_after: 400, limit_scope: 'phone' } }, 'telnyx_sms', NOW);
  assert.equal(phone.cooldown.scope, 'phone');
  assert.match(phone.message, /3 every 10 minutes.*Try again in 7 minutes\./);
});

test('429 from an older server falls back to the message and channel type', () => {
  const r = testRefusal({ status: 429, message: 'too many test notifications, try again in 9 seconds', body: {} }, 'twilio_voice', NOW);
  assert.deepEqual(r.cooldown, { scope: 'phone', until: NOW + 9000 });
});

test('quota, upgrade and unavailable refusals', () => {
  const q = testRefusal({ status: 403, body: { error: 'SMS quota reached (100 of 100 this billing period)', quota_exceeded: true, used: 100, limit: 100 } }, 'telnyx_sms');
  assert.equal(q.kind, 'quota');
  assert.equal(q.message, 'SMS quota reached (100 of 100 this billing period). The test was not sent. SMS alerts also stop until the quota resets with the next billing period.');
  assert.equal(q.upgradeHref, '/settings/billing');
  const v = testRefusal({ status: 403, body: { error: 'Voice call quota reached (50 of 50 this billing period)', quota_exceeded: true } }, 'telnyx_voice');
  assert.match(v.message, /Voice alerts also stop/);

  const u = testRefusal({ status: 403, body: { error: 'Telegram alerts are paused.', upgrade_required: true } }, 'telegram');
  assert.equal(u.kind, 'upgrade');
  assert.equal(u.refreshOrg, true);
  assert.equal(u.message, 'Telegram alerts are paused.');

  assert.equal(testRefusal({ status: 503, body: {} }, 'telnyx_sms').kind, 'unavailable');
  // Anything else is left to the existing handling.
  assert.equal(testRefusal({ status: 400, body: { field: 'url' } }, 'webhook'), null);
  assert.equal(testRefusal({ status: 403, body: { error: 'forbidden' } }, 'webhook'), null);
});

test('cooldowns cover the right buttons', () => {
  const cd = { phone: NOW + 5000 };
  assert.equal(testCooldown(cd, 'webhook', NOW), null);
  assert.deepEqual(testCooldown(cd, 'telnyx_sms', NOW), { scope: 'phone', until: NOW + 5000 });
  assert.equal(testCooldown(cd, 'telnyx_sms', NOW + 6000), null);
  const both = { org: NOW + 1000, phone: NOW + 9000 };
  assert.equal(testCooldown(both, 'webhook', NOW).scope, 'org');
  assert.equal(testCooldown(both, 'twilio_sms', NOW).scope, 'phone');
});

test('duration text', () => {
  assert.equal(durationText(1), '1 second');
  assert.equal(durationText(59), '59 seconds');
  assert.equal(durationText(60), '1 minute');
  assert.equal(durationText(61), '2 minutes');
  assert.equal(durationText(0), '1 second');
});

// Contract: plan_status and grace_ends_at on GET /notification-channels
// (internal/api/handlers/notification_channels_planstatus_full.go).

test('server plan_status wins over the local rules', () => {
  const ends = new Date(NOW + 5 * DAY).toISOString();
  // The org says Pro, the server says grace: trust the server.
  const v = channelPlanView({ type: 'telnyx_sms', plan_status: 'grace', grace_ends_at: ends }, { plan: 'pro' }, opts);
  assert.equal(v.status, 'grace');
  assert.match(v.detail, /until 2026-11-29/);
  const d = channelPlanView({ type: 'telegram', plan_status: 'disabled', grace_ends_at: ends }, { plan: 'pro' }, opts);
  assert.equal(d.status, 'paused');
  assert.equal(d.badge, 'Requires Pro');
  assert.equal(channelPlanView({ type: 'telegram', plan_status: 'ok' }, { plan: 'free' }, opts), null);
  // No plan_status (older server): fall back to the org rules.
  assert.equal(channelPlanView({ type: 'telegram' }, { plan: 'free' }, opts).status, 'paused');
  assert.equal(channelPlanView({ type: 'telegram', plan_status: 'disabled' }, null, { ...opts, edition: 'foss' }), null);
});

test('paidKinds matches the owner email wording', () => {
  assert.equal(paidKinds([{ type: 'twilio_voice' }, { type: 'telnyx_sms' }]), 'SMS and voice');
  assert.equal(paidKinds([{ type: 'telegram' }, { type: 'twilio_sms' }, { type: 'telnyx_voice' }]), 'SMS, voice and Telegram');
  assert.equal(paidKinds([{ type: 'webhook' }]), 'Paid');
});

test('channel page banner: grace with the end date, then disabled', () => {
  const soon = new Date(NOW + 3 * DAY).toISOString();
  const later = new Date(NOW + 9 * DAY).toISOString();
  const grace = paidChannelsBanner([
    { type: 'telnyx_sms', plan_status: 'grace', grace_ends_at: later, enabled: true },
    { type: 'twilio_voice', plan_status: 'grace', grace_ends_at: soon, enabled: true },
    { type: 'webhook', plan_status: 'ok', enabled: true },
  ], null, opts);
  assert.equal(grace.state, 'grace');
  assert.equal(grace.text, 'SMS and voice channels stop delivering on 2026-11-27 unless you upgrade.');
  assert.equal(grace.upgradeHref, '/settings/billing');

  const past = new Date(NOW - DAY).toISOString();
  const disabled = paidChannelsBanner([
    { type: 'telnyx_sms', plan_status: 'disabled', grace_ends_at: past, enabled: true },
    { type: 'telegram', plan_status: 'grace', grace_ends_at: later, enabled: true },
  ], null, opts);
  assert.equal(disabled.state, 'disabled');
  assert.match(disabled.text, /^SMS channels are disabled\. They stopped delivering alerts on 2026-11-23/);

  assert.equal(disabled.label, 'Disabled');
  assert.match(disabled.text, /Alerts routed only to them do not reach anyone\.$/);
  // The upgrade step is the link, not repeated in the text.
  assert.doesNotMatch(disabled.text, /Upgrade/);
  assert.deepEqual(disabled.names, ['Unnamed channel']);
  assert.equal(disabled.alsoGrace, 'Telegram channels stop delivering on 2026-12-03 unless you upgrade.');
  assert.deepEqual(disabled.action, { text: 'Upgrade to Pro to turn them back on', href: '/settings/billing' });
  assert.equal(disabled.note, '');
  assert.equal(grace.label, 'Grace period');
  assert.deepEqual(grace.names, []);
  assert.equal(grace.alsoGrace, '');
  assert.deepEqual(grace.action, { text: 'Upgrade to Pro', href: '/settings/billing' });

  // Nothing affected, a switched-off channel, or FOSS: no banner.
  assert.equal(paidChannelsBanner([{ type: 'webhook', plan_status: 'ok' }], null, opts), null);
  assert.equal(paidChannelsBanner([{ type: 'telnyx_sms', plan_status: 'disabled', enabled: false }], null, opts), null);
  assert.equal(paidChannelsBanner([{ type: 'telnyx_sms', plan_status: 'disabled' }], null, { ...opts, edition: 'foss' }), null);
  assert.equal(paidChannelsBanner(null, null, opts), null);
});

test('channel page banner: who can act on it', () => {
  const later = new Date(NOW + 9 * DAY).toISOString();
  const rows = [{ name: 'On-call SMS', type: 'telnyx_sms', plan_status: 'grace', grace_ends_at: later, enabled: true }];
  // Members cannot change the plan (billing writes are owner or admin):
  // they are told who can and get a read-only link to the plan.
  const member = paidChannelsBanner(rows, null, { ...opts, canUpgrade: false });
  assert.equal(member.note, 'Ask an owner or admin to upgrade to Pro.');
  assert.deepEqual(member.action, { text: 'View plan', href: '/settings/billing' });
  // Without billing there is no billing tab to link to.
  const noBilling = paidChannelsBanner(rows, null, { ...opts, billingEnabled: false });
  assert.equal(noBilling.action, null);
  assert.equal(noBilling.text, 'SMS channels stop delivering on 2026-12-03 unless you upgrade.');
  // A grace with no known end still says what happens.
  const open = paidChannelsBanner([{ type: 'telegram', plan_status: 'grace', enabled: true }], null, opts);
  assert.equal(open.text, 'Telegram channels stop delivering when the grace period ends unless you upgrade.');
});
