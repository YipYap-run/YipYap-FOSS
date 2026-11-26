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

test('Free SaaS orgs see Integrations (upgrade prompt) but not SSO; FOSS sees neither', () => {
  const free = { edition: 'saas', plan: 'free', billing_enabled: true };
  assert.ok(keys(visibleSettingsTabs(free, false, 'owner')).includes('integrations'));
  assert.equal(keys(visibleSettingsTabs(free, false, 'owner')).includes('sso'), false);
  assert.equal(keys(availableSettingsTabs({ edition: 'foss' }, true)).includes('integrations'), false);
});
