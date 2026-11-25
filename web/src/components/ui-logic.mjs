// Pure helpers behind the shared Modal and toasts in ui.jsx, kept free of
// Preact so `npm test` can run them under node.

// How long a toast stays, in ms. Errors stay until dismissed so a failure
// reason is never lost to a timer (WCAG 2.2.1).
export function toastLifetime(type) {
  return type === 'error' ? null : 3000;
}

// Which live region a toast goes in: errors interrupt (role=alert), the
// rest wait for a pause (role=status, aria-live=polite).
export function toastRegion(type) {
  return type === 'error' ? 'alert' : 'status';
}

// How long a channel Test result stays on its button, in ms. A failure
// stays until the next Test or until the person dismisses its note.
export function testResultLifetime(state) {
  return state === 'ok' ? 3000 : null;
}

// Focus trap for Tab inside a dialog. count is the number of focusable
// elements, index the position of the focused one (-1 when focus is on the
// dialog itself or outside it). Returns the index to move focus to, or
// null to let the browser move it.
export function trapFocusIndex(count, index, shift) {
  if (count <= 0) return null;
  if (index < 0) return shift ? count - 1 : 0;
  if (shift && index === 0) return count - 1;
  if (!shift && index === count - 1) return 0;
  return null;
}

// Elements that take Tab focus. Filtered for visibility at run time.
export const FOCUSABLE = [
  'a[href]',
  'area[href]',
  'button:not([disabled])',
  'input:not([disabled]):not([type="hidden"])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  'iframe',
  '[contenteditable="true"]',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

// Open dialogs, innermost last. Only the top one answers Escape and traps
// Tab, so a dialog opened from another closes on its own.
const stack = [];

export function pushDialog(token) {
  stack.push(token);
}

export function removeDialog(token) {
  const i = stack.lastIndexOf(token);
  if (i >= 0) stack.splice(i, 1);
}

export function isTopDialog(token) {
  return stack.length > 0 && stack[stack.length - 1] === token;
}

// Whether a keydown should close the dialog. Content inside the dialog
// that uses Escape itself (an inline confirm, for example) calls
// preventDefault or stopPropagation, and the dialog stays open, so the
// first Escape cancels the question and keeps the form's edits.
export function closesDialog(e, isTop) {
  return !!e && e.key === 'Escape' && !!isTop && !e.defaultPrevented;
}

// Where the dialog puts focus when it opens. A page that already moved
// focus to a field inside (to show an error, say) keeps it there.
// Otherwise: an [autofocus] element, then the first visible control, then
// the dialog itself.
export function initialFocusTarget({ activeInside, autofocus, firstControl, dialog }) {
  if (activeInside) return null;
  return autofocus || firstControl || dialog || null;
}
