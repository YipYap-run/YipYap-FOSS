// Run with: cd web && npm test
// Contract: internal/domain/alert.go event types and the delivery event
// detail written by internal/notify/outcome.go and the escalation engine.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { describeEvent, collapseDeliveryEvents, deliverySummary, failureSummaryText, failureSummaryLink, channelSettingsHref, humanizeType } from './timeline-logic.mjs';

const queued = (job, name = 'Ops Slack') => ({ event_type: 'notification_queued', channel: 'slack', detail: { job_id: job, channel_id: 'c1', channel_name: name } });
const sent = (job, name = 'Ops Slack') => ({ event_type: 'notification_sent', channel: 'slack', detail: JSON.stringify({ job_id: job, channel_id: 'c1', channel_name: name }) });
const failed = (job, name = 'Pager email', reason = 'Delivery failed: the mail server rejected the login.') =>
  ({ event_type: 'notification_failed', channel: 'smtp', detail: { job_id: job, channel_id: 'c2', channel_name: name, reason } });

test('sent events name the channel and its type', () => {
  const d = describeEvent(sent('j1'));
  assert.equal(d.label, 'Sent to Ops Slack (Slack)');
  assert.equal(d.tone, 'up');
  assert.equal(d.pill, 'Sent');
  assert.equal(d.channelId, 'c1');
});

test('failed events carry the sanitized reason without the lead-in', () => {
  const d = describeEvent(failed('j2'));
  assert.equal(d.label, 'Delivery to Pager email (Email) failed: The mail server rejected the login.');
  assert.equal(d.tone, 'down');
  assert.equal(d.pill, 'Failed');
  assert.equal(d.cause, 'The mail server rejected the login.');
  assert.equal(d.reason, '');
  assert.equal(describeEvent({ event_type: 'notification_failed', channel: 'webhook' }).label, 'Delivery to Webhook failed: Every retry failed.');
});

test('failed events link to the channel row on the channel list', () => {
  assert.equal(describeEvent(failed('j2')).channelHref, '/settings/channels?channel=c2');
  assert.equal(describeEvent({ event_type: 'notification_failed', channel: 'webhook' }).channelHref, '');
  // The id comes from stored event detail; it is encoded, never spliced raw.
  assert.equal(channelSettingsHref('a&b=1#x'), '/settings/channels?channel=a%26b%3D1%23x');
  assert.equal(channelSettingsHref(42), '');
  assert.equal(describeEvent(sent('j1')).channelHref, undefined);
});

test('the failure banner links to the one failing channel, else the list', () => {
  const one = deliverySummary([failed('j1'), failed('j2')]);
  assert.deepEqual(one.failedChannelIds, ['c2']);
  assert.deepEqual(failureSummaryLink(one), { href: '/settings/channels?channel=c2', text: 'Check this channel' });
  const other = { event_type: 'notification_failed', channel: 'webhook', detail: { job_id: 'j3', channel_id: 'c9', channel_name: 'Ops webhook' } };
  const two = deliverySummary([failed('j1'), other]);
  assert.deepEqual(failureSummaryLink(two), { href: '/settings/channels', text: 'Check notification channels' });
  assert.deepEqual(failureSummaryLink(deliverySummary([{ event_type: 'notification_failed', channel: 'smtp' }])).href, '/settings/channels');
  assert.equal(failureSummaryLink(null).href, '/settings/channels');
});

test('queued events say sending, not sent', () => {
  const d = describeEvent(queued('j3'));
  assert.equal(d.label, 'Sending to Ops Slack (Slack)');
  assert.equal(d.pill, 'Sending');
});

test('legacy notified rows do not claim delivery', () => {
  const d = describeEvent({ event_type: 'notified', channel: 'telnyx_sms' });
  assert.equal(d.label, 'Queued for SMS');
  assert.doesNotMatch(d.label, /sent|delivered/i);
  assert.match(d.reason, /not recorded/);
});

