import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  toggleGrant, setAllGrants, buildScopes, expiryPayload, customDateBounds, expiryStatus,
  scopeSummary, creatorLabel, revokePrompt, removeMemberPath, removeMemberResultText,
  validateCreate, defaultExpiryChoice, expiryPreview,
} from './api-keys-view.mjs';

// Shape served by GET /org/api-keys/scopes (see apikey_scopes.go).
const RESOURCES = [
  { key: 'monitors', label: 'Monitors', actions: ['read', 'write'] },
  { key: 'alerts', label: 'Alerts', actions: ['read', 'write'] },
  { key: 'incidents', label: 'Incidents', actions: ['read'] },
];

const NOW = new Date(2026, 10, 24, 12, 0, 0);
const fmt = ts => new Date(ts).toISOString().slice(0, 10);

test('write implies read when toggling grants', () => {
  let g = toggleGrant({}, 'monitors', 'write', true);
  assert.deepEqual(g, { monitors: 'write' });
  g = toggleGrant(g, 'monitors', 'write', false);
  assert.deepEqual(g, { monitors: 'read' });
  g = toggleGrant({ monitors: 'write' }, 'monitors', 'read', false);
  assert.deepEqual(g, {});
  g = toggleGrant({ monitors: 'write' }, 'monitors', 'read', true);
  assert.deepEqual(g, { monitors: 'write' });
});

test('setAllGrants respects what each resource supports', () => {
  assert.deepEqual(setAllGrants(RESOURCES, 'write'), { monitors: 'write', alerts: 'write', incidents: 'read' });
  assert.deepEqual(setAllGrants(RESOURCES, 'read'), { monitors: 'read', alerts: 'read', incidents: 'read' });
  assert.deepEqual(setAllGrants(RESOURCES, 'none'), {});
});

test('buildScopes sends presets alone and custom grants in vocabulary order', () => {
  assert.deepEqual(buildScopes('read-only', { monitors: 'write' }, RESOURCES), ['read-only']);
  assert.deepEqual(buildScopes('full-access', {}, RESOURCES), ['full-access']);
  assert.deepEqual(
    buildScopes('custom', { incidents: 'read', monitors: 'write', alerts: 'read' }, RESOURCES),
    ['monitors:write', 'alerts:read', 'incidents:read'],
  );
  // incidents cannot be written by a key, so a stray write grant becomes read.
  assert.deepEqual(buildScopes('custom', { incidents: 'write' }, RESOURCES), ['incidents:read']);
  // Resources the server did not advertise are dropped.
  assert.deepEqual(buildScopes('custom', { billing: 'write' }, RESOURCES), []);
});

test('expiryPayload matches the create contract', () => {
  assert.deepEqual(expiryPayload('never', ''), { value: 'never' });
  const p = expiryPayload('90', '', NOW);
  assert.equal(new Date(p.value).getTime() - NOW.getTime(), 90 * 86400000);
  const { min, max } = customDateBounds(NOW, 730);
  assert.equal(min, '2026-11-25');
  const d = expiryPayload('date', min, NOW);
  assert.ok(d.value && new Date(d.value) > NOW);
  // End of the picked day stays inside the 730-day server cap.
  const last = expiryPayload('date', max, NOW);
  assert.ok(new Date(last.value).getTime() <= NOW.getTime() + 730 * 86400000);
  assert.ok(expiryPayload('date', '2026-11-24', NOW).error);
  assert.ok(expiryPayload('date', '2030-01-01', NOW).error);
  assert.ok(expiryPayload('date', '', NOW).error);
  assert.ok(expiryPayload('bogus', '', NOW).error);
  assert.equal(defaultExpiryChoice(90), '90');
  assert.equal(defaultExpiryChoice(45), '90');
  assert.equal(expiryPreview('never', '', NOW), 'This key works until someone revokes it.');
  assert.match(expiryPreview('30', '', NOW, fmt), /^Stops working on 2026-12-2\d\.$/);
});

test('expiryStatus flags expired and soon-to-expire keys', () => {
  assert.deepEqual(expiryStatus({ expires_at: null }, NOW), { tone: 'never', label: 'No expiry' });
  assert.equal(expiryStatus({ expires_at: '2026-11-01T00:00:00Z', expired: true }, NOW, fmt).tone, 'expired');
  assert.equal(expiryStatus({ expires_at: new Date(NOW.getTime() + 5 * 86400000).toISOString() }, NOW).label, 'Expires in 5 days');
  assert.equal(expiryStatus({ expires_at: new Date(NOW.getTime() + 3600000).toISOString() }, NOW).label, 'Expires within a day');
  assert.equal(expiryStatus({ expires_at: '2027-06-01T00:00:00Z' }, NOW, fmt).label, 'Expires 2027-06-01');
});

