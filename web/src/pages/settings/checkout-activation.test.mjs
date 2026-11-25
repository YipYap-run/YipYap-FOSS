import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  FAST_POLL_MS,
  SLOW_POLL_MS,
  SLOW_POLL_ATTEMPTS,
  pollDelay,
  isStripePlanActive,
  safeRedirectURL,
  stripCheckoutMarker,
  checkAgainMessage,
} from './checkout-activation.mjs';

// Plan values match TestBillingGetReflectsCheckoutActivation and
// TestBillingGetReportsPromoPlan in internal/api/handlers/billing_get_test.go.
test('isStripePlanActive only accepts a plan bought through checkout', () => {
  assert.equal(isStripePlanActive(null), false);
  assert.equal(isStripePlanActive({}), false);
  assert.equal(isStripePlanActive({ plan: 'free' }), false);
  assert.equal(isStripePlanActive({ plan: 'promo' }), false);
  assert.equal(isStripePlanActive({ plan: '' }), false);
  assert.equal(isStripePlanActive({ plan: 'pro' }), true);
  assert.equal(isStripePlanActive({ plan: 'enterprise' }), true);
});

test('pollDelay runs a fast phase, then a slow background phase, then stops', () => {
  const fast = FAST_POLL_MS.reduce((a, b) => a + b, 0);
  assert.ok(fast >= 60000 && fast <= 120000, `fast phase is ${fast}ms`);
  for (let i = 0; i < FAST_POLL_MS.length; i++) {
    assert.deepEqual(pollDelay(i), { ms: FAST_POLL_MS[i], slow: false });
  }
  assert.deepEqual(pollDelay(FAST_POLL_MS.length), { ms: SLOW_POLL_MS, slow: true });
  const last = FAST_POLL_MS.length + SLOW_POLL_ATTEMPTS - 1;
  assert.deepEqual(pollDelay(last), { ms: SLOW_POLL_MS, slow: true });
  assert.equal(pollDelay(last + 1), null);
  // The whole schedule should give up at roughly the 15 minute mark the copy
  // promises before pointing at support.
  const total = fast + SLOW_POLL_MS * SLOW_POLL_ATTEMPTS;
  assert.ok(total >= 14 * 60000 && total <= 16 * 60000, `total is ${total}ms`);
});

test('safeRedirectURL only allows https, or http on localhost', () => {
  assert.equal(safeRedirectURL('https://checkout.stripe.com/c/pay/cs_test_1'), 'https://checkout.stripe.com/c/pay/cs_test_1');
  assert.equal(safeRedirectURL('http://localhost:12111/portal'), 'http://localhost:12111/portal');
  assert.equal(safeRedirectURL('http://evil.example/'), null);
  assert.equal(safeRedirectURL('javascript:alert(1)'), null);
  assert.equal(safeRedirectURL('data:text/html,hi'), null);
  assert.equal(safeRedirectURL('//evil.example/'), null);
  assert.equal(safeRedirectURL(''), null);
  assert.equal(safeRedirectURL(undefined), null);
  assert.equal(safeRedirectURL({}), null);
});

// The success URL built in BillingHandler.Checkout is
// /settings/billing?session_id={CHECKOUT_SESSION_ID}.
test('stripCheckoutMarker removes session_id and keeps other params', () => {
  assert.deepEqual(stripCheckoutMarker('?session_id=cs_test_1'), { returned: true, search: '' });
  assert.deepEqual(stripCheckoutMarker('?tab=x&session_id=cs_test_1'), { returned: true, search: '?tab=x' });
  assert.deepEqual(stripCheckoutMarker('?tab=x'), { returned: false, search: '?tab=x' });
  assert.deepEqual(stripCheckoutMarker(''), { returned: false, search: '' });
});

test('checkAgainMessage tells the user what happens next', () => {
  const at = new Date(2026, 10, 24, 9, 5);
  assert.match(checkAgainMessage(at, true), /^Checked at .+\. Not active yet\. This page keeps checking/);
  assert.match(checkAgainMessage(at, false), /contact support/);
  assert.doesNotMatch(checkAgainMessage(at, false), /\u2014/);
});
