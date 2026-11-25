// Run with: cd web && npm test
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { toastLifetime, toastRegion, testResultLifetime, trapFocusIndex, pushDialog, removeDialog, isTopDialog, FOCUSABLE } from './ui-logic.mjs';

test('error toasts stay until dismissed, others time out', () => {
  assert.equal(toastLifetime('error'), null);
  assert.equal(toastLifetime('success'), 3000);
  assert.equal(toastLifetime('warning'), 3000);
  assert.equal(toastLifetime(undefined), 3000);
});

test('errors go to the alert region, the rest to the polite status region', () => {
  assert.equal(toastRegion('error'), 'alert');
  assert.equal(toastRegion('success'), 'status');
  assert.equal(toastRegion('warning'), 'status');
});

test('a failed channel Test stays, a sent one clears', () => {
  assert.equal(testResultLifetime('fail'), null);
  assert.equal(testResultLifetime('ok'), 3000);
});

test('Tab wraps from the last element to the first', () => {
  assert.equal(trapFocusIndex(3, 2, false), 0);
  assert.equal(trapFocusIndex(3, 1, false), null);
});

test('Shift+Tab wraps from the first element to the last', () => {
  assert.equal(trapFocusIndex(3, 0, true), 2);
  assert.equal(trapFocusIndex(3, 1, true), null);
});

test('focus on the dialog itself or outside moves to an edge', () => {
  assert.equal(trapFocusIndex(3, -1, false), 0);
  assert.equal(trapFocusIndex(3, -1, true), 2);
});

test('a dialog with nothing focusable leaves focus where it is', () => {
  assert.equal(trapFocusIndex(0, -1, false), null);
});

test('one focusable element keeps focus on itself', () => {
  assert.equal(trapFocusIndex(1, 0, false), 0);
  assert.equal(trapFocusIndex(1, 0, true), 0);
});

test('only the innermost dialog is on top', () => {
  const a = {}, b = {};
  pushDialog(a);
  assert.ok(isTopDialog(a));
  pushDialog(b);
  assert.ok(!isTopDialog(a));
  assert.ok(isTopDialog(b));
  removeDialog(b);
  assert.ok(isTopDialog(a));
  removeDialog(a);
  assert.ok(!isTopDialog(a));
});

test('disabled controls and tabindex=-1 are not focusable', () => {
  assert.match(FOCUSABLE, /button:not\(\[disabled\]\)/);
  assert.match(FOCUSABLE, /\[tabindex\]:not\(\[tabindex="-1"\]\)/);
});

test('Escape closes only the top dialog, and not when content inside handled it', async () => {
  const { closesDialog } = await import('./ui-logic.mjs');
  assert.equal(closesDialog({ key: 'Escape', defaultPrevented: false }, true), true);
  assert.equal(closesDialog({ key: 'Escape', defaultPrevented: false }, false), false);
  // An inline confirm inside the form cancels itself first.
  assert.equal(closesDialog({ key: 'Escape', defaultPrevented: true }, true), false);
  assert.equal(closesDialog({ key: 'Tab', defaultPrevented: false }, true), false);
  assert.equal(closesDialog(null, true), false);
});

test('opening a dialog does not take focus from a field the page already focused', async () => {
  const { initialFocusTarget } = await import('./ui-logic.mjs');
  const dialog = { n: 'dialog' }, first = { n: 'first' }, auto = { n: 'auto' };
  assert.equal(initialFocusTarget({ activeInside: true, autofocus: auto, firstControl: first, dialog }), null);
  assert.equal(initialFocusTarget({ activeInside: false, autofocus: auto, firstControl: first, dialog }), auto);
  assert.equal(initialFocusTarget({ activeInside: false, autofocus: null, firstControl: first, dialog }), first);
  assert.equal(initialFocusTarget({ activeInside: false, autofocus: null, firstControl: null, dialog }), dialog);
});
