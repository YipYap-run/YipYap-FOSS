// Checks the "Slack (YipYap app)" channel form helpers against the API
// contract in internal/api/handlers/notification_channels_slackapp_full.go
// and slack_app_channels_full.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  SLACK_APP_TYPE,
  slackAppConfig,
  withInstallation,
  withChannel,
  validateSlackAppConfig,
  channelLabel,
  channelOptions,
  slackAppSummary,
  interpretChannelsError,
} from '../src/pages/settings/slack-app-channel-state.mjs';

function apiErr(status, message) {
  const e = new Error(message || 'HTTP ' + status);
  e.status = status;
  return e;
}

test('type name matches the server channel type', () => {
  assert.equal(SLACK_APP_TYPE, 'slack_app');
});

test('config sent to the API has only the three known fields', () => {
  // The server rejects unknown fields, so form leftovers must not leak.
  const out = slackAppConfig({ installation_id: ' i1 ', channel_id: ' C123 ', channel_name: '#ops', _formatManual: true, bot_token: 'x' });
  assert.deepEqual(out, { installation_id: 'i1', channel_id: 'C123', channel_name: 'ops' });
});

test('changing the workspace clears the channel', () => {
  const cfg = { installation_id: 'i1', channel_id: 'C123', channel_name: 'ops' };
  assert.deepEqual(withInstallation(cfg, 'i2'), { installation_id: 'i2', channel_id: '', channel_name: '' });
  assert.deepEqual(withInstallation(cfg, 'i1'), cfg);
});

test('picking a channel stores its name for the list summary', () => {
  const channels = [{ id: 'C1', name: 'ops' }, { id: 'G2', name: 'secret', is_private: true }];
  assert.deepEqual(withChannel({ installation_id: 'i1' }, channels, 'G2'),
    { installation_id: 'i1', channel_id: 'G2', channel_name: 'secret' });
});

test('validation mirrors the server checks', () => {
  assert.deepEqual(Object.keys(validateSlackAppConfig({})).sort(), ['channel_id', 'installation_id']);
  assert.ok(validateSlackAppConfig({ installation_id: 'i1', channel_id: 'general' }).channel_id);
  assert.deepEqual(validateSlackAppConfig({ installation_id: 'i1', channel_id: 'C0123ABC' }), {});
});

test('channel options keep a saved channel that Slack no longer lists', () => {
  const opts = channelOptions([{ id: 'C1', name: 'ops' }], { channel_id: 'C9', channel_name: 'old' });
  assert.equal(opts[0].value, 'C9');
  assert.match(opts[0].label, /#old .*not found/);
  assert.equal(opts[1].label, '#ops');
  assert.equal(channelLabel({ id: 'G1', name: 'sec', is_private: true }), '#sec (private)');
});

test('summary names the channel and, when known, the workspace', () => {
  const cfg = { installation_id: 'i1', channel_id: 'C1', channel_name: 'ops' };
  assert.equal(slackAppSummary(cfg), '#ops');
  assert.equal(slackAppSummary(cfg, [{ id: 'i1', team_name: 'Acme' }]), '#ops in Acme');
  assert.equal(slackAppSummary({ installation_id: 'i1' }), '');
});

test('channel list errors map to sentences', () => {
  assert.match(interpretChannelsError(apiErr(404)), /no longer connected/);
  assert.match(interpretChannelsError(apiErr(409, 'Slack no longer accepts this workspace\'s connection. Connect the workspace again.')), /Connect the workspace again/);
  assert.match(interpretChannelsError(apiErr(403)), /owners and admins/);
  assert.match(interpretChannelsError(apiErr(503)), /not set up/);
  assert.match(interpretChannelsError(apiErr(502)), /Try again/);
});

test('disconnected workspace is flagged only once the list is in', async () => {
  const { slackAppChannelHealth } = await import('../src/pages/settings/slack-app-channel-state.mjs');
  const ch = { type: 'slack_app', config: { installation_id: 'i1', channel_id: 'C123' } };
  assert.equal(slackAppChannelHealth(ch, null), 'unknown');
  assert.equal(slackAppChannelHealth(ch, { installations: [{ id: 'i1' }] }), 'ok');
  assert.equal(slackAppChannelHealth(ch, { installations: [{ id: 'i2' }] }), 'disconnected');
  assert.equal(slackAppChannelHealth({ type: 'slack', config: {} }, { installations: [] }), 'ok');
});

test('channelsUsingInstall finds only slack_app channels on that install', async () => {
  const { channelsUsingInstall } = await import('../src/pages/settings/slack-app-channel-state.mjs');
  const list = [
    { name: 'ops', type: 'slack_app', config: { installation_id: 'i1' } },
    { name: 'dev', type: 'slack_app', config: { installation_id: 'i2' } },
    { name: 'byo', type: 'slack', config: { installation_id: 'i1' } },
  ];
  assert.deepEqual(channelsUsingInstall(list, 'i1').map(c => c.name), ['ops']);
  assert.deepEqual(channelsUsingInstall(list, ''), []);
});

test('Slack app type is offered only when it can work', async () => {
  const { offerSlackAppType, noWorkspaceMessage } = await import('../src/pages/settings/slack-app-channel-state.mjs');
  assert.equal(offerSlackAppType(null, false), true);
  assert.equal(offerSlackAppType({ available: true, installations: [] }, false), true);
  assert.equal(offerSlackAppType({ available: false, installations: [] }, false), false);
  assert.equal(offerSlackAppType({ available: false, installations: [{ id: 'i1' }] }, false), true);
  assert.equal(offerSlackAppType({ available: false, installations: [] }, true), true);
  assert.match(noWorkspaceMessage({ available: false, installations: [] }, true), /not set up/);
  assert.match(noWorkspaceMessage({ available: true, installations: [] }, true), /Add to Slack/);
  assert.match(noWorkspaceMessage({ available: true, installations: [] }, false), /Ask an owner/);
});
