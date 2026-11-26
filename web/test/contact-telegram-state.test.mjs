// Checks the Connect Telegram helpers against the API in
// internal/api/handlers/telegram_link_contact.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  CONTACT_LINK_REQUEST,
  ownBotIntegrations,
  telegramContactMode,
  isPlatformTelegramMethod,
  contactStatusMessage,
  interpretContactLinkError,
} from '../src/pages/settings/contact-telegram-state.mjs';

function apiErr(status, body = {}) {
  const e = new Error(body.error || 'HTTP ' + status);
  e.status = status;
  e.body = body;
  return e;
}

const ownBot = { id: 'c1', name: 'Our bot', type: 'telegram', enabled: true, own_bot: true };
const platformChannel = { id: 'c2', name: 'Ops chat', type: 'telegram', enabled: true };
const slack = { id: 'c3', name: 'Slack', type: 'slack', enabled: true };

test('the code request asks for a contact method', () => {
  assert.deepEqual(CONTACT_LINK_REQUEST, { purpose: 'contact_method' });
  assert.ok(Object.isFrozen(CONTACT_LINK_REQUEST));
});

test('ownBotIntegrations keeps only Telegram channels on their own bot', () => {
  assert.deepEqual(ownBotIntegrations({ integrations: [ownBot, platformChannel, slack] }), [ownBot]);
  assert.deepEqual(ownBotIntegrations({}), []);
  assert.deepEqual(ownBotIntegrations(null), []);
  assert.deepEqual(ownBotIntegrations({ integrations: [null, slack] }), []);
});

test('telegramContactMode prefers Connect Telegram', () => {
  assert.equal(telegramContactMode({ telegram_linking: true, integrations: [ownBot] }), 'connect');
  assert.equal(telegramContactMode({ telegram_linking: true, integrations: [] }), 'connect');
  // Without the YipYap bot, only an org bot leaves a way in.
  assert.equal(telegramContactMode({ telegram_linking: false, integrations: [ownBot, platformChannel] }), 'manual');
  assert.equal(telegramContactMode({ telegram_linking: false, integrations: [platformChannel] }), 'unavailable');
  assert.equal(telegramContactMode({ integrations: [] }), 'unavailable');
  assert.equal(telegramContactMode(null), 'unavailable');
});

test('isPlatformTelegramMethod', () => {
  assert.equal(isPlatformTelegramMethod({ type: 'telegram', config: { chat_id: '42' } }), true);
  assert.equal(isPlatformTelegramMethod({ type: 'telegram', config: { chat_id: '42', via_channel_id: '' } }), true);
  assert.equal(isPlatformTelegramMethod({ type: 'telegram', config: { chat_id: '42', via_channel_id: 'c1' } }), false);
  assert.equal(isPlatformTelegramMethod({ type: 'telegram' }), true);
  assert.equal(isPlatformTelegramMethod({ type: 'email', config: {} }), false);
  assert.equal(isPlatformTelegramMethod(null), false);
});

test('contactStatusMessage', () => {
  assert.equal(contactStatusMessage('linked', '@jane'), 'Connected to @jane.');
  assert.equal(contactStatusMessage('linked', ''), 'Connected.');
  assert.match(contactStatusMessage('expired'), /expired/);
  assert.match(contactStatusMessage('failed'), /reply in Telegram/);
  assert.match(contactStatusMessage('pending'), /^Waiting for the code in Telegram\.$/);
  assert.match(contactStatusMessage('pending', '', true), /less than a minute/);
});

test('interpretContactLinkError', () => {
  assert.deepEqual(interpretContactLinkError(apiErr(503, { error: 'telegram linking is not configured on this server' })).unavailable, true);
  assert.match(interpretContactLinkError(apiErr(503)).message, /not set up/);
  const limit = interpretContactLinkError(apiErr(409, { error: 'you can keep at most 20 contact methods' }));
  assert.deepEqual(limit, { message: 'You can keep at most 20 contact methods.', unavailable: true });
  const plan = interpretContactLinkError(apiErr(403, { error: 'Telegram contact methods require a paid plan', upgrade_required: true }));
  assert.deepEqual(plan, { message: 'Telegram contact methods require a paid plan.', unavailable: true });
  assert.deepEqual(interpretContactLinkError(apiErr(429)).unavailable, false);
  assert.match(interpretContactLinkError(apiErr(500)).message, /HTTP 500/);
  assert.match(interpretContactLinkError(new Error('')).message, /Try again/);
  assert.match(interpretContactLinkError(null).message, /Try again/);
});
