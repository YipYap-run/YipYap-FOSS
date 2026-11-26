import { test } from 'node:test';
import assert from 'node:assert/strict';

import {
  viaOptions, viaValue, viaSource, applyVia, defaultVia, viaIntegration, fieldApplies,
  slackLookupResult, slackLookupError,
} from '../src/pages/settings/contact-chat-app-state.mjs';

// Shape of GET users/me/contact-method-options integrations.
const integrations = [
  { id: 'ch-1', name: 'Team Slack', type: 'slack', enabled: true, source: 'own_bot' },
  { id: 'ch-2', name: 'Old Slack', type: 'slack', enabled: false, source: 'own_bot' },
  { id: 'in-1', name: 'Acme', type: 'slack', enabled: true, source: 'app' },
  { id: 'in-2', name: 'Acme Discord', type: 'discord', enabled: true, source: 'app' },
  { id: 'ch-3', name: 'Alerts', type: 'telegram', enabled: true, source: 'own_bot', own_bot: true },
];

test('viaOptions lists app installs first and labels each sender', () => {
  const opts = viaOptions(integrations, 'slack');
  assert.deepEqual(opts.map(o => o.label), ['Slack app: Acme', 'Own bot: Team Slack', 'Own bot: Old Slack (disabled)']);
  assert.deepEqual(opts.map(o => o.value), ['app:in-1', 'channel:ch-1', 'channel:ch-2']);
  assert.equal(opts[2].enabled, false);
  assert.deepEqual(viaOptions(integrations, 'discord').map(o => o.label), ['Discord app: Acme Discord']);
  assert.deepEqual(viaOptions(undefined, 'slack'), []);
});

test('applyVia sets exactly one reference', () => {
  const base = { slack_user_id: 'U1', via_channel_id: 'ch-1' };
  const app = applyVia(base, 'app:in-1');
  assert.deepEqual(app, { slack_user_id: 'U1', via_install_id: 'in-1' });
  assert.equal(viaValue(app), 'app:in-1');
  assert.equal(viaSource(app), 'app');
  const own = applyVia(app, 'channel:ch-1');
  assert.deepEqual(own, { slack_user_id: 'U1', via_channel_id: 'ch-1' });
  assert.equal(viaSource(own), 'own_bot');
  assert.deepEqual(applyVia(own, ''), { slack_user_id: 'U1' });
  assert.equal(viaSource({}), '');
});

test('defaultVia picks the first enabled sender, app first', () => {
  assert.equal(defaultVia(integrations, 'slack'), 'app:in-1');
  assert.equal(defaultVia(integrations.filter(i => i.source !== 'app'), 'slack'), 'channel:ch-1');
  assert.equal(defaultVia([], 'discord'), '');
});

test('viaIntegration finds the sender of a stored config', () => {
  assert.equal(viaIntegration(integrations, { via_install_id: 'in-2' }).name, 'Acme Discord');
  assert.equal(viaIntegration(integrations, { via_channel_id: 'ch-1' }).name, 'Team Slack');
  // An app install ID never matches a channel reference.
  assert.equal(viaIntegration(integrations, { via_channel_id: 'in-1' }), undefined);
});

test('fieldApplies shows the ID field for the chosen sender only', () => {
  const userID = { key: 'user_id', via: 'app' };
  const dmChannel = { key: 'dm_channel_id', via: 'own_bot' };
  assert.equal(fieldApplies(userID, { via_install_id: 'in-2' }), true);
  assert.equal(fieldApplies(dmChannel, { via_install_id: 'in-2' }), false);
  assert.equal(fieldApplies(dmChannel, { via_channel_id: 'ch-9' }), true);
  assert.equal(fieldApplies({ key: 'slack_user_id' }, {}), true);
  assert.equal(fieldApplies(userID, {}), false);
});

test('slackLookupResult says who was found or which email missed', () => {
  assert.deepEqual(
    slackLookupResult({ found: true, slack_user_id: 'U0ME', name: 'jane', email: 'j@x.co' }, 'Acme'),
    { found: true, id: 'U0ME', message: 'Found you as @jane in Acme.' },
  );
  const miss = slackLookupResult({ found: false, email: 'j@x.co' }, 'Acme');
  assert.equal(miss.found, false);
  assert.match(miss.message, /No member of Acme uses j@x\.co\. Enter your Slack member ID/);
});

test('slackLookupError keeps the server reason', () => {
  assert.equal(slackLookupError({ status: 429 }), 'Too many lookups. Wait a minute, or enter your member ID below.');
  assert.equal(slackLookupError({ status: 409, body: { error: 'Ask an admin to reconnect it' } }), 'Ask an admin to reconnect it.');
  assert.match(slackLookupError({}), /Could not look you up/);
});
