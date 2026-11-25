// Checks the webhook signing UI helpers against the API contract in
// internal/api/handlers/notification_channels_signing.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createHmac } from 'node:crypto';
import {
  TIMESTAMP_HEADER,
  SIGNATURE_HEADER,
  REPLAY_WINDOW_MINUTES,
  isUnsignedWebhook,
  isSignedWebhook,
  withoutSecret,
  applyRotation,
  rotatePrompt,
  interpretRotateError,
  shouldWarnBeforeClose,
} from '../src/pages/settings/webhook-signing-state.mjs';

function apiErr(status, body = {}) {
  const e = new Error(body.error || 'HTTP ' + status);
  e.status = status;
  e.body = body;
  return e;
}

test('unsigned tag only for webhook channels the server reports as unsigned', () => {
  assert.equal(isUnsignedWebhook({ type: 'webhook', signing_secret_set: false }), true);
  assert.equal(isUnsignedWebhook({ type: 'webhook', signing_secret_set: true }), false);
  // GET omits the flag for other types; a missing flag is not "unsigned".
  assert.equal(isUnsignedWebhook({ type: 'slack' }), false);
  assert.equal(isUnsignedWebhook({ type: 'webhook' }), false);
  assert.equal(isUnsignedWebhook(null), false);
  assert.equal(isSignedWebhook({ type: 'webhook', signing_secret_set: true }), true);
  assert.equal(isSignedWebhook({ type: 'email', signing_secret_set: true }), false);
});

test('withoutSecret drops the one-time secret and keeps the rest', () => {
  const created = { id: 'c1', type: 'webhook', name: 'Ops', signing_secret: 'whsec_abc', signing_secret_set: true };
  const clean = withoutSecret(created);
  assert.equal('signing_secret' in clean, false);
  assert.equal(clean.signing_secret_set, true);
  assert.equal(clean.name, 'Ops');
  assert.equal(created.signing_secret, 'whsec_abc', 'input is not mutated');
});

test('applyRotation updates the channel in place without caching the secret', () => {
  const list = [
    { id: 'a', type: 'webhook', signing_secret_set: false },
    { id: 'b', type: 'email' },
  ];
  const next = applyRotation(list, { id: 'a', type: 'webhook', name: 'A', signing_secret: 'whsec_new' });
  assert.equal(next[0].signing_secret_set, true);
  assert.equal(next[0].name, 'A');
  assert.equal(JSON.stringify(next).includes('whsec_new'), false);
  assert.equal(next[1], list[1]);
  assert.equal(applyRotation(list, null), list);
});

test('rotate prompt warns that the old secret stops working', () => {
  const r = rotatePrompt(true);
  assert.match(r.body, /stops working right away/);
  assert.equal(r.danger, true);
  assert.equal(rotatePrompt(false).confirm, 'Generate secret');
  for (const p of [rotatePrompt(true), rotatePrompt(false)]) {
    assert.equal(/\u2014/.test(p.title + p.body), false, 'no em-dashes in UI copy');
  }
});

test('rotate errors map to the documented status codes', () => {
  assert.match(interpretRotateError(apiErr(403)), /owners and admins/);
  assert.match(interpretRotateError(apiErr(404)), /no longer exists/);
  assert.match(interpretRotateError(apiErr(400)), /webhook/);
  assert.equal(interpretRotateError(apiErr(500, { error: 'store down' })), 'store down');
  assert.match(interpretRotateError(apiErr(502)), /Try again/);
  assert.match(interpretRotateError(undefined), /Try again/);
});

test('closing asks once while an uncopied secret is on screen', () => {
  assert.equal(shouldWarnBeforeClose({ secret: 'whsec_x', copied: false }, false), true);
  assert.equal(shouldWarnBeforeClose({ secret: 'whsec_x', copied: false }, true), false);
  assert.equal(shouldWarnBeforeClose({ secret: 'whsec_x', copied: true }, false), false);
  assert.equal(shouldWarnBeforeClose(null, false), false);
});

// The "How to verify" steps in the UI name these headers and window. Check
// them against the scheme in docs/webhooks.md: v1=hex(HMAC-SHA256(secret,
// timestamp + "." + raw body)), keyed with the whole whsec_ string.
test('header names and the verify recipe match the documented scheme', () => {
  assert.equal(TIMESTAMP_HEADER, 'X-YipYap-Timestamp');
  assert.equal(SIGNATURE_HEADER, 'X-YipYap-Signature');
  assert.equal(REPLAY_WINDOW_MINUTES, 5);
  // Test vector from docs/webhooks.md (also checked in Go by
  // TestSignWebhook_TestVector).
  const secret = 'whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw';
  const ts = '1700000000';
  const body = '{"alert_id":"a1","status":"firing"}';
  const sig = createHmac('sha256', secret).update(ts + '.' + body).digest('hex');
  assert.equal('v1=' + sig, 'v1=5a83cb255e788cae0f2a49cd57a05c6af26aac65684d6fef44edc0770a4ad895');
});
