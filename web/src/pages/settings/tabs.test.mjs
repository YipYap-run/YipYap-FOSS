import { test } from 'node:test';
import assert from 'node:assert/strict';
import { availableSettingsTabs, visibleSettingsTabs } from './tabs.mjs';

const promo = { edition: 'saas', plan: 'promo', billing_enabled: true };

function keys(tabs) {
  return tabs.map(t => t.key);
}

test('a SaaS promo org reaches the promo billing card', () => {
  assert.ok(keys(visibleSettingsTabs(promo, true, 'owner')).includes('billing'));
  assert.ok(keys(visibleSettingsTabs(promo, true, 'admin')).includes('billing'));
});

test('billing is hidden when Stripe is off or in FOSS', () => {
  assert.equal(keys(availableSettingsTabs({ ...promo, billing_enabled: false }, true)).includes('billing'), false);
  assert.equal(keys(availableSettingsTabs({ ...promo, edition: 'foss', billing_enabled: false }, true)).includes('billing'), false);
});

test('members and viewers do not get the Billing tab', () => {
  assert.equal(keys(visibleSettingsTabs(promo, true, 'member')).includes('billing'), false);
  assert.equal(keys(visibleSettingsTabs(promo, true, 'viewer')).includes('billing'), false);
});
