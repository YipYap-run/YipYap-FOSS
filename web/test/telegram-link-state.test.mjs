// Checks the Telegram link helpers against the API contract in
// internal/api/handlers/telegram_link.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  isTelegramUnlinked,
  usesOwnBot,
  telegramChatLabel,
  telegramSummary,
  secondsLeft,
  formatMinutesLeft,
  formatCountdown,
  expiresSoon,
  EXPIRY_WARNING_SECONDS,
  isFinalStatus,
  statusMessage,
  linkCodeRequest,
  interpretLinkCodeError,
  safeTelegramLink,
  pollFailure,
  nameChangedSinceCode,
  NAME_MAX,
  POLL_FAILURES_BEFORE_NOTICE,
} from '../src/pages/settings/telegram-link-state.mjs';

function apiErr(status, body = {}) {
  const e = new Error(body.error || 'HTTP ' + status);
  e.status = status;
  e.body = body;
  return e;
}

test('a telegram channel without chat_id is unlinked', () => {
  assert.equal(isTelegramUnlinked({ type: 'telegram', config: {} }), true);
  assert.equal(isTelegramUnlinked({ type: 'telegram', config: { chat_id: ' ' } }), true);
  assert.equal(isTelegramUnlinked({ type: 'telegram', config: { chat_id: '-100' } }), false);
  // Older rows may hold a numeric chat_id.
  assert.equal(isTelegramUnlinked({ type: 'telegram', config: { chat_id: -100 } }), false);
  assert.equal(isTelegramUnlinked({ type: 'webhook', config: {} }), false);
  assert.equal(isTelegramUnlinked(null), false);
});

test('own bot channels are recognised by their (redacted) token', () => {
  assert.equal(usesOwnBot({ bot_token: '••••••••', chat_id: '-1' }), true);
  assert.equal(usesOwnBot({ chat_id: '-1' }), false);
});

test('the chat label prefers the title the bot stored', () => {
  assert.equal(telegramChatLabel({ chat_id: '-100', chat_title: 'Ops Room' }), 'Ops Room');
  assert.equal(telegramChatLabel({ chat_id: '-100' }), 'Chat -100');
  assert.equal(telegramChatLabel({}), '');
  assert.equal(telegramSummary({ chat_title: 'Ops Room', chat_id: '-1' }), 'Chat: Ops Room');
  assert.equal(telegramSummary({}), 'No chat linked');
});

test('time left counts down to the expires_at the API returns', () => {
  const now = Date.parse('2026-11-24T12:00:00Z');
  assert.equal(secondsLeft('2026-11-24T12:15:00Z', now), 900);
  assert.equal(secondsLeft('2026-11-24T11:59:00Z', now), 0);
  assert.equal(secondsLeft('garbage', now), 0);
  assert.equal(formatMinutesLeft(900), '15 minutes left');
  assert.equal(formatMinutesLeft(61), '2 minutes left');
  assert.equal(formatMinutesLeft(60), '1 minute left');
  assert.equal(formatMinutesLeft(30), 'less than a minute left');
  assert.equal(formatMinutesLeft(0), 'expired');
});

test('polling stops on linked, expired and failed', () => {
  assert.equal(isFinalStatus('pending'), false);
  for (const s of ['linked', 'expired', 'failed']) assert.equal(isFinalStatus(s), true);
  assert.equal(statusMessage('linked', 'Ops Room'), 'Linked to Ops Room.');
  assert.match(statusMessage('expired'), /expired/);
  assert.match(statusMessage('pending'), /Waiting/);
});

test('link code requests name a channel or carry the new name', () => {
  assert.deepEqual(linkCodeRequest({ channelId: 'c1', name: 'x' }), { purpose: 'channel', channel_id: 'c1' });
  assert.deepEqual(linkCodeRequest({ name: '  Ops  ' }), { purpose: 'channel', name: 'Ops' });
});

test('link code errors map to messages', () => {
  assert.equal(interpretLinkCodeError(apiErr(503, { error: 'telegram linking is not configured on this server' })).unavailable, true);
  assert.match(interpretLinkCodeError(apiErr(429)).message, /Too many/);
  assert.equal(interpretLinkCodeError(apiErr(400, { error: 'name is required' })).message, 'Name is required.');
  assert.equal(interpretLinkCodeError(apiErr(403, { error: 'notification channel limit reached (5). Upgrade your plan to add more' })).unavailable, true);
});

