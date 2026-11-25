import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseThreshold, thresholdSummary } from './channel-failure-alerts-logic.mjs';

test('accepts whole numbers in range', () => {
  assert.deepEqual(parseThreshold('1'), { value: 1 });
  assert.deepEqual(parseThreshold(' 20 '), { value: 20 });
  assert.deepEqual(parseThreshold(5, { min: 1, max: 20 }), { value: 5 });
});

test('rejects out of range, fractions, signs and text', () => {
  for (const raw of ['0', '21', '2.5', '-3', '+4', '', 'abc', '1e1', null, undefined]) {
    assert.ok(parseThreshold(raw).error, `accepted ${raw}`);
  }
});

test('uses the server bounds when given', () => {
  assert.ok(parseThreshold('8', { min: 1, max: 5 }).error);
  assert.match(parseThreshold('8', { min: 1, max: 5 }).error, /1 to 5/);
});

test('summary reads naturally for one and many', () => {
  assert.equal(thresholdSummary(1), 'Owners are emailed after the first failed delivery.');
  assert.equal(thresholdSummary(3), 'Owners are emailed after 3 failed deliveries in a row.');
});
