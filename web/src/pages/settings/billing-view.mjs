// Pure helpers for the Billing tab's plan card (bill-3, bill-4, bill-5).
// Kept free of Preact so node --test can check them against the GET
// /api/v1/billing contract pinned in
// internal/api/handlers/billing_view_contract_test.go.

import { formatPromoDate } from '../../components/promo-copy.mjs';

// Statuses of a subscription that still exists in Stripe. Matches
// billingSubscriptionLive in internal/api/handlers/billing_checkout.go.
const LIVE = new Set(['active', 'trialing', 'past_due']);

export function hasSubscription(b) {
  return !!b && LIVE.has(b.status);
}

// A paid plan with no billing row at all (status "none"): set by staff
// through the ops API, so there is no Stripe customer or portal.
export function isStaffManagedPlan(b) {
  return !!b && b.plan !== 'free' && b.plan !== 'promo' && b.status === 'none';
}

export function isTrialing(b) {
  return !!b && b.status === 'trialing';
}

// validDate returns the timestamp when it is a real date. Go encodes a zero
// time.Time as 0001-01-01T00:00:00Z.
export function validDate(ts) {
  if (!ts || String(ts).startsWith('0001')) return '';
  const d = new Date(ts);
  return Number.isNaN(d.getTime()) ? '' : ts;
}

