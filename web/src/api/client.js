import { upgradePrompt } from '../state/upgrade';
import { staffReadonly, setStaffReadonly, notifyReadonly, readonlyMessage, setReadonlyMessage } from '../state/session';

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
    if (!noRedirect) {
      setToken(null);
      window.location.href = '/login';
    }
    throw new Error('Unauthorized');
  }

  if (!res.ok) {
    const body = await res.json().catch(() => ({}));

    // The server marks read-only write rejections. Learn about the session here
    // even if the initial probe was skipped, and surface the same toast the
    // local guard uses.
    if (res.status === 403 && body.staff_readonly) {
      setStaffReadonly(true);
      setReadonlyMessage(body.error);
      notifyReadonly(body.error);
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
