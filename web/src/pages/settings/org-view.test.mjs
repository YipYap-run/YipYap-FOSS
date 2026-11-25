import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeSlug, slugError, orgPatchBody, slugChanged, graceDaysError, mfaGapText, orgMFASectionMode,
} from './org-view.mjs';

test('rename without a slug change sends no slug', () => {
  const org = { name: 'Acme', slug: 'acme' };
  assert.deepEqual(orgPatchBody({ name: 'Acme Corp', slug: 'acme', oncall_display: 'email' }, org),
    { name: 'Acme Corp', oncall_display: 'email' });
  assert.equal(slugChanged({ name: 'Acme Corp', slug: ' ACME ' }, org), false);
});

test('a changed slug is sent normalized and needs confirmation', () => {
  const org = { name: 'Acme', slug: 'acme' };
  const body = orgPatchBody({ name: 'Acme', slug: ' Acme-2 ', oncall_display: 'name' }, org);
  assert.equal(body.slug, 'acme-2');
  assert.equal(slugChanged({ name: 'Acme', slug: 'acme-2' }, org), true);
});

test('slug validation matches the server rules', () => {
  assert.equal(normalizeSlug('  AbC '), 'abc');
  for (const ok of ['ab', 'acme', 'acme-2', 'a1-b2-c3']) assert.equal(slugError(ok), '', ok);
  for (const bad of ['', ' ', 'a', '-x', 'x-', 'a--b', 'a b', 'a_b', 'api', 'x'.repeat(64)]) {
    assert.notEqual(slugError(bad), '', bad);
  }
});

test('grace days must be 0..30 whole days', () => {
  for (const ok of ['0', '7', '30']) assert.equal(graceDaysError(ok), '', ok);
  for (const bad of ['', '-1', '31', '1.5', 'x']) assert.notEqual(graceDaysError(bad), '', bad);
});

test('MFA gap text', () => {
  assert.equal(mfaGapText(null), '');
  assert.equal(mfaGapText({ members_total: 3, members_without_mfa: 0 }), 'All 3 members have MFA set up.');
  assert.equal(mfaGapText({ members_total: 5, members_without_mfa: 2 }), '2 of 5 members have not set up MFA yet.');
});

test('org MFA section visibility', () => {
  assert.equal(orgMFASectionMode('member', true), 'hidden');
  assert.equal(orgMFASectionMode('viewer', true), 'hidden');
  assert.equal(orgMFASectionMode('admin', false), 'upgrade');
  assert.equal(orgMFASectionMode('owner', true), 'manage');
});
