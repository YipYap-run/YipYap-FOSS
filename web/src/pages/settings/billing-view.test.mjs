import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  hasSubscription, isStaffManagedPlan, isTrialing, validDate, planName, trialNote, periodLabel,
  enterpriseConfirm, enterpriseSettled, checkoutConflict, activationDoneText,
  formatCents, enterpriseMonthlyLabel, billingEmailRequest, ACTIVATION_PENDING_TEXT,
  billingEmailError, billingEmailServerError, memberPlanNote,
  enterpriseConflictText, portalErrorText, usageLinkHref,
} from './billing-view.mjs';

// Shapes match GET /api/v1/billing as pinned in
// internal/api/handlers/billing_view_contract_test.go.
const trialing = { plan: 'pro', status: 'trialing', seat_count: 3, has_enterprise: false, period_end: '2026-12-14T12:00:00Z' };
const active = { plan: 'pro', status: 'active', seat_count: 1, has_enterprise: false, period_end: '2026-12-24T00:00:00Z' };
const comped = { plan: 'pro', status: 'none', seat_count: 0, has_enterprise: false, period_end: '0001-01-01T00:00:00Z' };
const pendingFree = { plan: 'free', status: 'pending', seat_count: 0 };

test('subscription states', () => {
  assert.equal(hasSubscription(trialing), true);
  assert.equal(hasSubscription(active), true);
  assert.equal(hasSubscription({ ...active, status: 'past_due' }), true);
  assert.equal(hasSubscription(comped), false);
  assert.equal(hasSubscription(pendingFree), false);
  assert.equal(isTrialing(trialing), true);
  assert.equal(isTrialing(active), false);
});

test('a paid plan without a billing row is staff managed', () => {
  assert.equal(isStaffManagedPlan(comped), true);
  assert.equal(isStaffManagedPlan({ ...comped, plan: 'enterprise' }), true);
  assert.equal(isStaffManagedPlan(active), false);
  assert.equal(isStaffManagedPlan({ plan: 'free', status: 'none' }), false);
  assert.equal(isStaffManagedPlan({ plan: 'promo', status: 'none' }), false);
});

test('plan names use Free, Pro and Enterprise', () => {
  assert.equal(planName(pendingFree), 'Free');
  assert.equal(planName(active), 'Pro');
  assert.equal(planName({ ...active, plan: 'enterprise', has_enterprise: true }), 'Pro + Enterprise');
  assert.equal(planName({ ...comped, plan: 'enterprise' }), 'Enterprise');
});

test('zero Go times are not dates', () => {
  assert.equal(validDate('0001-01-01T00:00:00Z'), '');
  assert.equal(validDate(''), '');
  assert.equal(validDate('nope'), '');
  assert.equal(validDate('2026-12-14T12:00:00Z'), '2026-12-14T12:00:00Z');
});

test('trial note names the first charge date and seats', () => {
  const line = trialNote(trialing, 'en-US');
  assert.match(line, /Free trial until December 14, 2026/);
  assert.match(line, /3 seats/);
  assert.match(trialNote({ ...trialing, seat_count: 1 }, 'en-US'), /1 seat,/);
  assert.equal(trialNote(active, 'en-US'), '');
  assert.match(trialNote({ ...trialing, period_end: '0001-01-01T00:00:00Z' }, 'en-US'), /free trial/);
  assert.equal(periodLabel(trialing), 'Trial ends');
  assert.equal(periodLabel(active), 'Current period ends');
});

test('enterprise confirmation shows the price and when it is charged', () => {
  const t = enterpriseConfirm({ ...trialing, ent_price_cents: 5000, ent_blocks: 1, ent_monthly_cents: 5000, ent_currency: 'usd' }, 'en-US');
  assert.equal(t.title, 'Add the Enterprise add-on?');
  assert.match(t.price, /\$50\/mo/);
  assert.match(t.note, /first charged on December 14, 2026/);
  assert.equal(t.action, 'Add Enterprise for $50/mo');

  const big = enterpriseConfirm({ ...active, ent_price_cents: 5000, ent_blocks: 2, ent_monthly_cents: 10000 }, 'en-US');
  assert.match(big.price, /\$100\/mo \(2 blocks of 50 seats\)/);
  assert.match(big.note, /prorated amount .* next invoice/);

  const unknown = enterpriseConfirm(active, 'en-US');
  assert.equal(unknown.price, '');
  assert.equal(unknown.action, 'Add Enterprise');

  const remove = enterpriseConfirm({ ...active, has_enterprise: true }, 'en-US');
  assert.equal(remove.title, 'Remove the Enterprise add-on?');
  assert.match(remove.note, /credited to your next invoice/);
});

test('prices format from cents', () => {
  assert.equal(formatCents(800, 'usd'), '$8');
  assert.equal(formatCents(850), '$8.50');
  assert.equal(formatCents(5000, 'eur'), 'EUR 50');
  assert.equal(formatCents(0), '');
  assert.equal(enterpriseMonthlyLabel({ ent_price_cents: 5000 }), '$50/mo');
  assert.equal(enterpriseMonthlyLabel({ ent_price_cents: 5000, ent_monthly_cents: 15000 }), '$150/mo');
  assert.equal(enterpriseMonthlyLabel(null), '');
});

