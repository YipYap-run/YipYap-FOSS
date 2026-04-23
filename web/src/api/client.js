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

export function getToken() {
  return token;
}

export async function api(path, options = {}) {
  const { noRedirect, ...fetchOpts } = options;
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
    throw new Error(body.error || `HTTP ${res.status}`);
  }

  if (res.status === 204) return null;
  return res.json();
}

export const get = (path, options) => api(path, options);
export const post = (path, body) =>
  api(path, { method: 'POST', body: JSON.stringify(body) });
export const patch = (path, body) =>
  api(path, { method: 'PATCH', body: JSON.stringify(body) });
export const put = (path, body) =>
  api(path, { method: 'PUT', body: JSON.stringify(body) });
export const del = (path) => api(path, { method: 'DELETE' });
