import { test } from 'node:test';
import assert from 'node:assert/strict';
import { trapTab, toastDuration, toastRole, associateLabels, tabbable, addToast, TOAST_LIMIT, TOAST_MIN_MS, TOAST_MAX_MS } from './a11y.mjs';

// Minimal element tree: enough of the DOM for associateLabels.
function h(tag, attrs = {}, ...children) {
  const el = {
    tagName: tag.toUpperCase(),
    attrs: { ...attrs },
    children,
    parentElement: null,
    nextElementSibling: null,
    getAttribute(n) { return n in this.attrs ? this.attrs[n] : null; },
    setAttribute(n, v) { this.attrs[n] = String(v); },
    hasAttribute(n) { return n in this.attrs; },
  };
  children.forEach((c, i) => { c.parentElement = el; c.nextElementSibling = children[i + 1] || null; });
  return el;
}

function ids() {
  let n = 0;
  return () => `f${++n}`;
}

test('trapTab wraps focus at both ends and pulls stray focus back in', () => {
  assert.equal(trapTab(3, 2, false), 0);
  assert.equal(trapTab(3, 0, true), 2);
  assert.equal(trapTab(3, 1, false), -1);
  assert.equal(trapTab(3, 1, true), -1);
  assert.equal(trapTab(3, -1, false), 0);
  assert.equal(trapTab(3, -1, true), 2);
  assert.equal(trapTab(0, -1, false), -2);
});

test('errors persist, other toasts scale with length within bounds', () => {
  assert.equal(toastDuration('Could not save', 'error'), null);
  assert.equal(toastDuration('Saved', 'success'), TOAST_MIN_MS);
  const long = 'Temporary password created. Share it securely; they must change it at next login.';
  assert.ok(toastDuration(long, 'success') >= TOAST_MIN_MS);
  assert.ok(toastDuration(long + long, 'success') > toastDuration(long, 'success'));
  assert.equal(toastDuration('x'.repeat(5000), 'warning'), TOAST_MAX_MS);
  assert.equal(toastDuration(undefined), TOAST_MIN_MS);
});

test('error toasts use role=alert, others role=status', () => {
  assert.equal(toastRole('error'), 'alert');
  assert.equal(toastRole('success'), 'status');
  assert.equal(toastRole('warning'), 'status');
});

test('sibling label and input get linked with a generated id', () => {
  const label = h('label');
  const input = h('input', { type: 'email' });
  const root = h('div', {}, h('div', { class: 'form-group' }, label, input));
  assert.equal(associateLabels(root, ids()), 1);
  assert.equal(input.getAttribute('id'), 'f1');
  assert.equal(label.getAttribute('for'), 'f1');
});

test('an existing control id is reused', () => {
  const label = h('label');
  const select = h('select', { id: 'role' });
  associateLabels(h('div', {}, label, select), ids());
  assert.equal(label.getAttribute('for'), 'role');
});

test('control nested in a wrapper div is found', () => {
  const label = h('label');
  const input = h('input', { type: 'color' });
  associateLabels(h('div', {}, label, h('div', {}, input)), ids());
  assert.equal(label.getAttribute('for'), input.getAttribute('id'));
});

test('a label with several controls after it names a group', () => {
  const label = h('label');
  const wrap = h('div', {}, h('input', { type: 'color' }), h('input', { type: 'text' }));
  associateLabels(h('div', {}, label, wrap), ids());
  assert.equal(label.getAttribute('for'), null);
  assert.equal(wrap.getAttribute('role'), 'group');
  assert.equal(wrap.getAttribute('aria-labelledby'), label.getAttribute('id'));
});

test('already associated, wrapping and named controls are left alone', () => {
  const wrapping = h('label', {}, h('input', { type: 'checkbox' }));
  const linked = h('label', { for: 'x' });
  const x = h('input', { id: 'x' });
  const plain = h('label');
  const named = h('input', { 'aria-label': 'Search' });
  const root = h('div', {}, wrapping, linked, x, plain, named);
  assert.equal(associateLabels(root, ids()), 0);
  assert.equal(wrapping.getAttribute('for'), null);
  assert.equal(plain.getAttribute('for'), null);
  assert.equal(named.getAttribute('id'), null);
});

test('hidden inputs are skipped and the next label stops the search', () => {
  const a = h('label');
  const hidden = h('input', { type: 'hidden' });
  const b = h('label');
  const input = h('input');
  associateLabels(h('div', {}, a, hidden, b, input), ids());
  assert.equal(a.getAttribute('for'), null);
  assert.equal(b.getAttribute('for'), input.getAttribute('id'));
});

test('running twice does not relink or create new ids', () => {
  const label = h('label');
  const input = h('textarea');
  const root = h('div', {}, label, input);
  const next = ids();
  associateLabels(root, next);
  assert.equal(associateLabels(root, next), 0);
  assert.equal(input.getAttribute('id'), 'f1');
});

function input(type, name, checked = false, attrs = {}) {
  return { ...h('input', { type, name, ...attrs }), checked };
}

test('tabbable keeps only the checked radio of a group and drops tabindex=-1', () => {
  const first = input('text', 'n');
  const a = input('radio', 'mode', false);
  const b = input('radio', 'mode', true);
  const skip = h('button', { tabindex: '-1' });
  const last = h('button');
  const out = tabbable([first, a, b, skip, last]);
  assert.deepEqual(out, [first, b, last]);
});

test('tabbable keeps every radio when none in the group is checked', () => {
  const a = input('radio', 'keys');
  const b = input('radio', 'keys');
  const cancel = h('button');
  assert.deepEqual(tabbable([a, b, cancel]), [a, b, cancel]);
});

test('Shift+Tab from a checked radio that opens the dialog wraps to the end', () => {
  // Remove-member dialog with "Keep" chosen: the unchecked "Revoke" radio
  // comes first in the DOM but Tab never stops there.
  const revoke = input('radio', 'keys', false);
  const keep = input('radio', 'keys', true);
  const submit = h('button');
  const cancel = h('button');
  const items = tabbable([revoke, keep, submit, cancel]);
  assert.equal(trapTab(items.length, items.indexOf(keep), true), items.length - 1);
});

test('addToast skips a duplicate showing toast and caps the list', () => {
  const one = addToast([], { id: 1, message: 'Saved', type: 'success' });
  assert.equal(addToast(one, { id: 2, message: 'Saved', type: 'success' }), one);
  assert.equal(addToast(one, { id: 3, message: 'Saved', type: 'error' }).length, 2);
  let list = [];
  for (let i = 0; i < TOAST_LIMIT + 2; i++) list = addToast(list, { id: i, message: `m${i}`, type: 'error' });
  assert.equal(list.length, TOAST_LIMIT);
  assert.equal(list[0].id, 2);
  assert.equal(list[list.length - 1].id, TOAST_LIMIT + 1);
});
