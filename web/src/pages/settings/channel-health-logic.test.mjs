// Run with: cd web && npm test
// Contract: internal/domain/notification.go ChannelHealth and the public
// failure classes in internal/notify/delivery_error.go.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { channelHealthView, deliveryReason, failingChannelsNote, linkedChannel, testSentMessage, testFilteredMessage, testFailureNote } from './channel-health-logic.mjs';

const NOW = Date.parse('2026-11-24T12:00:00Z');

test('deliveryReason drops the server lead-in', () => {
  assert.equal(deliveryReason('Delivery failed: the destination returned HTTP 500.'), 'The destination returned HTTP 500.');
  assert.equal(deliveryReason('Delivery failed. Check the channel settings and try again.'), 'Check the channel settings and try again.');
  assert.equal(deliveryReason('Something else'), 'Something else');
  assert.equal(deliveryReason(''), '');
  assert.equal(deliveryReason(undefined), '');
  assert.equal(deliveryReason({}), '');
});

test('unknown health shows nothing', () => {
  assert.equal(channelHealthView(undefined, NOW), null);
  assert.equal(channelHealthView({ status: 'unknown', consecutive_failures: 0 }, NOW), null);
});

test('failing health shows a badge, count, age and reason', () => {
  const v = channelHealthView({
    status: 'failing',
    consecutive_failures: 3,
    last_failure_at: '2026-11-24T11:55:00Z',
    last_error: 'Delivery failed: the mail server rejected the login.',
  }, NOW);
  assert.equal(v.status, 'failing');
  assert.equal(v.badge, 'Failing');
  assert.equal(v.detail, 'Last 3 deliveries failed (5 min ago). The mail server rejected the login.');
});

test('a single failure reads in the singular and tolerates a missing reason', () => {
  const v = channelHealthView({ status: 'failing', consecutive_failures: 1 }, NOW);
  assert.equal(v.detail, 'Last delivery failed.');
});

test('a failure count alone counts as failing', () => {
  assert.equal(channelHealthView({ status: 'ok', consecutive_failures: 2 }, NOW).status, 'failing');
});

test('ok health has no badge and shows the last send', () => {
  const v = channelHealthView({ status: 'ok', consecutive_failures: 0, last_success_at: '2026-11-22T12:00:00Z' }, NOW);
  assert.equal(v.badge, '');
  assert.equal(v.detail, 'Last sent 2 days ago');
});

test('failingChannelsNote names failing channels and fits the role', () => {
  const chans = [
    { name: 'Ops email', health: { status: 'failing', consecutive_failures: 2 } },
    { name: 'Slack', health: { status: 'ok', consecutive_failures: 0 } },
  ];
  assert.equal(failingChannelsNote(chans, { canManage: true, now: NOW }),
    'Ops email is failing to deliver alerts. Check its settings, then send a test.');
  assert.equal(failingChannelsNote(chans, { canManage: false, now: NOW }),
    'Ops email is failing to deliver alerts. Ask an owner or admin to check the settings.');
  chans[1].health = { status: 'failing', consecutive_failures: 1 };
  assert.match(failingChannelsNote(chans, { canManage: true, now: NOW }), /^2 channels are failing to deliver alerts: Ops email, Slack\. Check their/);
  assert.equal(failingChannelsNote([], { now: NOW }), '');
  assert.equal(failingChannelsNote(null), '');
});

test('failing row says when owners were emailed', () => {
  const now = Date.parse('2026-11-24T10:00:00Z');
  const base = { status: 'failing', consecutive_failures: 3, last_error: 'Delivery failed: the destination returned HTTP 404.', last_failure_at: '2026-11-24T09:58:00Z' };
  assert.ok(!channelHealthView(base, now).detail.includes('Owners were emailed'));
  assert.match(channelHealthView({ ...base, owners_notified_at: '2026-11-24T09:58:00Z' }, now).detail, /Owners were emailed\.$/);
});

test('linkedChannel reads the channel from the failure email link', () => {
  const list = [{ id: 'a1' }, { id: 'b2' }];
  assert.deepEqual(linkedChannel('?channel=b2', list), { id: 'b2', found: true });
  assert.deepEqual(linkedChannel('?channel=zz', list), { id: 'zz', found: false });
  assert.deepEqual(linkedChannel('?channel=a1', null), { id: 'a1', found: false });
  assert.equal(linkedChannel('', list), null);
  assert.equal(linkedChannel('?channel=', list), null);
  assert.equal(linkedChannel('?other=1', list), null);
  assert.equal(linkedChannel(undefined, list), null);
});

test('testSentMessage mentions the owner follow-up only when one was sent', () => {
  assert.equal(testSentMessage({ status: 'test notification sent', owners_notified: false }), 'Test notification sent');
  assert.equal(testSentMessage(null), 'Test notification sent');
  assert.match(testSentMessage({ owners_notified: true }), /owners are getting a follow-up email/);
});

test('testFilteredMessage explains a test the event filter dropped', () => {
  assert.equal(testFilteredMessage({ status: 'test notification sent' }), null);
  assert.equal(testFilteredMessage(null), null);
  const msg = testFilteredMessage({ status: 'filtered', filtered: true, event_type: 'run.yipyap.alert.fired.v1' });
  assert.match(msg, /^Nothing was sent/);
  assert.match(msg, /run\.yipyap\.alert\.fired\.v1/);
});

test('testFailureNote keeps the reason and stays until dismissed', () => {
  assert.deepEqual(testFailureNote('Delivery failed: the destination returned HTTP 500.'), {
    kind: 'failed', message: 'Test failed. The destination returned HTTP 500.', dismissible: true,
  });
  assert.equal(testFailureNote('').message, 'Test failed. Check the channel settings and try again.');
  assert.equal(testFailureNote(undefined).message, 'Test failed. Check the channel settings and try again.');
});
