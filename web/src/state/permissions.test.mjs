import { test } from 'node:test';
import assert from 'node:assert/strict';
import { canWrite, isOrgAdmin, canSeeSettingsTab, alertPagedToUser, canAckAlert, settingsTabDeniedNote, ROLE_OPTIONS, roleLabel, assignableRoles, viewOnlyNote, settingsTabReadOnlyNote, roleSelectOptions } from './permissions.mjs';

test('canWrite matches the server writer roles', () => {
  for (const r of ['owner', 'admin', 'member']) assert.equal(canWrite(r), true, r);
  for (const r of ['viewer', '', undefined, null, 'api_key', 'staff-readonly']) assert.equal(canWrite(r), false, String(r));
});

test('isOrgAdmin is owner or admin only', () => {
  assert.equal(isOrgAdmin('owner'), true);
  assert.equal(isOrgAdmin('admin'), true);
  assert.equal(isOrgAdmin('member'), false);
  assert.equal(isOrgAdmin('viewer'), false);
});

test('billing, API keys, SSO, org and integrations tabs are hidden from non-admins', () => {
  for (const tab of ['billing', 'api-keys', 'sso', 'org', 'integrations']) {
    assert.equal(canSeeSettingsTab('owner', tab), true, tab);
    assert.equal(canSeeSettingsTab('admin', tab), true, tab);
    assert.equal(canSeeSettingsTab('member', tab), false, tab);
    assert.equal(canSeeSettingsTab('viewer', tab), false, tab);
  }
});

test('members see resource tabs, viewers lose only the import tab', () => {
  for (const tab of ['teams', 'channels', 'escalation', 'maintenance', 'status-page', 'monitor-states', 'members', 'security', 'account', 'data']) {
    assert.equal(canSeeSettingsTab('member', tab), true, tab);
    assert.equal(canSeeSettingsTab('viewer', tab), true, tab);
  }
  assert.equal(canSeeSettingsTab('member', 'import'), true);
  assert.equal(canSeeSettingsTab('viewer', 'import'), false);
});

const pagedToV1 = [
  { event_type: 'triggered' },
  { event_type: 'notified', target_user_id: 'v1' },
];

test('alertPagedToUser checks the paged event types and target user', () => {
  assert.equal(alertPagedToUser(pagedToV1, 'v1'), true);
  assert.equal(alertPagedToUser(pagedToV1, 'v2'), false);
  for (const t of ['escalated', 'ownership_transferred', 'routed']) {
    assert.equal(alertPagedToUser([{ event_type: t, target_user_id: 'v1' }], 'v1'), true, t);
  }
  // An ack or other event naming the user does not count as a page.
  assert.equal(alertPagedToUser([{ event_type: 'ack', target_user_id: 'v1' }], 'v1'), false);
  assert.equal(alertPagedToUser(null, 'v1'), false);
  assert.equal(alertPagedToUser(pagedToV1, ''), false);
});

test('canAckAlert: viewers only for alerts paged to them, writers always', () => {
  assert.equal(canAckAlert('viewer', 'v1', pagedToV1), true);
  assert.equal(canAckAlert('viewer', 'v2', pagedToV1), false);
  assert.equal(canAckAlert('viewer', 'v1', []), false);
  assert.equal(canAckAlert('member', 'm1', []), true);
  assert.equal(canAckAlert('admin', 'a1', []), true);
  assert.equal(canAckAlert(undefined, 'x', pagedToV1), false);
});

test('deep links to admin tabs explain who can manage them', () => {
  assert.equal(settingsTabDeniedNote('member', 'billing', 'Billing'), 'Only owners and admins can manage Billing.');
  assert.equal(settingsTabDeniedNote('viewer', 'api-keys', 'API Keys'), 'Only owners and admins can manage API Keys.');
  assert.equal(settingsTabDeniedNote('member', 'sso', 'SSO'), 'Only owners and admins can manage SSO.');
  assert.equal(settingsTabDeniedNote('viewer', 'import', 'Import'), 'Import is not available to viewers.');
  assert.equal(settingsTabDeniedNote('owner', 'billing', 'Billing'), null);
  assert.equal(settingsTabDeniedNote('member', 'teams', 'Teams'), null);
});

test('role options name all four roles with a description', () => {
  assert.deepEqual(ROLE_OPTIONS.map(r => r.value), ['owner', 'admin', 'member', 'viewer']);
  for (const r of ROLE_OPTIONS) assert.ok(r.label && r.description, r.value);
  assert.equal(roleLabel('viewer'), 'Viewer');
  assert.equal(roleLabel('mystery'), 'mystery');
  assert.equal(roleLabel(undefined), '');
});

test('assignableRoles matches what the server lets each role grant', () => {
  // internal/api/handlers/users.go: only the owner may grant admin; admins
  // may set member or viewer; owner moves only through ownership transfer.
  assert.deepEqual(assignableRoles('owner').map(r => r.value), ['admin', 'member', 'viewer']);
  assert.deepEqual(assignableRoles('admin').map(r => r.value), ['member', 'viewer']);
  assert.deepEqual(assignableRoles('member'), []);
  assert.deepEqual(assignableRoles('viewer'), []);
});

test('roleSelectOptions keeps the current role visible so the select never shows a wrong value', () => {
  assert.deepEqual(roleSelectOptions('admin', 'admin').map(r => r.value), ['admin', 'member', 'viewer']);
  assert.equal(roleSelectOptions('admin', 'admin')[0].disabled, true);
  assert.deepEqual(roleSelectOptions('admin', 'viewer').map(r => r.value), ['member', 'viewer']);
  assert.deepEqual(roleSelectOptions('owner', 'viewer').map(r => r.value), ['admin', 'member', 'viewer']);
});

test('viewOnlyNote explains hidden controls to viewers only', () => {
  assert.match(viewOnlyNote('viewer'), /view-only/i);
  for (const r of ['owner', 'admin', 'member']) assert.equal(viewOnlyNote(r), null, r);
});

test('settingsTabReadOnlyNote covers viewers on resource tabs and non-admins on members', () => {
  for (const tab of ['channels', 'escalation', 'teams', 'monitor-states', 'maintenance', 'status-page']) {
    assert.ok(settingsTabReadOnlyNote('viewer', tab), tab);
    assert.equal(settingsTabReadOnlyNote('member', tab), null, tab);
    assert.equal(settingsTabReadOnlyNote('admin', tab), null, tab);
  }
  assert.match(settingsTabReadOnlyNote('member', 'members'), /owners and admins/);
  assert.match(settingsTabReadOnlyNote('viewer', 'members'), /owners and admins/);
  assert.equal(settingsTabReadOnlyNote('admin', 'members'), null);
  for (const tab of ['account', 'security', 'data']) assert.equal(settingsTabReadOnlyNote('viewer', tab), null, tab);
});
