// Accessibility helpers shared by the Modal, toasts and settings forms.
// Kept free of Preact and browser globals so node --test can cover them.

// Selector for elements that can take keyboard focus inside a dialog.
export const FOCUSABLE = [
  'a[href]',
  'button:not([disabled])',
  'input:not([type=hidden]):not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(', ');

// trapTab returns the index to focus when Tab (or Shift+Tab) is pressed with
// focus at index `current` in a list of `count` focusable elements, or -1 to
// let the browser move focus normally. Focus wraps at both ends and is pulled
// back in when it sits outside the list (current < 0).
export function trapTab(count, current, shift) {
  if (count <= 0) return -2; // nothing focusable: keep focus on the dialog
  if (current < 0) return shift ? count - 1 : 0;
  if (shift && current === 0) return count - 1;
  if (!shift && current === count - 1) return 0;
  return -1;
}

// Toast timing. Errors stay until dismissed. Other toasts stay at least 5s
// and add about 60ms per character (roughly 200 words a minute), capped so a
// stale message does not cover the page for long.
export const TOAST_MIN_MS = 5000;
export const TOAST_MAX_MS = 15000;

export function toastDuration(message, type) {
  if (type === 'error') return null;
  const len = String(message ?? '').length;
  return Math.min(TOAST_MAX_MS, Math.max(TOAST_MIN_MS, len * 60));
}

// Errors go to an assertive alert region; everything else is polite.
export function toastRole(type) {
  return type === 'error' ? 'alert' : 'status';
}

const CONTROL_TAGS = new Set(['INPUT', 'SELECT', 'TEXTAREA']);

function isControl(el) {
  if (!el || !CONTROL_TAGS.has(el.tagName)) return false;
  return !(el.tagName === 'INPUT' && (el.getAttribute('type') || '').toLowerCase() === 'hidden');
}

function walk(el, fn) {
  for (const child of Array.from(el.children || [])) {
    fn(child);
    walk(child, fn);
  }
}

function controlsIn(el) {
  if (isControl(el)) return [el];
  const out = [];
  walk(el, c => { if (isControl(c)) out.push(c); });
  return out;
}

function insideLabel(el, stop) {
  for (let p = el.parentElement; p && p !== stop; p = p.parentElement) {
    if (p.tagName === 'LABEL') return true;
  }
  return false;
}

function hasName(el) {
  return el.hasAttribute('aria-label') || el.hasAttribute('aria-labelledby');
}

// associateLabels links every <label> under root that has no `for` and wraps
// no control to the control that follows it, the way settings forms are
// written (<label>Email</label><input/> as siblings). When the next sibling
// holds several controls (a group of checkboxes, an input plus a picker), it
// becomes role="group" named by the label instead. Labels already associated
// and controls that already have a name are left alone. `nextId` returns a
// fresh, document-unique id. Returns the number of labels it linked.
export function associateLabels(root, nextId) {
  if (!root) return 0;
  const labels = [];
  walk(root, el => { if (el.tagName === 'LABEL') labels.push(el); });
  const claimed = new Set();
  for (const label of labels) {
    const f = label.getAttribute('for');
    if (f) claimed.add(f);
  }
  let linked = 0;
  for (const label of labels) {
    if (label.getAttribute('for') || controlsIn(label).length > 0) continue;
    for (let sib = label.nextElementSibling; sib; sib = sib.nextElementSibling) {
      if (sib.tagName === 'LABEL') break;
      const controls = controlsIn(sib);
      if (controls.length === 0) continue;
      const free = controls.filter(c => !insideLabel(c, sib.parentElement) && !hasName(c) && !claimed.has(c.getAttribute('id')));
      if (controls.length === 1 && free.length === 1) {
        const control = free[0];
        let id = control.getAttribute('id');
        if (!id) { id = nextId(); control.setAttribute('id', id); }
        label.setAttribute('for', id);
        claimed.add(id);
        linked++;
      } else if (controls.length > 1 && !sib.hasAttribute('role')) {
        let lid = label.getAttribute('id');
        if (!lid) { lid = nextId(); label.setAttribute('id', lid); }
        sib.setAttribute('role', 'group');
        sib.setAttribute('aria-labelledby', lid);
        linked++;
      }
      break;
    }
  }
  return linked;
}

// tabbable narrows a list of FOCUSABLE matches to what Tab actually visits,
// so the dialog trap knows the real first and last stops. It drops elements
// with tabindex="-1" and, in a radio group with a checked option, every
// radio but the checked one (the browser skips the others). Without this,
// Shift+Tab from a checked radio that is not first in the list escapes the
// dialog.
export function tabbable(items) {
  const list = Array.from(items || []);
  const checkedGroups = new Set();
  for (const el of list) {
    if (isRadio(el) && el.checked && el.getAttribute('name')) checkedGroups.add(el.getAttribute('name'));
  }
  return list.filter(el => {
    if (el.getAttribute('tabindex') === '-1') return false;
    if (!isRadio(el)) return true;
    const name = el.getAttribute('name');
    return !name || !checkedGroups.has(name) || el.checked;
  });
}

function isRadio(el) {
  return el.tagName === 'INPUT' && (el.getAttribute('type') || '').toLowerCase() === 'radio';
}

// TOAST_LIMIT caps how many toasts are on screen. Errors stay until
// dismissed, so a run of failures could otherwise cover the page.
export const TOAST_LIMIT = 4;

// addToast appends a toast unless the same message of the same type is
// already showing (a retried action that fails again should not stack a
// copy). Past TOAST_LIMIT the oldest toast is dropped.
export function addToast(list, toast, limit = TOAST_LIMIT) {
  const current = list || [];
  if (current.some(t => t.message === toast.message && t.type === toast.type)) return current;
  const next = [...current, toast];
  return next.length > limit ? next.slice(next.length - limit) : next;
}
