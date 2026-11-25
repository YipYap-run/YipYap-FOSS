import { logout } from './auth';

// An account without a password confirms a security change (adding or
// removing a sign-in method) with a fresh SSO sign-in. startSSOReauth signs
// out, opens the SSO form with the email filled in, and remembers where to
// come back to. The SSO callback reads the return path once.
//
// The record lives in sessionStorage so it survives the trip to the identity
// provider in this tab only, and expires after REAUTH_TTL_MS.

const STORAGE_KEY = 'yipyap.ssoReauth';
const REAUTH_TTL_MS = 15 * 60 * 1000;

// Only in-app settings paths are accepted as a return target, so a tampered
// storage value cannot send the user anywhere else.
const RETURN_PATH = /^\/settings(\/[a-z-]+)?$/;

function read() {
  let raw = null;
  try {
    raw = sessionStorage.getItem(STORAGE_KEY);
  } catch (_) {
    return null;
  }
  if (!raw) return null;
  try {
    const rec = JSON.parse(raw);
    if (!rec || typeof rec.at !== 'number' || Date.now() - rec.at > REAUTH_TTL_MS) {
      clearSSOReauth();
      return null;
    }
    if (typeof rec.path !== 'string' || !RETURN_PATH.test(rec.path)) {
      clearSSOReauth();
      return null;
    }
    return { email: typeof rec.email === 'string' ? rec.email : '', path: rec.path };
  } catch (_) {
    clearSSOReauth();
    return null;
  }
}

export function clearSSOReauth() {
  try {
    sessionStorage.removeItem(STORAGE_KEY);
  } catch (_) {
    // Storage blocked: nothing was stored either.
  }
}

// pendingSSOReauth returns { email, path } while a re-auth started from
// Settings is in progress, or null.
export function pendingSSOReauth() {
  return read();
}

// consumeSSOReauthReturn returns the settings path to go back to after the
// SSO sign-in finished, and forgets it.
export function consumeSSOReauthReturn() {
  const rec = read();
  clearSSOReauth();
  return rec ? rec.path : null;
}

export async function startSSOReauth(email, path = '/settings/security') {
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify({ email: email || '', path, at: Date.now() }));
  } catch (_) {
    // Without storage the user still gets the SSO form, just not the return.
  }
  // logout() clears session state but not this record.
  await logout();
  window.location.href = '/login?sso=1&reason=reauth';
}
