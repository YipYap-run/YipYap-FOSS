import { signal, computed } from '@preact/signals';
import { post, get, setToken } from '../api/client';
import { connectWS, disconnectWS } from '../api/ws';

export const currentUser = signal(null);
export const currentOrg = signal(null);
export const authLoading = signal(true);
export const billingStatus = signal(null);
export const appMeta = signal(null);
export const isLoggedIn = computed(() => !!currentUser.value);
export const mfaState = signal(null); // { mfa_token, mfa_methods }

export async function loadMeta() {
  try {
    appMeta.value = await get('/meta');
  } catch (_) {
    appMeta.value = { edition: 'foss', billing_enabled: false };
  }
}

export async function completeLogin(token, user) {
  // Keep an in-memory copy of the token for the Authorization header fallback,
  // but the real session lives in the HttpOnly cookie set by the server.
  setToken(token);
  currentUser.value = user;
  try {
    currentOrg.value = await get('/org');
  } catch (_) {
    currentOrg.value = null;
  }
  connectWS();
}

export async function login(email, password) {
  const res = await post('/auth/login', { email, password });
  if (res.mfa_required) {
    return { mfa_required: true, mfa_token: res.mfa_token, mfa_methods: res.mfa_methods };
  }
  if (res.account_disabled) {
    return { account_disabled: true };
  }
  // Server set the HttpOnly cookie; mirror token in-memory for fallback only.
  setToken(res.token);
  currentUser.value = res.user;
  currentOrg.value = res.org || null;
  connectWS();
  return res;
}

export async function register(orgName, email, password) {
  const res = await post('/auth/register', { org_name: orgName, email, password });
  setToken(res.token);
  currentUser.value = res.user;
  currentOrg.value = res.org || null;
  connectWS();
  return res;
}

export async function logout() {
  // Ask the server to clear the HttpOnly cookie.
  try {
    await post('/auth/logout', {});
  } catch (_) {
    // Best-effort - clear local state regardless.
  }
  setToken(null);
  currentUser.value = null;
  currentOrg.value = null;
  disconnectWS();
}

export async function loadUser() {
  authLoading.value = true;
  await loadMeta();
  try {
    // Try to refresh - the server will accept the HttpOnly cookie and issue a
    // fresh one. If this succeeds we know the session is valid.
    // Refresh may fail for impersonation tokens - that's OK, use as-is.
    try {
      const res = await post('/auth/refresh', {});
      if (res.token) setToken(res.token);
      if (res.user) currentUser.value = res.user;
    } catch (_) {
      // Token may still be valid for reads even if refresh fails.
      // Set a synthetic user so the UI doesn't redirect to login.
      if (!currentUser.value && appMeta.value?.edition !== 'foss') {
        // Probe /org to see if the cookie-only session is still usable.
        try {
          const org = await get('/org', { noRedirect: true });
          currentOrg.value = org;
          currentUser.value = { id: 'session', role: 'viewer', name: 'Read-only session' };
          connectWS();
        } catch (_) {
          // No valid session at all.
          authLoading.value = false;
          return;
        }
      }
      authLoading.value = false;
      return;
    }

    try {
      const org = await get('/org', { noRedirect: true });
      currentOrg.value = org;
    } catch (_) {
      currentOrg.value = null;
    }

    if (appMeta.value?.billing_enabled) {
      try {
        const billing = await get('/billing', { noRedirect: true });
        billingStatus.value = billing;
      } catch (_) {
        billingStatus.value = null;
      }
    }

    connectWS();
  } catch (_) {
    setToken(null);
    currentUser.value = null;
  } finally {
    authLoading.value = false;
  }
}
