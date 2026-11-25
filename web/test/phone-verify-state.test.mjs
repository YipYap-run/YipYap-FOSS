// Checks the phone verification UI helpers against the API contract in
// internal/api/handlers/phone_verify_full.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  isPhoneChannel,
  isUnverified,
  defaultMethod,
  normalizeCode,
  toSentence,
  formatWait,
  formatCountdown,
  interpretSendError,
  interpretConfirmError,
} from '../src/pages/settings/phone-verify-state.mjs';

// Mirrors the error the API client throws for a non-2xx response.
function apiErr(status, body = {}) {
  const e = new Error(body.error || 'HTTP ' + status);
  e.status = status;
  e.body = body;
  return e;
}

test('only phone types with status pending count as unverified', () => {
  assert.equal(isPhoneChannel('telnyx_sms'), true);
  assert.equal(isPhoneChannel('twilio_voice'), true);
  assert.equal(isPhoneChannel('webhook'), false);
  assert.equal(isUnverified({ type: 'telnyx_sms', verification: { status: 'pending' } }), true);
  assert.equal(isUnverified({ type: 'telnyx_sms', verification: { status: 'verified', verified_at: '2026-11-24T00:00:00Z' } }), false);
  // Non-phone channels omit the field entirely.
  assert.equal(isUnverified({ type: 'webhook' }), false);
  assert.equal(isUnverified(null), false);
});

test('voice channels default to a call', () => {
  assert.equal(defaultMethod('telnyx_voice'), 'voice');
  assert.equal(defaultMethod('twilio_voice'), 'voice');
  assert.equal(defaultMethod('telnyx_sms'), 'sms');
});

test('normalizeCode keeps six digits from pasted input', () => {
  assert.equal(normalizeCode('123 456'), '123456');
  assert.equal(normalizeCode('123-456-789'), '123456');
  assert.equal(normalizeCode('abc'), '');
});

test('server messages become sentences', () => {
  assert.equal(toSentence('the verification code has expired; request a new code'),
    'The verification code has expired. Request a new code.');
  assert.equal(toSentence(''), '');
});

test('wait and countdown formatting', () => {
  assert.equal(formatWait(1), '1 second');
  assert.equal(formatWait(45), '45 seconds');
  assert.equal(formatWait(61), '2 minutes');
  assert.equal(formatWait(3600), '1 hour');
  assert.equal(formatWait(86400), '24 hours');
  assert.equal(formatCountdown(60), '1:00');
  assert.equal(formatCountdown(9), '0:09');
});

test('send 429 carries the cooldown from retry_after_seconds', () => {
  const r = interpretSendError(apiErr(429, {
    error: 'a code was just sent; wait before requesting another',
    retry_after_seconds: 42,
  }));
  assert.equal(r.retryAfter, 42);
  assert.match(r.message, /^A code was just sent\. You can request another code in 42 seconds\.$/);
});

test('send 429 daily limit states the wait in hours', () => {
  const r = interpretSendError(apiErr(429, {
    error: 'too many codes sent to this number today; try again tomorrow',
    retry_after_seconds: 86400,
  }));
  assert.equal(r.retryAfter, 86400);
  assert.equal(r.message, 'Too many codes sent to this number today. You can request another code in 24 hours.');
});

test('send errors by status', () => {
  assert.equal(interpretSendError(apiErr(409, { error: 'this number is already verified' })).alreadyVerified, true);
  assert.equal(interpretSendError(apiErr(503, { error: 'phone verification is not available' })).unavailable, true);
  assert.match(interpretSendError(apiErr(502)).message, /Could not send the code/);
  assert.equal(interpretSendError(apiErr(400, { error: 'only US and Canada phone numbers (+1) are supported at this time' })).message,
    'Only US and Canada phone numbers (+1) are supported at this time.');
  assert.equal(interpretSendError(apiErr(500)).retryAfter, 0);
});

test('wrong code reports attempts left', () => {
  const r = interpretConfirmError(apiErr(422, { error: 'incorrect code', attempts_remaining: 3 }));
  assert.equal(r.attemptsRemaining, 3);
  assert.equal(r.needNewCode, false);
  assert.equal(r.message, 'That code is not correct. 3 attempts left.');
  assert.equal(interpretConfirmError(apiErr(422, { attempts_remaining: 1 })).message, 'That code is not correct. 1 attempt left.');
});

test('last wrong code and lockout require a new code', () => {
  assert.equal(interpretConfirmError(apiErr(422, { attempts_remaining: 0 })).needNewCode, true);
  const locked = interpretConfirmError(apiErr(429, { error: 'too many incorrect codes; request a new code', attempts_remaining: 0 }));
  assert.equal(locked.needNewCode, true);
  assert.equal(locked.attemptsRemaining, 0);
});

test('expired, missing and malformed codes', () => {
  assert.equal(interpretConfirmError(apiErr(410)).needNewCode, true);
  assert.equal(interpretConfirmError(apiErr(409)).needNewCode, true);
  const bad = interpretConfirmError(apiErr(400, { error: 'enter the 6-digit code' }));
  assert.equal(bad.needNewCode, false);
  assert.equal(bad.message, 'Enter the 6-digit code.');
});
