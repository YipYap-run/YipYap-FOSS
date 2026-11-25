import { signal } from '@preact/signals';

// recoverCredentials carries the email and password from a sign-in that the
// server answered with account_disabled to the account restore page, so the
// user is not asked to type the password they just entered. It lives only
// in memory: never in the URL or browser storage. The restore page takes it
// once and clears it, and a page reload drops it.
export const recoverCredentials = signal(null); // { email, password }

export function takeRecoverCredentials(email) {
  const creds = recoverCredentials.value;
  recoverCredentials.value = null;
  if (!creds || !creds.password) return null;
  if (email && creds.email.trim().toLowerCase() !== email.trim().toLowerCase()) return null;
  return creds;
}
