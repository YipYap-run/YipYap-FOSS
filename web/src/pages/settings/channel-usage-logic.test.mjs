import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usedByList, usedByLabel, unusedLabel, policyHref, stepsText, policyNamesText, needsDisableConfirm, disableConfirmText, deleteConfirmText, deleteBlockedBy, formSnapshot, channelOptionLabel, withoutPolicyParam, stepsWithoutActiveChannel, stepChannelWarning } from './channel-usage-logic.mjs';

const refs = [
  { policy_id: 'p1', policy_name: 'Backup', steps: [1] },
  { policy_id: 'p2', policy_name: 'Primary', steps: [1, 3] },
];

test('usedByList tolerates a missing field', () => {
  assert.deepEqual(usedByList({}), []);
  assert.deepEqual(usedByList({ used_by: null }), []);
  assert.equal(usedByList({ used_by: refs }), refs);
});

test('usedByLabel counts policies', () => {
  assert.equal(usedByLabel([]), '');
  assert.equal(usedByLabel(undefined), '');
  assert.equal(usedByLabel(refs.slice(0, 1)), 'Used by 1 policy');
  assert.equal(usedByLabel(refs), 'Used by 2 policies');
});

test('policyHref encodes the id', () => {
  assert.equal(policyHref('a b/c'), '/settings/escalation?policy=a%20b%2Fc');
});

test('stepsText', () => {
  assert.equal(stepsText([2]), 'step 2');
  assert.equal(stepsText([1, 3]), 'steps 1 and 3');
  assert.equal(stepsText([1, 2, 4]), 'steps 1, 2 and 4');
  assert.equal(stepsText(null), '');
});

test('disableConfirmText only when an in-use channel is being disabled', () => {
  assert.equal(disableConfirmText(true, true, refs), '');
  assert.equal(disableConfirmText(false, false, refs), '');
  assert.equal(disableConfirmText(true, false, []), '');
  const t = disableConfirmText(true, false, refs);
  assert.match(t, /^These 2 escalation policies send .*: Backup and Primary\.$/);
  assert.match(disableConfirmText(true, false, refs.slice(0, 1)), /^This escalation policy sends .*: Backup\.$/);
});

test('deleteBlockedBy reads the row and the 409 body', () => {
  assert.equal(deleteBlockedBy({ used_by: [] }), null);
  assert.equal(deleteBlockedBy({ used_by: refs }), refs);
  assert.deepEqual(deleteBlockedBy(null, { status: 409, body: { error: 'x', used_by: refs } }), refs);
  assert.equal(deleteBlockedBy(null, { status: 500, body: {} }), null);
  assert.equal(deleteBlockedBy(null, { status: 409, body: {} }), null);
});

test('stepsWithoutActiveChannel flags steps that page nobody', () => {
  const channels = [
    { id: 'on', enabled: true, plan_status: 'ok' },
    { id: 'off', enabled: false },
    { id: 'paused', enabled: true, plan_status: 'disabled' },
    { id: 'grace', enabled: true, plan_status: 'grace' },
  ];
  const steps = [
    { targets: [{ channel_id: 'on' }] },
    { targets: [{ channel_id: 'off' }, { channel_id: 'paused' }] },
    { targets: [] },
    { targets: [{ channel_id: 'gone' }] },
    { targets: [{ channel_id: 'off' }, { channel_id: 'grace' }] },
  ];
  assert.deepEqual(stepsWithoutActiveChannel(steps, channels), [2, 3, 4]);
  assert.deepEqual(stepsWithoutActiveChannel(steps, null), []);
});

test('stepChannelWarning names the reason', () => {
  const channels = [{ id: 'off', enabled: false }, { id: 'paused', enabled: true, plan_status: 'disabled' }];
  assert.match(stepChannelWarning({ targets: [] }, channels), /no notification channel/);
  assert.match(stepChannelWarning({ targets: [{ channel_id: 'off' }] }, channels), /disabled/);
  assert.match(stepChannelWarning({ targets: [{ channel_id: 'paused' }] }, channels), /paused by your plan/);
  assert.match(stepChannelWarning({ targets: [{ channel_id: 'off' }, { channel_id: 'gone' }] }, channels), /deleted/);
});

test('unusedLabel only when the API says the channel is unused', () => {
  assert.equal(unusedLabel({}), '');
  assert.equal(unusedLabel({ used_by: refs }), '');
  assert.match(unusedLabel({ used_by: [] }), /Not in any escalation policy/);
});

test('policyNamesText joins names', () => {
  assert.equal(policyNamesText([]), '');
  assert.equal(policyNamesText([{ policy_name: 'A' }]), 'A');
  assert.equal(policyNamesText([{ policy_name: 'A' }, { policy_name: '' }, { policy_name: 'C' }]), 'A, Unnamed policy and C');
});

test('needsDisableConfirm only for an in-use channel being turned off', () => {
  assert.equal(needsDisableConfirm(true, false, refs), true);
  assert.equal(needsDisableConfirm(true, true, refs), false);
  assert.equal(needsDisableConfirm(false, false, refs), false);
  assert.equal(needsDisableConfirm(true, false, []), false);
  assert.equal(needsDisableConfirm(true, false, undefined), false);
});

test('deleteConfirmText names the channel', () => {
  assert.equal(deleteConfirmText('Ops Slack'), 'Delete "Ops Slack"? This cannot be undone.');
  assert.equal(deleteConfirmText('  '), 'Delete this channel? This cannot be undone.');
});

test('formSnapshot ignores key order and bookkeeping keys', () => {
  const a = formSnapshot({ name: 'x', enabled: true, config: { url: 'u', _urlHost: 'h', headers: [{ name: 'A', value: '' }] } });
  const b = formSnapshot({ enabled: true, config: { headers: [{ value: '', name: 'A' }], url: 'u' }, name: 'x' });
  assert.equal(a, b);
  assert.notEqual(a, formSnapshot({ name: 'x', enabled: false, config: { url: 'u', headers: [{ name: 'A', value: '' }] } }));
});

test('channelOptionLabel flags channels that will not deliver', () => {
  assert.equal(channelOptionLabel({ name: 'Ops', type: 'slack', enabled: true }, 'Slack'), 'Ops (Slack)');
  assert.equal(channelOptionLabel({ name: 'Ops', type: 'slack', enabled: false }), 'Ops (slack), disabled');
  assert.equal(channelOptionLabel({ name: 'Ops', type: 'sms', enabled: true, plan_status: 'disabled' }, 'SMS'), 'Ops (SMS), paused by plan');
});

test('withoutPolicyParam drops only the policy parameter', () => {
  assert.equal(withoutPolicyParam('?policy=p1'), '');
  assert.equal(withoutPolicyParam('?policy=p1&x=2'), '?x=2');
  assert.equal(withoutPolicyParam('?x=2'), null);
  assert.equal(withoutPolicyParam(''), null);
});