test('core and unknown types read as text', () => {
  assert.equal(describeEvent({ event_type: 'triggered' }).label, 'Alert triggered');
  assert.equal(describeEvent({ event_type: 'resolved' }).tone, 'up');
  assert.equal(describeEvent({ event_type: 'some_new.thing' }).label, 'Some new thing');
  assert.equal(humanizeType(''), 'Event');
  assert.equal(describeEvent({ event_type: 'notification_sent', detail: 'not json' }).label, 'Sent to a channel');
});

test('collapse hides queued rows that have an outcome', () => {
  const events = [{ event_type: 'triggered' }, queued('j1'), queued('j2'), queued('j3'), sent('j1'), failed('j2')];
  const out = collapseDeliveryEvents(events);
  assert.deepEqual(out.map(e => e.event_type), ['triggered', 'notification_queued', 'notification_sent', 'notification_failed']);
  assert.equal(out[1].detail.job_id, 'j3');
  assert.deepEqual(collapseDeliveryEvents(null), []);
});

test('summary counts final outcomes and names failing channels once', () => {
  const events = [queued('j1'), sent('j1'), failed('j2'), failed('j4'), failed('j5', 'Ops webhook'), queued('j6')];
  const s = deliverySummary(events);
  assert.equal(s.sent, 1);
  assert.equal(s.failed, 3);
  assert.equal(s.pending, 1);
  assert.deepEqual(s.failedChannels, ['Pager email', 'Ops webhook']);
  assert.equal(failureSummaryText(s), '3 notifications could not be sent (Pager email, Ops webhook).');
  assert.equal(failureSummaryText({ failed: 1, failedChannels: [] }), '1 notification could not be sent.');
  assert.equal(failureSummaryText({ failed: 5, failedChannels: ['a', 'b', 'c', 'd', 'e'] }), '5 notifications could not be sent (a, b, c and 2 more).');
  assert.equal(failureSummaryText(deliverySummary([sent('j1')])), '');
});

test('acks from a signed link say so and name the channel', () => {
  const d = describeEvent({ event_type: 'ack', channel: 'link', detail: { via: 'link', channel_id: 'c2', channel_name: 'Pager email' } });
  assert.equal(d.label, 'Acknowledged via link from Pager email');
  assert.equal(d.tone, 'warning');
  assert.equal(describeEvent({ event_type: 'ack', detail: '{"via":"link"}' }).label, 'Acknowledged via link');
  assert.equal(describeEvent({ event_type: 'ack', channel: 'slack' }).label, 'Acknowledged');
});

test('ackSummary names the person and the link channel', async () => {
  const { ackSummary } = await import('./timeline-logic.mjs');
  const alert = { status: 'acknowledged', acknowledged_at: '2026-11-24T10:00:00Z', acknowledged_by_name: 'Jane' };
  const events = [
    { event_type: 'triggered' },
    { event_type: 'ack', detail: JSON.stringify({ via: 'link', channel_id: 'c2', channel_name: 'Pager email' }) },
  ];
  assert.deepEqual(ackSummary(alert, events), { text: 'Acknowledged by Jane via link from Pager email', at: alert.acknowledged_at, viaLink: true });
});

test('ackSummary for a channel-wide link ack has no person', async () => {
  const { ackSummary } = await import('./timeline-logic.mjs');
  const alert = { acknowledged_at: '2026-11-24T10:00:00Z' };
  const s = ackSummary(alert, [{ event_type: 'ack', detail: { via: 'link', channel_name: 'Ops SMS' } }]);
  assert.equal(s.text, 'Acknowledged via link from Ops SMS');
});

test('ackSummary for an app ack and an unacked alert', async () => {
  const { ackSummary } = await import('./timeline-logic.mjs');
  assert.equal(ackSummary({ acknowledged_at: 'x', acknowledged_by_name: 'sam@example.com' }, [{ event_type: 'ack' }]).text, 'Acknowledged by sam@example.com');
  assert.equal(ackSummary({ acknowledged_at: 'x' }, []).text, 'Acknowledged');
  assert.equal(ackSummary({ status: 'firing' }, []), null);
  assert.equal(ackSummary(null, []), null);
});

