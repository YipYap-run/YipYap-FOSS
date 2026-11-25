// Run with: cd web && npm test
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  splitRecipients, addRecipients, isLikelyEmail, validateEmailConfig,
  emailConfigBody, parseEmailConfigError,
} from './email-channel-logic.mjs';

test('splitRecipients accepts the separators the API accepts', () => {
  assert.deepEqual(splitRecipients('a@x.io, b@x.io;c@x.io\nd@x.io'), ['a@x.io', 'b@x.io', 'c@x.io', 'd@x.io']);
  assert.deepEqual(splitRecipients(['a@x.io', ' b@x.io ']), ['a@x.io', 'b@x.io']);
  assert.deepEqual(splitRecipients(undefined), []);
});

test('addRecipients drops case-insensitive duplicates', () => {
  assert.deepEqual(addRecipients(['A@x.io'], ['a@x.io', 'Ops <b@x.io>', 'b@X.io']), ['A@x.io', 'Ops <b@x.io>']);
});

test('isLikelyEmail', () => {
  assert.ok(isLikelyEmail('ops@company.com'));
  assert.ok(isLikelyEmail('On call <ops@company.com>'));
  assert.ok(!isLikelyEmail('ops@company'));
  assert.ok(!isLikelyEmail('ops company.com'));
  assert.ok(!isLikelyEmail('ops@company.com\r\nBcc: x@y.io'));
});

test('platform mode only checks recipients', () => {
  assert.deepEqual(validateEmailConfig({ mode: 'platform', to: 'a@x.io' }), {});
  assert.equal(validateEmailConfig({ mode: 'platform', to: '' }).to, 'Add at least one recipient');
  const eleven = Array.from({ length: 11 }, (_, i) => `u${i}@x.io`).join(',');
  assert.match(validateEmailConfig({ to: eleven }).to, /Up to 10/);
});

test('smtp mode requires every server field', () => {
  const errs = validateEmailConfig({ mode: 'smtp', to: 'a@x.io' });
  for (const k of ['host', 'port', 'tls_mode', 'username', 'password', 'from']) {
    assert.ok(errs[k], `${k} should be required`);
  }
  const ok = { mode: 'smtp', to: 'a@x.io', host: 'smtp.x.io', port: 587, tls_mode: 'starttls', username: 'u', password: 'p', from: 'a@x.io' };
  assert.deepEqual(validateEmailConfig(ok), {});
  assert.ok(validateEmailConfig({ ...ok, port: 70000 }).port);
  assert.ok(validateEmailConfig({ ...ok, host: 'smtp://x.io' }).host);
});

test('emailConfigBody sends only the keys the mode allows', () => {
  const cfg = { mode: 'platform', to: 'a@x.io;b@x.io', host: 'evil.example', password: 'x' };
  assert.deepEqual(emailConfigBody(cfg), { mode: 'platform', to: 'a@x.io, b@x.io' });
  const smtp = emailConfigBody({ ...cfg, mode: 'smtp', port: '465', tls_mode: 'tls', username: 'u', from: 'a@x.io' });
  assert.equal(smtp.port, 465);
  assert.equal(smtp.host, 'evil.example');
});

test('parseEmailConfigError maps API errors to fields', () => {
  assert.deepEqual(parseEmailConfigError('invalid email channel config: host is required'), { field: 'host', message: 'Required' });
  assert.deepEqual(parseEmailConfigError('invalid email channel config: from is not a valid email address'),
    { field: 'from', message: 'Not a valid email address' });
  assert.deepEqual(parseEmailConfigError('invalid email channel config: port must be between 1 and 65535'),
    { field: 'port', message: 'Must be between 1 and 65535' });
  assert.equal(parseEmailConfigError('invalid email channel config: tls_mode is required').field, 'tls_mode');
  assert.equal(parseEmailConfigError('invalid email channel config: config must be a JSON object').field, null);
  assert.equal(parseEmailConfigError('name is required'), null);
  assert.equal(parseEmailConfigError(undefined), null);
});

test('parseEmailConfigError rewords the missing platform mailer error', () => {
  const msg = 'invalid email channel config: mode "platform" is not available: this server has no email service configured, so use your own SMTP server';
  const got = parseEmailConfigError(msg);
  assert.equal(got.field, 'mode');
  assert.equal(got.platformUnavailable, true);
  assert.ok(!got.message.includes('"platform"'), got.message);
});
