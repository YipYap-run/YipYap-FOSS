import { signal, computed } from '@preact/signals';

export const staffReadonly = signal(false);
export const staffSession = signal(null);
export const readonlyMessage = signal('This is a read-only session.');
export const READONLY_TOAST_EVENT = 'yipyap:toast';

export function setStaffReadonly() {}
export function setStaffSession() {}
export function setReadonlyMessage() {}
export function notifyReadonly() {}

export const mfaEnforcement = signal(null);
export const mfaBlocked = computed(() => false);
export const mfaEnrollmentHold = signal(false);

export function setMFAEnforcement() {}
export function markMFABlocked() {}
export function clearMFAGraceDismissal() {}
export function dismissMFAGrace() {}

export const mfaGraceActive = computed(() => false);
export const mfaGraceDismissed = computed(() => false);

export function formatDeadline() {
  return '';
}