test('only https t.me links from the API are rendered', () => {
  assert.equal(safeTelegramLink('https://t.me/yipyap_bot?start=ABCD2345EF'), 'https://t.me/yipyap_bot?start=ABCD2345EF');
  assert.equal(safeTelegramLink('https://t.me/yipyap_bot?startgroup=ABCD2345EF'), 'https://t.me/yipyap_bot?startgroup=ABCD2345EF');
  assert.equal(safeTelegramLink('javascript:alert(1)'), '');
  assert.equal(safeTelegramLink('http://t.me/yipyap_bot'), '');
  assert.equal(safeTelegramLink('https://t.me.evil.example/x'), '');
  assert.equal(safeTelegramLink('https://user@t.me/x'), '');
  assert.equal(safeTelegramLink(''), '');
  assert.equal(safeTelegramLink(undefined), '');
});

test('a 404 poll stops the wait, other errors notice after a few', () => {
  const gone = pollFailure(apiErr(404, { error: 'link code not found' }), 1);
  assert.equal(gone.stop, true);
  assert.match(gone.message, /new code/);
  assert.deepEqual(pollFailure(apiErr(500), 1), { stop: false, message: '' });
  const net = new Error('Failed to fetch');
  assert.equal(pollFailure(net, POLL_FAILURES_BEFORE_NOTICE - 1).message, '');
  const late = pollFailure(net, POLL_FAILURES_BEFORE_NOTICE);
  assert.equal(late.stop, false);
  assert.match(late.message, /Still trying/);
});

test('a name edited after the code was made is flagged', () => {
  assert.equal(nameChangedSinceCode('Ops', 'Ops'), false);
  assert.equal(nameChangedSinceCode('Ops', ' Ops '), false);
  assert.equal(nameChangedSinceCode('Ops', 'Ops alerts'), true);
  assert.equal(nameChangedSinceCode('Ops', ''), true);
  // Relinking an existing channel sends no name.
  assert.equal(nameChangedSinceCode('', 'Anything'), false);
});

test('the name limit matches the server', () => {
  // telegramLinkMaxNameLen in internal/api/handlers/telegram_link.go.
  assert.equal(NAME_MAX, 100);
});

test('the page live region names the linked chat', async () => {
  const { linkedAnnouncement } = await import('../src/pages/settings/telegram-link-state.mjs');
  // GET /telegram/link-codes/{id} with status "linked" carries chat_title.
  assert.equal(linkedAnnouncement({ status: 'linked', channel_id: 'c1', chat_title: ' Ops Room ' }), 'Linked to Ops Room.');
  assert.equal(linkedAnnouncement({ status: 'linked', channel_id: 'c1' }), 'Chat linked.');
  assert.equal(linkedAnnouncement(null), 'Chat linked.');
});

test('the visible countdown is m:ss and never negative', () => {
  assert.equal(formatCountdown(900), '15:00');
  assert.equal(formatCountdown(299), '4:59');
  assert.equal(formatCountdown(65), '1:05');
  assert.equal(formatCountdown(9), '0:09');
  assert.equal(formatCountdown(0), '0:00');
  assert.equal(formatCountdown(-5), '0:00');
  assert.equal(formatCountdown(undefined), '0:00');
});

test('the countdown counts from expires_at in the 201 body', () => {
  const now = Date.parse('2026-11-24T10:00:00Z');
  const body = { expires_at: '2026-11-24T10:15:00Z' };
  assert.equal(formatCountdown(secondsLeft(body.expires_at, now)), '15:00');
  assert.equal(formatCountdown(secondsLeft(body.expires_at, now + 14 * 60 * 1000 + 1500)), '0:59');
});

test('the status line warns once in the last minute of a pending code', () => {
  assert.equal(EXPIRY_WARNING_SECONDS, 60);
  assert.equal(expiresSoon(61), false);
  assert.equal(expiresSoon(60), true);
  assert.equal(expiresSoon(1), true);
  assert.equal(expiresSoon(0), false, 'an expired code says expired, not soon');
  const early = statusMessage('pending', '', expiresSoon(120));
  const late = statusMessage('pending', '', expiresSoon(30));
  assert.notEqual(early, late);
  assert.match(late, /less than a minute/);
  // Every tick inside the last minute gives the same text, so the live
  // region speaks once.
  assert.equal(statusMessage('pending', '', expiresSoon(59)), statusMessage('pending', '', expiresSoon(2)));
  // The flag only changes pending text.
  assert.equal(statusMessage('linked', 'Ops', true), 'Linked to Ops.');
});
