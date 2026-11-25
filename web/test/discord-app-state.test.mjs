// Checks the Discord app UI helpers against the API contract in
// internal/api/handlers/discord_app_full.go and
// notification_channels_discordapp_full.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  STATUS_PARAM,
  DISCORD_APP_TYPE,
  statusFromSearch,
  searchWithoutStatus,
  shouldShowSection,
  isDiscordAuthorizeURL,
  serverName,
  removePrompt,
  interpretInstallError,
  interpretRemoveError,
  discordAppConfig,
  withInstallation,
  withChannel,
  validateDiscordAppConfig,
  channelOptions,
  discordAppSummary,
  interpretChannelsError,
  discordAppChannelHealth,
  channelsUsingInstall,
  offerDiscordAppType,
  noServerMessage,
  installGoneError,
  usageLine,
  newChannelConfig,
  UNAVAILABLE,
} from '../src/pages/settings/discord-app-state.mjs';

function apiErr(status, message) {
  const e = new Error(message || 'HTTP ' + status);
  e.status = status;
  return e;
}

// Every status word discord_app_full.go finish() redirects with.
const CALLBACK_STATUSES = ['installed', 'cancelled', 'expired', 'forbidden', 'failed', 'unavailable'];

test('callback statuses map to messages; unknown values are ignored', () => {
  assert.equal(STATUS_PARAM, 'discord');
  for (const s of CALLBACK_STATUSES) {
    const n = statusFromSearch('?discord=' + s);
    assert.ok(n && n.text, s);
  }
  assert.equal(statusFromSearch('?discord=installed').tone, 'success');
  assert.equal(statusFromSearch('?discord=expired').tone, 'error');
  assert.equal(statusFromSearch('?discord=<b>pwned</b>'), null);
  assert.equal(statusFromSearch('?slack=installed'), null);
  assert.equal(searchWithoutStatus('?discord=installed&channel=c1'), '?channel=c1');
  assert.equal(searchWithoutStatus('?discord=installed'), '');
});

test('section shows when available or servers remain', () => {
  assert.equal(shouldShowSection(null), false);
  assert.equal(shouldShowSection({ available: false, installations: [] }), false);
  assert.equal(shouldShowSection({ available: true, installations: [] }), true);
  assert.equal(shouldShowSection({ available: false, installations: [{ id: 'i' }] }), true);
});

test('only Discord consent URLs are followed', () => {
  assert.ok(isDiscordAuthorizeURL('https://discord.com/oauth2/authorize?client_id=1&state=x'));
  assert.ok(isDiscordAuthorizeURL('https://discord.com/api/oauth2/authorize?client_id=1'));
  for (const bad of [
    'http://discord.com/oauth2/authorize',
    'https://discord.com.evil.test/oauth2/authorize',
    'https://evil.test/oauth2/authorize',
    'https://user:pw@discord.com/oauth2/authorize',
    'https://discord.com/channels/1',
    'javascript:alert(1)',
    '',
    null,
  ]) {
    assert.equal(isDiscordAuthorizeURL(bad), false, String(bad));
  }
});

test('names, remove prompt and error text', () => {
  assert.equal(serverName({ guild_name: ' Ops ', guild_id: '1' }), 'Ops');
  assert.equal(serverName({ guild_name: '', guild_id: '123' }), '123');
  const p = removePrompt({ guild_name: 'Ops' }, ['a', 'b', 'c', 'd']);
  assert.match(p.body, /4 channels post here \(a, b, c and 1 more\)/);
  assert.match(p.body, /bot also leaves it/);
  assert.match(interpretInstallError(apiErr(403)), /owners and admins/);
  assert.match(interpretInstallError(apiErr(503)), /not available/);
  assert.match(interpretRemoveError(apiErr(502)), /did not confirm/);
  assert.match(interpretChannelsError(apiErr(409, 'The YipYap bot is no longer in this Discord server. Connect the server again.')), /no longer in this Discord server/);
  assert.match(interpretChannelsError(apiErr(404)), /no longer connected/);
});

test('channel config keeps only the three API fields', () => {
  assert.deepEqual(discordAppConfig({ installation_id: ' i1 ', channel_id: ' 200000000000000001 ', channel_name: '#alerts', bot_token: 'x' }),
    { installation_id: 'i1', channel_id: '200000000000000001', channel_name: 'alerts' });
  assert.deepEqual(withInstallation({ installation_id: 'i1', channel_id: '2', channel_name: 'a' }, 'i2'),
    { installation_id: 'i2', channel_id: '', channel_name: '' });
  const chans = [{ id: '200000000000000001', name: 'alerts' }];
  assert.equal(withChannel({ installation_id: 'i1' }, chans, '200000000000000001').channel_name, 'alerts');
});