test('billing email saves through PATCH /billing/email with {email}', () => {
  assert.deepEqual(billingEmailRequest('  ops@example.com '), { path: '/billing/email', body: { email: 'ops@example.com' } });
  assert.deepEqual(billingEmailRequest(undefined), { path: '/billing/email', body: { email: '' } });
});

test('activation copy after returning from checkout', () => {
  assert.equal(ACTIVATION_PENDING_TEXT, 'Payment received. Activating your plan...');
});

test('enterprise change settles when has_enterprise matches', () => {
  assert.equal(enterpriseSettled(active, true), false);
  assert.equal(enterpriseSettled({ ...active, has_enterprise: true }, true), true);
  assert.equal(enterpriseSettled(active, false), true);
  assert.equal(enterpriseSettled(null, false), false);
});

test('a 409 from checkout means wait for activation', () => {
  assert.equal(checkoutConflict({ status: 409 }), true);
  assert.equal(checkoutConflict({ status: 500 }), false);
  assert.equal(checkoutConflict({ status: 400 }), false);
  assert.equal(checkoutConflict(null), false);
});

test('activation copy mentions the trial when there is one', () => {
  assert.match(activationDoneText(trialing, 'en-US'), /Free trial until December 14, 2026/);
  assert.match(activationDoneText(active, 'en-US'), /^Paid features are now available/);
  assert.match(activationDoneText(null), /^Paid features/);
});

// Mirrors TestBillingEmailPatchValidatesAddress: the field refuses what the
// server would reject, so the user sees the problem before a round trip.
test('billing email field checks match the server rules', () => {
  assert.equal(billingEmailError(' ops@example.com '), '');
  assert.match(billingEmailError('   '), /Enter an email address/);
  assert.match(billingEmailError('Ops <ops@example.com>'), /single email address/);
  assert.match(billingEmailError('a@example.com, b@example.com'), /single email address/);
  assert.match(billingEmailError('not-an-email'), /single email address/);
  assert.match(billingEmailError(`${'a'.repeat(250)}@example.com`), /254 characters/);
});

test('billing email server errors become field copy', () => {
  assert.match(billingEmailServerError({ status: 400, message: 'invalid email address' }), /single email address/);
  assert.match(billingEmailServerError({ status: 400, message: 'email address too long' }), /254 characters/);
  assert.match(billingEmailServerError({ status: 400, message: 'email is required' }), /Enter an email address/);
  assert.match(billingEmailServerError({ status: 502, message: 'x' }), /was not updated/);
  assert.match(billingEmailServerError({ status: 404 }), /no Stripe customer/);
  assert.match(billingEmailServerError({ status: 403 }), /owners and admins/);
  assert.match(billingEmailServerError(new Error('network')), /Could not save/);
});

test('members and viewers are told who can change the plan', () => {
  assert.match(memberPlanNote({ plan: 'free', status: 'none' }), /upgrade the plan/);
  assert.match(memberPlanNote({ plan: 'promo', status: 'none' }), /upgrade the plan/);
  assert.match(memberPlanNote({ ...active, status: 'past_due' }), /payment method/);
  assert.match(memberPlanNote(active), /change the subscription/);
  assert.equal(memberPlanNote(comped), '');
  assert.equal(memberPlanNote(null), '');
});

// Statuses pinned in internal/api/handlers/billing_portal_enterprise_test.go.
test('an Enterprise 409 means the page was stale, not a failure', () => {
  const conflict = Object.assign(new Error('the Enterprise add-on is already active'), { status: 409 });
  assert.match(enterpriseConflictText(conflict, true), /already active/);
  assert.match(enterpriseConflictText(conflict, false), /already off/);
  const off = Object.assign(new Error('the Enterprise add-on is not active'), { status: 409 });
  assert.match(enterpriseConflictText(off, false), /already off/);
  const lapsed = Object.assign(new Error('subscription is not active'), { status: 409 });
  assert.match(enterpriseConflictText(lapsed, true), /subscription is not active/);
  assert.equal(enterpriseConflictText(Object.assign(new Error('x'), { status: 500 }), true), '');
  assert.equal(enterpriseConflictText(null, true), '');
});

test('portal errors explain a missing Stripe customer', () => {
  assert.match(portalErrorText({ status: 404, message: 'no billing record found' }), /no Stripe customer/);
  assert.match(portalErrorText({ status: 403 }), /owners and admins/);
  assert.match(portalErrorText({ status: 500 }), /Try again/);
  assert.match(portalErrorText(undefined), /Try again/);
});

test('quota links land on the promo card for a promo org', () => {
  assert.equal(usageLinkHref('promo'), '/settings/billing#promo');
  assert.equal(usageLinkHref('pro'), '/settings/billing#usage');
  assert.equal(usageLinkHref(undefined), '/settings/billing#usage');
});
