import { test } from 'node:test';
import assert from 'node:assert/strict';
import { promoBanner, formatPromoDate, promoCountdown, promoCardCopy, metaIsStale, promoUpgradeCopy } from './promo-copy.mjs';

// Field names and phases match TestMetaPromoPhases in
// internal/api/meta_full_test.go.
const expires = '2026-12-01T12:00:00Z';

test('no banner outside the warning and grace phases', () => {
  assert.equal(promoBanner(null, true), null);
  assert.equal(promoBanner({ plan: 'pro' }, true), null);
  assert.equal(promoBanner({ plan: 'promo', promo_phase: 'active', promo_expires_at: expires, promo_days_remaining: 30 }, true), null);
  assert.equal(promoBanner({ plan: 'promo', promo_phase: 'warning' }, true), null);
  assert.equal(promoBanner({ plan: 'promo', promo_phase: 'warning', promo_expires_at: 'bad' }, true), null);
});

test('warning phase names the end date; admins get the billing link', () => {
  const meta = { plan: 'promo', promo_phase: 'warning', promo_expires_at: expires, promo_days_remaining: 7 };
  const date = formatPromoDate(expires, 'en-US');
  assert.equal(date, 'December 1, 2026');

  const admin = promoBanner(meta, true, 'en-US');
  assert.equal(admin.lead, 'Your promo ends on December 1, 2026.');
  assert.equal(admin.action, 'Add a payment method');
  assert.equal(admin.actionHref, '/settings/billing#promo');
  assert.equal(admin.tail, ' to keep Pro features.');

  const member = promoBanner(meta, false, 'en-US');
  assert.equal(member.lead, 'Your promo ends on December 1, 2026.');
  assert.equal(member.action, undefined);
  assert.match(member.tail, /Ask an owner or admin/);
});

test('grace phase counts days until the switch to Free', () => {
  const meta = { plan: 'promo', promo_phase: 'grace', promo_expires_at: expires, promo_days_remaining: 12 };
  assert.equal(promoBanner(meta, false, 'en-US').lead,
    'Your promo ended on December 1, 2026. Features switch to Free in 12 days.');
  assert.equal(promoBanner({ ...meta, promo_days_remaining: 1 }, true, 'en-US').lead,
    'Your promo ended on December 1, 2026. Features switch to Free in 1 day.');
  assert.equal(promoBanner({ ...meta, promo_days_remaining: 0 }, true, 'en-US').lead,
    'Your promo ended on December 1, 2026. Features switch to Free today.');
  assert.equal(promoBanner(meta, true, 'en-US').actionHref, '/settings/billing#promo');
});

test('countdown wording for warning and grace', () => {
  assert.equal(promoCountdown(null), '');
  assert.equal(promoCountdown({ promo_phase: 'active', promo_days_remaining: 40 }), '');
  assert.equal(promoCountdown({ promo_phase: 'warning', promo_days_remaining: 14 }), 'in 14 days');
  assert.equal(promoCountdown({ promo_phase: 'warning', promo_days_remaining: 1 }), 'in 1 day');
  assert.equal(promoCountdown({ promo_phase: 'grace', promo_days_remaining: 0 }), 'today');
  assert.equal(promoCountdown({ promo_phase: 'grace', promo_days_remaining: 'x' }), '');
});

test('billing card puts the countdown on the date it counts to', () => {
  const warning = promoCardCopy({ promo_phase: 'warning', promo_days_remaining: 7 });
  assert.equal(warning.endLabel, 'Promo ends');
  assert.equal(warning.endCountdown, 'in 7 days');
  assert.equal(warning.freeCountdown, '');
  assert.match(warning.cta, /Add a payment method/);

  const grace = promoCardCopy({ promo_phase: 'grace', promo_days_remaining: 0 });
  assert.equal(grace.endLabel, 'Promo ended');
  assert.equal(grace.endCountdown, '');
  assert.equal(grace.freeCountdown, 'today');

  const active = promoCardCopy({ promo_phase: 'active', promo_days_remaining: 60 });
  assert.equal(active.endCountdown, '');
  assert.equal(active.cta, 'Ready to upgrade to a paid plan?');
});

test('meta reload after an hour away', () => {
  const hour = 60 * 60 * 1000;
  assert.equal(metaIsStale(0, 1000, hour), true);
  assert.equal(metaIsStale(1000, 1000 + hour, hour), false);
  assert.equal(metaIsStale(1000, 1001 + hour, hour), true);
});

test('promo upgrade copy names the first charge date when a trial applies', () => {
  const line = promoUpgradeCopy('2026-12-14T12:00:00Z', 'en-US');
  assert.match(line, /free trial/);
  assert.match(line, /not be charged until December 14, 2026/);
  assert.match(line, /Enterprise can be added/);
});

test('promo upgrade copy says billing starts at checkout without a trial', () => {
  for (const v of [undefined, null, '', 'not a date']) {
    const line = promoUpgradeCopy(v, 'en-US');
    assert.match(line, /^Billing starts when you complete checkout\./);
    assert.doesNotMatch(line, /trial/);
  }
});