test('scopeSummary reads presets, custom and legacy keys', () => {
  assert.equal(scopeSummary({ scopes: ['full-access'] }, RESOURCES), 'Full access');
  assert.equal(scopeSummary({ scopes: ['read-only'] }, RESOURCES), 'Read-only');
  assert.equal(scopeSummary({ scopes: [], unrestricted: true }, RESOURCES), 'Full access (created before scopes)');
  assert.equal(
    scopeSummary({ scopes: ['monitors:write', 'incidents:read'] }, RESOURCES),
    'Monitors (read and write), Incidents (read)',
  );
  assert.equal(scopeSummary({ scopes: ['monitors'] }, RESOURCES), 'Monitors (read and write)');
});

test('creatorLabel handles removed creators', () => {
  assert.equal(creatorLabel({ creator_removed: true }), 'A removed member');
  assert.equal(creatorLabel({ creator_name: 'Ana', creator_email: 'ana@example.com' }), 'Ana (ana@example.com)');
  assert.equal(creatorLabel({ creator_email: 'ana@example.com' }), 'ana@example.com');
  assert.equal(creatorLabel({}), 'Unknown');
});

test('member removal prompt and request', () => {
  assert.equal(revokePrompt(2), 'This person created 2 keys. Revoke them?');
  assert.equal(revokePrompt(1), 'This person created 1 key. Revoke it?');
  assert.equal(revokePrompt(0), '');
  assert.equal(removeMemberPath('u1', 0, true), '/users/u1');
  assert.equal(removeMemberPath('u1', 2, true), '/users/u1?revoke_api_keys=true');
  assert.equal(removeMemberPath('u1', 2, false), '/users/u1?revoke_api_keys=false');
  assert.equal(removeMemberPath('a/b', 0, false), '/users/a%2Fb');
  assert.equal(removeMemberResultText({ revoked_api_keys: 2 }, 2, true), 'Member removed. 2 keys revoked.');
  assert.equal(removeMemberResultText(null, 2, false), 'Member removed. Their 2 keys still work.');
  assert.equal(removeMemberResultText(null, 0, false), 'Member removed');
});

test('validateCreate catches what the server would reject', () => {
  const base = { name: 'CI', mode: 'read-only', grants: {}, resources: RESOURCES, expiry: '90', customDate: '', now: NOW };
  assert.deepEqual(validateCreate(base), {});
  assert.ok(validateCreate({ ...base, name: '  ' }).name);
  assert.ok(validateCreate({ ...base, name: 'x'.repeat(101) }).name);
  assert.ok(validateCreate({ ...base, mode: 'custom' }).scopes);
  assert.deepEqual(validateCreate({ ...base, mode: 'custom', grants: { alerts: 'read' } }), {});
  assert.ok(validateCreate({ ...base, expiry: 'date', customDate: '' }).expiry);
});

test('keyLabel adds the prefix only when the server sent one', async () => {
  const { keyLabel } = await import('./api-keys-view.mjs');
  assert.equal(keyLabel({ name: 'CI', prefix: 'yy_abc' }), 'CI (yy_abc...)');
  assert.equal(keyLabel({ name: 'Old key' }), 'Old key');
  assert.equal(keyLabel({ name: 'Old key', prefix: '' }), 'Old key');
  assert.equal(keyLabel({}), 'Unnamed key');
});

test('revokeConsequence explains an expired key differently', async () => {
  const { revokeConsequence } = await import('./api-keys-view.mjs');
  const past = new Date(NOW.getTime() - 86400000).toISOString();
  const future = new Date(NOW.getTime() + 30 * 86400000).toISOString();
  assert.match(revokeConsequence({ expires_at: past, expired: true }, NOW), /already expired/);
  assert.match(revokeConsequence({ expires_at: future }, NOW), /stops working right away/);
  assert.match(revokeConsequence({ expires_at: null }, NOW), /stops working right away/);
});

test('closing the new-key dialog asks first until the key is copied', async () => {
  const { shouldConfirmDiscard } = await import('./api-keys-view.mjs');
  assert.equal(shouldConfirmDiscard({ secret: 'yy_abc', copied: false }), true);
  assert.equal(shouldConfirmDiscard({ secret: 'yy_abc', copied: true }), false);
  assert.equal(shouldConfirmDiscard({ secret: 'yy_abc', copied: false, force: true }), false);
  assert.equal(shouldConfirmDiscard({ secret: '', copied: false }), false);
});
