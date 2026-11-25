import { signal } from '@preact/signals';

// Set when the server rejects a request with code password_change_required
// (an admin reset this user's password). The app then shows only the
// change-password screen until the user picks a new password.
export const passwordChangeRequired = signal(false);

export function setPasswordChangeRequired(value) {
  passwordChangeRequired.value = !!value;
}
