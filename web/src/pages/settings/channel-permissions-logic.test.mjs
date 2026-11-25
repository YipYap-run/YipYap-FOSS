import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalizePermissions, toggleRole, lockReason } from './channel-permissions-logic.mjs';

test('normalize adds owner, grants test to managers, drops viewer managers', () => {
  assert.deepEqual(normalizePermissions({ manage_roles: ['member', 'viewer'], test_roles: ['viewer'] }), {
    manage_roles: ['owner', 'member'],
    test_roles: ['owner', 'member', 'viewer'],
  });
});

test('toggling a manager on also lets them test', () => {
  const p = normalizePermissions({ manage_roles: ['owner', 'admin'], test_roles: ['owner', 'admin'] });
  assert.deepEqual(toggleRole(p, 'manage', 'member').test_roles, ['owner', 'admin', 'member']);
});

test('owner cannot be removed', () => {
  const p = normalizePermissions({});
  assert.deepEqual(toggleRole(p, 'manage', 'owner').manage_roles, ['owner']);
});

test('lock reasons', () => {
  const p = normalizePermissions({ manage_roles: ['admin'] });
  assert.match(lockReason(p, 'manage', 'owner'), /Owners/);
  assert.match(lockReason(p, 'manage', 'viewer'), /Viewers/);
  assert.match(lockReason(p, 'test', 'admin'), /manage/);
  assert.equal(lockReason(p, 'test', 'member'), '');
});

test('describeRoles lists roles in order', async () => {
  const { describeRoles } = await import('./channel-permissions-logic.mjs');
  assert.equal(describeRoles(['admin', 'owner']), 'owners and admins');
  assert.equal(describeRoles(['member', 'owner', 'admin']), 'owners, admins and members');
  assert.equal(describeRoles(['owner']), 'owners');
  assert.equal(describeRoles([]), '');
});

test('channelAccessNote reads the API fields, not the role', async () => {
  const { channelAccessNote } = await import('./channel-permissions-logic.mjs');
  assert.equal(channelAccessNote({ can_manage: true, can_test: true }), '');
  assert.equal(channelAccessNote(null), '');
  assert.equal(
    channelAccessNote({ can_manage: false, can_test: true, manage_roles: ['owner', 'admin'], test_roles: ['owner', 'admin', 'member'] }),
    'Only owners and admins can add or change channels. You can send test notifications.');
  assert.equal(
    channelAccessNote({ can_manage: false, can_test: false, manage_roles: ['owner', 'admin', 'member'], test_roles: ['owner', 'admin', 'member'] }),
    'Only owners, admins and members can add or change channels, and only owners, admins and members can send tests.');
});

test('samePermissions ignores order and implied roles', async () => {
  const { samePermissions } = await import('./channel-permissions-logic.mjs');
  assert.ok(samePermissions({ manage_roles: ['admin'], test_roles: ['member'] },
    { manage_roles: ['owner', 'admin'], test_roles: ['member', 'admin', 'owner'] }));
  assert.ok(!samePermissions({ manage_roles: ['admin'] }, { manage_roles: ['member'] }));
});

test('selfLockoutWarning only for a non-owner dropping their own role', async () => {
  const { selfLockoutWarning, normalizePermissions } = await import('./channel-permissions-logic.mjs');
  const p = normalizePermissions({ manage_roles: ['owner'] });
  assert.match(selfLockoutWarning(p, 'admin'), /admins \(including you\)/);
  assert.equal(selfLockoutWarning(p, 'owner'), '');
  assert.equal(selfLockoutWarning(normalizePermissions({ manage_roles: ['admin'] }), 'admin'), '');
});

test('selfLockoutWarning says when the test right goes too', async () => {
  const { selfLockoutWarning, normalizePermissions } = await import('./channel-permissions-logic.mjs');
  const keepsTest = normalizePermissions({ manage_roles: ['owner'], test_roles: ['admin'] });
  assert.doesNotMatch(selfLockoutWarning(keepsTest, 'admin'), /test/);
  const losesBoth = normalizePermissions({ manage_roles: ['owner'], test_roles: [] });
  assert.match(selfLockoutWarning(losesBoth, 'admin'), /or send test notifications/);
  assert.equal(selfLockoutWarning(losesBoth, 'api_key'), '');
});

test('firstEnabledCheckbox skips disabled boxes', async () => {
  const { firstEnabledCheckbox } = await import('./channel-permissions-logic.mjs');
  const a = { disabled: true }, b = { disabled: false };
  assert.equal(firstEnabledCheckbox({ querySelectorAll: () => [a, b] }), b);
  assert.equal(firstEnabledCheckbox({ querySelectorAll: () => [a] }), null);
  assert.equal(firstEnabledCheckbox(null), null);
});
