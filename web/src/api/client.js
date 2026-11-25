import { upgradePrompt } from '../state/upgrade';
import { staffReadonly, setStaffReadonly, notifyReadonly, readonlyMessage, setReadonlyMessage, markMFABlocked } from '../edition/mfa';
import { setPasswordChangeRequired } from '../state/session';

const API_BASE = '/api/v1';

// In-memory token for the current session only - never persisted to localStorage.
// The authoritative session is the HttpOnly yipyap_session cookie set by the
// server. This variable exists only so callers can check "is there a token in
// this session" without a round-trip, and to send the Authorization header as
// a fallback during the transition period for non-browser clients.
let token = null;

export function setToken(t) {
  token = t ?? null;
}

// Logout must work even in a read-only staff session, otherwise the operator
// cannot leave the impersonated view.
const READONLY_WRITE_EXEMPT = new Set(['/auth/logout']);

export async function api(path, options = {}) {
  const { noRedirect, ...fetchOpts } = options;
  const method = (fetchOpts.method || 'GET').toUpperCase();

  // Stop mutating requests before they leave the browser. The server enforces
  // this too; this guard exists so every click gets a clear toast instead of a
  // silent 403 (or an unhandled rejection when a handler has no catch).
  if (staffReadonly.value && method !== 'GET' && method !== 'HEAD' &&
      !READONLY_WRITE_EXEMPT.has(path)) {
    const message = readonlyMessage.value;
    notifyReadonly(message);
    const err = new Error(message);
    err.status = 403;
    err.readonly = true;
    throw err;
  }

  const headers = { 'Content-Type': 'application/json', ...fetchOpts.headers };
  // Include Authorization header as a fallback (e.g. for clients that don't
  // support cookies). The server accepts both; the cookie takes precedence.
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${API_BASE}${path}`, {
    ...fetchOpts,
    headers,
    // Send the HttpOnly session cookie with every request.
    credentials: 'same-origin',
  });

  if (res.status === 401 && !path.startsWith('/auth/')) {
    // A revoked session gets a code so the sign-in page can say why.
    const body = await res.json().catch(() => ({}));
    if (!noRedirect) {
      setToken(null);
      window.location.href = body.code === 'session_revoked' ? '/login?reason=signed_out' : '/login';
      const err = new Error('Unauthorized');
      err.status = 401;
      throw err;
    }
    // The caller handles the 401 itself (for example a wrong password or a
    // missing step-up proof), so it gets the server's message and code.
    const err = new Error(body.error || 'Unauthorized');
    err.status = 401;
    err.body = body;
    throw err;
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));

    // The sign-in rate limiter answers 429 in plain text with a Retry-After
    // header, so there is no JSON error. Give every caller a sentence it can
    // show as is instead of "HTTP 429" (auth-16).
    if (res.status === 429) {
      if (!body.error) body.error = 'Too many attempts. Wait a minute and try again.';
      const retry = parseInt(res.headers.get('Retry-After') || '', 10);
      if (body.retry_after == null && retry > 0) body.retry_after = retry;
    }

    // The server marks read-only write rejections. Learn about the session here
    // even if the initial probe was skipped, and surface the same toast the
    // local guard uses.
    if (res.status === 403 && body.staff_readonly) {
      setStaffReadonly(true);
      setReadonlyMessage(body.error);
      notifyReadonly(body.error);
    }

    // The org requires MFA and this user's grace period is over. Switch the
    // app to the enrollment-only screen.
    if (res.status === 403 && body.code === 'mfa_enrollment_required') {
      markMFABlocked(body.deadline);
    }

    // An admin reset this user's password. Only the change-password screen
    // works until they choose a new one.
    if (res.status === 403 && body.code === 'password_change_required') {
      setPasswordChangeRequired(true);
    }

    // The account was disabled mid-session. Every further call would fail,
    // so end the session and explain why on the sign-in page.
    if (res.status === 403 && body.code === 'account_disabled' && !noRedirect &&
        window.location.pathname !== '/login') {
      setToken(null);
      await fetch(`${API_BASE}/auth/logout`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
      }).catch(() => {});
      window.location.href = '/login?reason=disabled';
    }

    // Plan-gate 403s carry an upgrade_required marker (set server-side on the
    // two plan-gate sources only). Surface the upgrade banner, then throw as
    // usual so callers still see the rejection.
    if (res.status === 403 && body.upgrade_required) {
      upgradePrompt.value = body.error || 'This feature requires a paid plan.';
    }
    const err = new Error(body.error || `HTTP ${res.status}`);
    err.status = res.status;
    err.body = body;
    err.readonly = !!body.staff_readonly;
    throw err;
  }

  if (res.status === 204) return null;
  return res.json();
}

export const get = (path, options) => api(path, options);
export const post = (path, body, options) =>
  api(path, { method: 'POST', body: JSON.stringify(body), ...options });
export const patch = (path, body) =>
  api(path, { method: 'PATCH', body: JSON.stringify(body) });
export const put = (path, body) =>
  api(path, { method: 'PUT', body: JSON.stringify(body) });
export const del = (path) => api(path, { method: 'DELETE' });