test('validation matches the server: server required, snowflake channel', () => {
  assert.deepEqual(Object.keys(validateDiscordAppConfig({})).sort(), ['channel_id', 'installation_id']);
  assert.ok(validateDiscordAppConfig({ installation_id: 'i', channel_id: 'general' }).channel_id);
  assert.deepEqual(validateDiscordAppConfig({ installation_id: 'i', channel_id: '200000000000000001' }), {});
});

test('channel options keep a saved channel missing from the list', () => {
  const opts = channelOptions([{ id: '200000000000000002', name: 'ops' }], { channel_id: '200000000000000001', channel_name: 'old' });
  assert.equal(opts[0].label, '#old (not found in Discord)');
  assert.equal(opts[1].label, '#ops');
});

test('summary, health and usage use the install list', () => {
  const data = { installations: [{ id: 'i1', guild_name: 'Acme' }] };
  const ch = { type: DISCORD_APP_TYPE, name: 'Ops', config: { installation_id: 'i1', channel_id: '2', channel_name: 'alerts' } };
  assert.equal(discordAppSummary(ch.config, data.installations), '#alerts in Acme');
  assert.equal(discordAppChannelHealth(ch, data), 'ok');
  assert.equal(discordAppChannelHealth(ch, null), 'unknown');
  assert.equal(discordAppChannelHealth({ ...ch, config: { installation_id: 'gone' } }, data), 'disconnected');
  assert.equal(discordAppChannelHealth({ type: 'discord', config: {} }, data), 'ok');
  assert.deepEqual(channelsUsingInstall([ch, { type: 'slack_app', config: { installation_id: 'i1' } }], 'i1').map(c => c.name), ['Ops']);
});

test('type is offered when the app is set up or a server is connected', () => {
  assert.equal(offerDiscordAppType(null, false), true);
  assert.equal(offerDiscordAppType({ available: false, installations: [] }, false), false);
  assert.equal(offerDiscordAppType({ available: false, installations: [] }, true), true);
  assert.equal(offerDiscordAppType({ available: true, installations: [] }, false), true);
  assert.match(noServerMessage({ available: false }, true), /not available/);
  assert.match(noServerMessage({ available: true }, false), /Ask an owner/);
});

test('availability copy never calls the YipYap host a server', () => {
  // In this UI "server" means a Discord server. A 503 or a switched-off app
  // must not read as "this Discord server is not set up".
  const texts = [
    UNAVAILABLE,
    statusFromSearch('?discord=unavailable').text,
    interpretInstallError(apiErr(503)),
    interpretInstallError(apiErr(404)),
    interpretChannelsError(apiErr(503)),
    noServerMessage({ available: false, installations: [] }, true),
  ];
  for (const t of texts) assert.doesNotMatch(t, /this server/i, t);
});

test('the connected notice offers the next step', () => {
  const n = statusFromSearch('?discord=installed');
  assert.equal(n.tone, 'success');
  assert.equal(n.action, 'add-channel');
  assert.match(n.text, /Discord \(YipYap app\)/);
  assert.equal(statusFromSearch('?discord=failed').action, undefined);
});

test('installGoneError: 404 and 409 from the channel list mean the install is gone', () => {
  assert.equal(installGoneError(apiErr(404)), true);
  assert.equal(installGoneError(apiErr(409)), true);
  assert.equal(installGoneError(apiErr(502)), false);
  assert.equal(installGoneError(apiErr(403)), false);
  assert.equal(installGoneError(null), false);
});

test('usageLine counts the channels that post to a server', () => {
  assert.equal(usageLine(0), 'No channel posts here yet');
  assert.equal(usageLine(undefined), 'No channel posts here yet');
  assert.equal(usageLine(1), 'Used by 1 channel');
  assert.equal(usageLine(4), 'Used by 4 channels');
});

test('newChannelConfig presets only the install and passes validation once a channel is picked', () => {
  assert.deepEqual(newChannelConfig(' inst-1 '), { installation_id: 'inst-1', channel_id: '', channel_name: '' });
  assert.deepEqual(newChannelConfig(''), { installation_id: '', channel_id: '', channel_name: '' });
  const errs = validateDiscordAppConfig(newChannelConfig('inst-1'));
  assert.equal(errs.installation_id, undefined);
  assert.equal(errs.channel_id, 'Choose a channel');
  // The preset stays when the same server is picked again.
  assert.equal(withInstallation(newChannelConfig('inst-1'), 'inst-1').installation_id, 'inst-1');
});
