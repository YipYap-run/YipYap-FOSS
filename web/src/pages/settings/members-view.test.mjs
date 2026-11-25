import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  usersQuery, userPage, pageSummary, userLabel, transferRequestBody,
  transferErrorField, teamMemberLabel, TRANSFER_CONSEQUENCE,
} from './members-view.mjs';

test('usersQuery builds the paged, search and batch forms', () => {
  assert.equal(usersQuery(), '/users');
  assert.equal(usersQuery({ limit: 50, offset: 100, q: ' pat ' }), '/users?q=pat&limit=50&offset=100');
  assert.equal(usersQuery({ role: 'admin', q: 'a', limit: 20 }), '/users?q=a&role=admin&limit=20');
  assert.equal(usersQuery({ ids: ['a', 'b', 'a', ''] }), '/users?ids=a%2Cb');
});

test('userPage reads {users,total} and the old array', () => {
  assert.deepEqual(userPage({ users: [{ id: 1 }], total: 75 }), { users: [{ id: 1 }], total: 75 });
  assert.deepEqual(userPage([{ id: 1 }, { id: 2 }]), { users: [{ id: 1 }, { id: 2 }], total: 2 });
  assert.deepEqual(userPage(null), { users: [], total: 0 });
});

test('pageSummary', () => {
  assert.equal(pageSummary(0, 3, 3), '3 members');
  assert.equal(pageSummary(0, 1, 1), '1 member');
  assert.equal(pageSummary(50, 50, 230), '51-100 of 230 members');
  assert.equal(pageSummary(0, 0, 0), '0 members');
});

test('userLabel', () => {
  assert.equal(userLabel({ email: 'a@x', name: 'Ann' }), 'Ann (a@x)');
  assert.equal(userLabel({ email: 'a@x', name: 'a@x' }), 'a@x');
  assert.equal(userLabel({ email: 'a@x' }), 'a@x');
});

test('transfer body sends user_id, never target_user_id (org-3)', () => {
  const b = transferRequestBody({ userId: 'u1', password: 'pw', mfaCode: ' 123456 ' });
  assert.deepEqual(b, { user_id: 'u1', current_password: 'pw', mfa_code: '123456' });
  assert.ok(!('target_user_id' in b));
  assert.ok(!('mfa_code' in transferRequestBody({ userId: 'u1', password: 'pw' })));
  assert.match(TRANSFER_CONSEQUENCE, /You will become an admin\. This cannot be undone without the new owner\./);
});

test('transferErrorField follows the server codes', () => {
  assert.equal(transferErrorField({ status: 403, body: { code: 'invalid_password' } }), 'password');
  assert.equal(transferErrorField({ status: 403, body: { code: 'mfa_required' } }), 'mfa');
  assert.equal(transferErrorField({ status: 403, body: { code: 'mfa_invalid' } }), 'mfa');
  assert.equal(transferErrorField({ status: 404, body: {} }), 'target');
  assert.equal(transferErrorField({ status: 500, body: {} }), '');
});

test('teamMemberLabel never shows a raw ID (ux-3)', () => {
  assert.equal(teamMemberLabel({ user_id: 'u1', email: 'p@x', name: 'Pat' }), 'Pat (p@x)');
  assert.equal(teamMemberLabel({ user_id: 'u1' }, { u1: { email: 'p@x' } }), 'p@x');
  assert.equal(teamMemberLabel({ user_id: 'u-unknown' }, {}), 'Former member');
});

import { pageNav, pickerOptions, teamSaveError, parsePosition } from './members-view.mjs';

test('pageNav labels the current page', () => {
  assert.deepEqual(pageNav(0, 230, 50), { page: 1, pages: 5, label: 'Page 1 of 5' });
  assert.equal(pageNav(50, 230, 50).label, 'Page 2 of 5');
  assert.equal(pageNav(200, 230, 50).label, 'Page 5 of 5');
  assert.equal(pageNav(0, 0, 50).label, 'Page 1 of 1');
});

test('pickerOptions keeps the chosen person and drops excluded ones', () => {
  const a = { id: 'a', email: 'a@x' };
  const b = { id: 'b', email: 'b@x' };
  const c = { id: 'c', email: 'c@x' };
  assert.deepEqual(pickerOptions([a, b], ['b']), [a]);
  // A new search no longer matches the chosen person: keep them first.
  assert.deepEqual(pickerOptions([b], [], c), [c, b]);
  // Already in the matches: no duplicate.
  assert.deepEqual(pickerOptions([a, b], [], a), [a, b]);
  // An excluded person is never offered, even when chosen.
  assert.deepEqual(pickerOptions([a], ['c'], c), [a]);
});

test('teamSaveError flags the plan limit', () => {
  const limit = { status: 403, message: 'Your plan allows 1 team. Upgrade to add more.', body: { error: 'Your plan allows 1 team. Upgrade to add more.', upgrade_required: true } };
  assert.deepEqual(teamSaveError(limit), { upgrade: true, text: 'Your plan allows 1 team. Upgrade to add more.' });
  assert.deepEqual(teamSaveError({ status: 403, body: { error: 'forbidden' } }), { upgrade: false, text: 'forbidden' });
  assert.deepEqual(teamSaveError({ status: 500, body: {} }), { upgrade: false, text: 'Could not save the team.' });
});

test('parsePosition accepts whole numbers only', () => {
  assert.equal(parsePosition('3'), 3);
  assert.equal(parsePosition(0), 0);
  assert.equal(parsePosition(''), null);
  assert.equal(parsePosition('-1'), null);
  assert.equal(parsePosition('1.5'), null);
  assert.equal(parsePosition('abc'), null);
});
