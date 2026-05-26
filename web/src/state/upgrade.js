import { signal } from '@preact/signals';

// Set by the API client when a plan-gate 403 (upgrade_required) is returned.
// A top-level component renders an upgrade banner while this is non-null.
// Kept in its own module so the API client can set it without importing auth.js
// (which imports the client, creating a cycle).
export const upgradePrompt = signal(null);

export function clearUpgradePrompt() {
  upgradePrompt.value = null;
}
