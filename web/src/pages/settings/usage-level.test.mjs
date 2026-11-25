import { test } from 'node:test';
import assert from 'node:assert/strict';
import { usageLevel, usageText, usagePercent, usageValueText, usageFlag, hasMeter } from './usage-level.mjs';

test('usageLevel thresholds', () => {
  assert.equal(usageLevel(0, 100), 'ok');
  assert.equal(usageLevel(79, 100), 'ok');
  assert.equal(usageLevel(80, 100), 'warn');
  assert.equal(usageLevel(99, 100), 'warn');
  assert.equal(usageLevel(100, 100), 'full');
  assert.equal(usageLevel(120, 100), 'full');
  assert.equal(usageLevel(20, 25), 'warn');
  assert.equal(usageLevel(5, -1), 'unlimited');
  assert.equal(usageLevel(5, undefined), 'unlimited');
  assert.equal(usageLevel(0, 0), 'full');
});

test('usageText matches the Billing copy', () => {
  assert.equal(usageText(80, 100), '80 of 100');
  assert.equal(usageText(20, 25), '20 of 25');
  assert.equal(usageText(12, -1), '12 sent, unlimited');
  assert.equal(usageText(undefined, 100), '0 of 100');
  assert.equal(usageText(-3, 100), '0 of 100');
});

test('usagePercent clamps', () => {
  assert.equal(usagePercent(80, 100), 80);
  assert.equal(usagePercent(150, 100), 100);
  assert.equal(usagePercent(5, -1), 0);
  assert.equal(usagePercent(5, 0), 0);
});

test('usageValueText states the level', () => {
  assert.equal(usageValueText('SMS', 10, 100), '10 of 100 SMS used');
  assert.equal(usageValueText('SMS', 80, 100), '80 of 100 SMS used, 80 percent');
  assert.equal(usageValueText('voice calls', 25, 25), '25 of 25 voice calls used, quota reached');
  assert.equal(usageValueText('SMS', 7, -1), '7 SMS sent, unlimited');
});

test('usageFlag shows the real percent from 80% up', () => {
  assert.equal(usageFlag(79, 100), '');
  assert.equal(usageFlag(80, 100), '80% used');
  assert.equal(usageFlag(95, 100), '95% used');
  assert.equal(usageFlag(24, 25), '96% used');
  assert.equal(usageFlag(199, 200), '99% used');
  assert.equal(usageFlag(100, 100), 'Quota reached');
  assert.equal(usageFlag(500, -1), '');
});

test('hasMeter skips unlimited and zero limits', () => {
  assert.equal(hasMeter(100), true);
  assert.equal(hasMeter(-1), false);
  assert.equal(hasMeter(0), false);
  assert.equal(hasMeter(undefined), false);
});
