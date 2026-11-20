import { signal } from '@preact/signals';

// True when the current session is a read-only staff impersonation.
export const staffReadonly = signal(false);

// Supplied by the SaaS session endpoint; the FOSS edition never sets it.
export const readonlyMessage = signal('This is a read-only session.');

export const READONLY_TOAST_EVENT = 'yipyap:toast';

export function setStaffReadonly(value) {
  staffReadonly.value = !!value;
}

export function setReadonlyMessage(message) {
  if (message) readonlyMessage.value = message;
}

// Show the read-only warning through the global toast host.
export function notifyReadonly(message) {
  if (typeof window === 'undefined') return;
  window.dispatchEvent(new CustomEvent(READONLY_TOAST_EVENT, {
    detail: { message: message || readonlyMessage.value, type: 'warning' },
  }));
}