// planName is the plan shown on the card. Plans are Free, Pro and
// Enterprise; Enterprise on a subscription is an add-on to Pro.
export function planName(b) {
  if (!b || b.plan === 'free') return 'Free';
  if (b.has_enterprise) return 'Pro + Enterprise';
  if (b.plan === 'enterprise') return 'Enterprise';
  return 'Pro';
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? '' : 's'}`;
}

// trialNote describes a subscription that started from a promo and is still
// in its trial. period_end is the trial end while Stripe reports trialing.
export function trialNote(b, locale) {
  if (!isTrialing(b)) return '';
  const end = validDate(b.period_end);
  const date = end ? formatPromoDate(end, locale) : '';
  const seats = Math.max(Number(b.seat_count) || 0, 1);
  if (!date) return `Your remaining promo time is a free trial. You are not charged until it ends.`;
  return `Free trial until ${date}. The first charge, for ${plural(seats, 'seat')}, is on that date.`;
}

// periodLabel names the period_end row.
export function periodLabel(b) {
  return isTrialing(b) ? 'Trial ends' : 'Current period ends';
}

export const SEATS_HINT = 'One seat per active member. The count updates when members join or leave.';

export const ENTERPRISE_ADD_ON_NOTE = 'Enterprise can be added on this page once the Pro subscription has started.';

// formatCents turns cents into a price like "$50" or "$8.50". '' when unknown.
export function formatCents(cents, currency) {
  const n = Number(cents);
  if (!n || n < 0) return '';
  const amt = (n / 100).toFixed(n % 100 === 0 ? 0 : 2);
  const cur = String(currency || 'usd').toLowerCase();
  return cur === 'usd' ? `$${amt}` : `${cur.toUpperCase()} ${amt}`;
}

// enterpriseMonthlyLabel is the add-on price for the org's seat count, like
// "$100/mo". GET /billing sends ent_monthly_cents (one block per 50 seats);
// without a billing row only the per-block price is known.
export function enterpriseMonthlyLabel(b) {
  if (!b) return '';
  const price = formatCents(b.ent_monthly_cents || b.ent_price_cents, b.ent_currency);
  return price ? `${price}/mo` : '';
}

// enterpriseConfirm describes adding or removing the Enterprise add-on for
// the confirmation dialog: a title, the price line, the proration note and
// the confirm button label.
export function enterpriseConfirm(b, locale) {
  if (b?.has_enterprise) {
    return {
      title: 'Remove the Enterprise add-on?',
      price: '',
      note: 'Enterprise features turn off for everyone in this organization once Stripe confirms the change. Unused time on the add-on is credited to your next invoice.',
      action: 'Remove Enterprise',
    };
  }
  const label = enterpriseMonthlyLabel(b);
  const blocks = Number(b?.ent_blocks) || 0;
  const per = blocks > 1 ? ` (${blocks} blocks of 50 seats)` : '';
  const price = label ? `Enterprise costs ${label}${per}, on top of your Pro seats.` : '';
  let note;
  if (isTrialing(b)) {
    const end = validDate(b.period_end);
    const date = end ? formatPromoDate(end, locale) : '';
    note = date
      ? `It is added to your trial now and first charged on ${date}, when the trial ends.`
      : 'It is added to your trial now and first charged when the trial ends.';
  } else {
    note = 'The prorated amount for the rest of this billing period is added to your next invoice.';
  }
  return { title: 'Add the Enterprise add-on?', price, note, action: label ? `Add Enterprise for ${label}` : 'Add Enterprise' };
}

// enterpriseSettled reports whether GET /billing shows the requested add-on
// state. The add-on endpoints only ask Stripe for the change; has_enterprise
// flips when the subscription webhook lands.
export function enterpriseSettled(b, wantEnterprise) {
  return !!b && !!b.has_enterprise === !!wantEnterprise;
}

export function enterprisePendingText(wantEnterprise) {
  return wantEnterprise
    ? 'Enterprise add-on requested. This page updates once Stripe confirms it.'
    : 'Enterprise removal requested. This page updates once Stripe confirms it.';
}

export function enterpriseStalledText(wantEnterprise) {
  const what = wantEnterprise ? 'adding Enterprise' : 'removing Enterprise';
  return `Stripe has not confirmed ${what} yet. Reload this page in a few minutes, or contact support if nothing changes.`;
}

// checkoutConflict reports whether a checkout error means a checkout for
// this org already went through and its webhook is due (HTTP 409: an
// active subscription exists, or the open session was already paid). The
// page then waits for activation instead of offering another checkout.
export function checkoutConflict(err) {
  return !!err && err.status === 409;
}

// enterpriseConflictText is shown when POST or DELETE /billing/enterprise
// answers 409, which means this page was stale: either the add-on is already
// in the state the user asked for (the webhook landed, or another admin
// changed it), or the subscription is no longer active. The page reloads
// billing instead of reporting a failure. '' for other errors.
export function enterpriseConflictText(err, wantEnterprise) {
  if (!err || err.status !== 409) return '';
  const msg = String(err.message || '').toLowerCase();
  if (msg.includes('enterprise')) {
    return wantEnterprise
      ? 'The Enterprise add-on is already active. This page now shows the current subscription.'
      : 'The Enterprise add-on is already off. This page now shows the current subscription.';
  }
  return 'The subscription is not active, so the Enterprise add-on cannot change. This page now shows the current subscription.';
}

// portalErrorText turns a POST /billing/portal error into a toast. 404
// means the org has no Stripe customer (checkout never reached Stripe), so
// there is no portal to open and retrying will not help.
export function portalErrorText(err) {
  const status = err?.status;
  if (status === 404) return 'This organization has no Stripe customer yet, so there is no billing portal to open. Contact support if you were charged.';
  if (status === 403) return 'Only owners and admins can manage the subscription.';
  return 'Could not open the billing portal. Try again in a moment.';
}

// ACTIVATION_PENDING_TEXT is shown after returning from Stripe Checkout while
// the page waits for the webhook to move the org onto its new plan.
export const ACTIVATION_PENDING_TEXT = 'Payment received. Activating your plan...';

// billingEmailRequest is the request that saves the billing contact:
// PATCH /billing/email with {"email"}, pinned in
// internal/api/handlers/billing_email_test.go.
export function billingEmailRequest(email) {
  return { path: '/billing/email', body: { email: String(email ?? '').trim() } };
}

// billingEmailError checks the billing email field before it is sent, with
// the same rules as normalizeBillingEmail in internal/api/handlers/billing.go:
// required, at most 254 characters, and a bare address (no display name and
// no list). Returns '' when the value can be sent.
export function billingEmailError(raw) {
  const email = String(raw ?? '').trim();
  if (!email) return 'Enter an email address.';
  if (email.length > 254) return 'Use an email address of 254 characters or fewer.';
  if (/[\s<>,;"]/.test(email) || !/^[^@]+@[^@]+\.[^@]+$/.test(email)) {
    return 'Enter a single email address, like billing@company.com.';
  }
  return '';
}

// billingEmailServerError turns a PATCH /billing/email error into copy for
// the field. Validation errors (400) belong next to the input; anything else
// is a save failure the user can retry.
export function billingEmailServerError(err) {
  const status = err?.status;
  const msg = String(err?.message || '').toLowerCase();
  if (status === 400) {
    if (msg.includes('too long')) return 'Use an email address of 254 characters or fewer.';
    if (msg.includes('required')) return 'Enter an email address.';
    return 'Enter a single email address, like billing@company.com.';
  }
  if (status === 502) return 'Stripe did not accept the change, so the billing email was not updated. Try again in a moment.';
  if (status === 404) return 'This organization has no Stripe customer yet, so there is no billing email to change.';
  if (status === 403) return 'Only owners and admins can change the billing email.';
  return 'Could not save the billing email. Try again in a moment.';
}

// memberPlanNote is shown to members and viewers in place of plan actions,
// so they know who can make the change. '' when no note is needed.
export function memberPlanNote(b) {
  if (!b) return '';
  if (b.status === 'past_due') return 'Ask an owner or admin to update the payment method.';
  if (b.plan === 'free' || b.plan === 'promo') return 'Only owners and admins can upgrade the plan.';
  if (isStaffManagedPlan(b)) return '';
  return 'Only owners and admins can change the subscription.';
}

// activationDoneText is the line under "Your plan is active."
export function activationDoneText(b, locale) {
  const trial = trialNote(b, locale);
  if (trial) return `Pro features are on for everyone in your organization. ${trial}`;
  return 'Paid features are now available to everyone in your organization.';
}

export function usageLinkHref(plan) {
  return plan === 'promo' ? '/settings/billing#promo' : '/settings/billing#usage';
}