test('isLiveAlertUpdate matches the viewed alert', async () => {
  const { isLiveAlertUpdate } = await import('./timeline-logic.mjs');
  const alert = { id: 'a1', monitor_id: 'm1' };
  assert.equal(isLiveAlertUpdate({ type: 'alert.acknowledged', alert_id: 'a1', monitor_id: 'm1' }, alert), true);
  assert.equal(isLiveAlertUpdate({ type: 'alert.acknowledged', alert_id: 'a2', monitor_id: 'm1' }, alert), false);
  assert.equal(isLiveAlertUpdate({ type: 'alert.resolved', monitor_id: 'm1' }, alert), true);
  assert.equal(isLiveAlertUpdate({ type: 'alert.resolved', monitor_id: 'm9' }, alert), false);
  assert.equal(isLiveAlertUpdate({ type: 'monitor.status_changed', monitor_id: 'm1' }, alert), false);
  assert.equal(isLiveAlertUpdate({ type: 'alert.fired', monitor_id: 'm9' }), true);
  assert.equal(isLiveAlertUpdate(null), false);
});

test('liveStatusText announces status changes only', async () => {
  const { liveStatusText } = await import('./timeline-logic.mjs');
  assert.equal(liveStatusText('firing', 'acknowledged'), 'This alert was acknowledged.');
  assert.equal(liveStatusText('acknowledged', 'resolved'), 'This alert was resolved.');
  assert.equal(liveStatusText('firing', 'firing'), '');
  assert.equal(liveStatusText(undefined, 'firing'), '');
});

test('plan gate and quota skips read as not sent, with a link to the plan', () => {
  const pro = describeEvent(failed('j9', 'Pager', 'requires Pro'));
  assert.equal(pro.pill, 'Skipped');
  assert.equal(pro.label, 'Not sent to Pager (Email): This channel type needs Pro and the grace period after the paid plan ended has passed.');
  assert.equal(pro.actionHref, '/settings/billing');
  assert.equal(pro.actionText, 'Upgrade to Pro');
  assert.equal(pro.channelHref, '');

  const quota = describeEvent({ event_type: 'notification_failed', channel: 'telnyx_sms', detail: { channel_id: 'c1', channel_name: 'On-call', reason: 'SMS quota reached (100 of 100 this billing period)' } });
  assert.equal(quota.label, 'Not sent to On-call (SMS): SMS quota reached (100 of 100 this billing period).');
  assert.equal(quota.actionText, 'View plan usage');

  const check = describeEvent({ event_type: 'notification_failed', channel: 'telegram', detail: { reason: 'plan could not be checked' } });
  assert.equal(check.label, 'Not sent to Telegram: The plan or quota could not be checked at the time.');
  assert.equal(check.actionHref, '');

  // A real send failure keeps its wording and channel link.
  const real = describeEvent(failed('j8', 'Pager', 'Delivery failed: timeout'));
  assert.equal(real.pill, 'Failed');
  assert.equal(real.actionHref, undefined);
  // Skips still count as not delivered in the summary.
  assert.equal(deliverySummary([failed('j9', 'Pager', 'requires Pro')]).failed, 1);
});

test('voice-call acks say they came by phone', async () => {
  const { ackSummary } = await import('./timeline-logic.mjs');
  const evt = { event_type: 'ack', channel: 'voice', detail: '{"via":"voice","channel_id":"","channel_name":""}' };
  assert.equal(describeEvent(evt).label, 'Acknowledged by phone call');
  const s = ackSummary({ acknowledged_at: '2026-11-24T10:00:00Z' }, [evt]);
  assert.equal(s.text, 'Acknowledged by phone call');
  assert.equal(s.viaLink, false);
});

test('a named voice-call ack reads as one sentence', async () => {
  const { ackSummary } = await import('./timeline-logic.mjs');
  const evt = { event_type: 'ack', channel: 'voice', detail: '{"via":"voice"}' };
  const s = ackSummary({ acknowledged_at: '2026-11-24T10:00:00Z', acknowledged_by_name: 'Sam Lee' }, [evt]);
  assert.equal(s.text, 'Acknowledged by Sam Lee on a phone call');
});
