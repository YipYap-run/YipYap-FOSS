// Checks the ack and resolve wording on the alert timeline against the
// events written by the escalation engine (engine.go handleAck and
// handleRecover), the web handlers (alerts.go) and the Slack app button
// handler (slack_app_actions_full.go). Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usersById, ackResolveLine, ackResolveText } from '../src/pages/alerts/timeline-actor.mjs';

const users = usersById([
  { id: 'u1', email: 'sam@example.com', name: 'Sam Lee' },
  { id: 'u2', email: 'kim@example.com' },
]);

const text = (evt, u = users) => ackResolveText(ackResolveLine(evt, u));

test('Slack button ack names the matched YipYap user and Slack', () => {
  assert.equal(text({ event_type: 'ack', channel: 'slack', target_user_id: 'u1' }), 'Acknowledged in Slack by Sam Lee');
  assert.equal(text({ event_type: 'resolved', channel: 'slack', target_user_id: 'u2' }), 'Resolved in Slack by kim@example.com');
});

test('Slack fallback actor is shown as given', () => {
  assert.equal(text({ event_type: 'resolved', channel: 'slack', target_user_id: 'Slack user jane' }), 'Resolved in Slack by Slack user jane');
});

test('unknown IDs are never shown raw', () => {
  assert.equal(text({ event_type: 'ack', channel: 'slack', target_user_id: '01JABCDEF' }), 'Acknowledged in Slack');
  assert.equal(text({ event_type: 'ack', target_user_id: 'u1' }, new Map()), 'Acknowledged');
});

test('web ack and automatic resolve', () => {
  assert.equal(text({ event_type: 'ack', target_user_id: 'u1' }), 'Acknowledged by Sam Lee');
  assert.equal(text({ event_type: 'resolved' }), 'Resolved automatically when the monitor recovered');
  assert.equal(text({ event_type: 'resolved', channel: 'telegram' }), 'Resolved in Telegram');
});

test('other event types are left to the generic row', () => {
  assert.equal(ackResolveLine({ event_type: 'notified', channel: 'slack' }, users), null);
  assert.equal(ackResolveLine(null, users), null);
});

test('usersById accepts an array or { users }', () => {
  assert.equal(usersById({ users: [{ id: 'a' }] }).size, 1);
  assert.equal(usersById(null).size, 0);
});
