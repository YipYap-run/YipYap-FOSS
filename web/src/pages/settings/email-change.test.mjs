import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  emailChangeError,
  emailChangeSuccess,
  needsEmailVerification,
  resendOutcome,
  RESEND_COOLDOWN_SECS,
} from './email-change.mjs';

function apiErr(status, message, body = {}) {
  const e = new Error(message);
  e.status = status;
  e.body = body;
  return e;
}

// Status codes and error strings match TestChangeEmailErrorContract in
// internal/api/handlers/users_contact_test.go.
test('emailChangeError maps the PUT /auth/email contract', () => {
  assert.equal(emailChangeError(apiErr(409, 'email already in use')), 'Another account already uses that email address.');
  assert.equal(emailChangeError(apiErr(400, 'invalid email address')), 'Enter a valid email address, like name@example.com.');
  assert.equal(emailChangeError(apiErr(400, 'email address too long')), 'Enter a valid email address, like name@example.com.');
  assert.equal(emailChangeError(apiErr(401, 'invalid password')), 'Your current password is not correct.');
  assert.equal(emailChangeError(apiErr(403, 'MFA verification failed')), 'The MFA code is not correct.');
  assert.equal(emailChangeError(apiErr(500, '')), 'Failed to update email. Try again.');
});

test('emailChangeSuccess tells the user to verify when required', () => {
  assert.match(
    emailChangeSuccess({ user: { email: 'new@example.com' }, verification_required: true }, 'x@example.com'),
    /verification link to new@example\.com/,
  );
  assert.equal(
    emailChangeSuccess({ user: { email: 'new@example.com' }, verification_required: false }, 'x@example.com'),
    'Email updated. Sign in with new@example.com from now on.',
  );
  assert.match(emailChangeSuccess({ verification_required: true }, 'typed@example.com'), /typed@example\.com/);
});

test('needsEmailVerification follows email_verified_at', () => {
  assert.equal(needsEmailVerification({ id: 'u1', email: 'a@example.com' }), true);
  assert.equal(needsEmailVerification({ id: 'u1', email: 'a@example.com', email_verified_at: '2026-11-24T00:00:00Z' }), false);
  assert.equal(needsEmailVerification({ id: 'session', role: 'viewer', name: 'Read-only session' }), false);
  assert.equal(needsEmailVerification({ id: 'u2', role: 'viewer', name: 'Staff (read-only)', email: 'staff read-only session' }), false);
  assert.equal(needsEmailVerification(null), false);
});

test('resendOutcome maps POST /auth/resend-verification', () => {
  assert.deepEqual(resendOutcome({ status: 'sent' }, null).kind, 'ok');
  assert.equal(resendOutcome({ status: 'sent' }, null).cooldown, RESEND_COOLDOWN_SECS);
  assert.equal(resendOutcome({ status: 'already_verified' }, null).kind, 'verified');

  const wait = resendOutcome(null, apiErr(429, 'please wait', { retry_after: 12 }));
  assert.equal(wait.kind, 'wait');
  assert.equal(wait.cooldown, 12);
  assert.equal(resendOutcome(null, apiErr(429, 'x', {})).cooldown, RESEND_COOLDOWN_SECS);

  // No mailer: the route answers 404.
  const none = resendOutcome(null, apiErr(404, 'HTTP 404'));
  assert.equal(none.kind, 'unavailable');
  assert.equal(none.cooldown, 0);

  assert.equal(resendOutcome(null, apiErr(500, 'boom')).kind, 'error');
});
