import { signal, computed } from '@preact/signals';
import { post, get, setToken } from '../api/client';
import { connectWS, disconnectWS } from '../api/ws';
import { passwordChangeRequired, setPasswordChangeRequired } from './session';
import {
  setStaffReadonly, setReadonlyMessage, setStaffSession,
  mfaEnforcement, setMFAEnforcement, mfaEnrollmentHold, clearMFAGraceDismissal,
} from '../edition/mfa';
import { ssoLinkHeaders, noteSSORequired } from './sso-link';
import { metaIsStale } from '../components/promo-copy.mjs';

export const currentUser = signal(null);
export const currentOrg = signal(null);
export const authLoading = signal(true);
export const billingStatus = signal(null);
export const appMeta = signal(null);
export const isLoggedIn = computed(() => !!currentUser.value);
export const mfaState = signal(null); // { mfa_token, mfa_methods }

// True when the org may use paid (Pro+) features. promo counts as paid.
// In FOSS edition these features are build-stripped, so don't gate there.
export const isPaidPlan = computed(() => {
  if (appMeta.value?.edition === 'foss') return true;
  const plan = appMeta.value?.plan ?? currentOrg.value?.plan;
  return !!plan && plan !== 'free';
});

// refreshMFAEnforcement reloads the org MFA policy state for this session
// (grace deadline, blocked, SSO exempt). SaaS only: FOSS has no org MFA
// policy and no /auth/session endpoint. A failed probe keeps the last known
// state; any 403 mfa_enrollment_required still flips the app to blocked.
export async function refreshMFAEnforcement() {
  if (appMeta.value?.edition === 'foss') {
    setMFAEnforcement(null);
    return null;
  }
  try {
    const info = await get('/auth/session', { noRedirect: true });
    // The server omits the field when it could not evaluate the policy;
    // keep the last known state then instead of hiding the banner.
    if (info && 'mfa_enforcement' in info) setMFAEnforcement(info.mfa_enforcement);
  } catch (_) {
    // Keep the last known state.
  }
  return mfaEnforcement.value;
}

// A user blocked by org MFA enforcement or a forced password change only
// sees a single screen, so there is nothing to stream to them.
function mustChangePassword() {
  return passwordChangeRequired.value || !!currentUser.value?.force_password_change;
}

function connectWSUnlessBlocked() {
  if (!mfaEnforcement.value?.blocked && !mustChangePassword()) connectWS();
}

// When /meta last loaded, for refreshMetaIfStale.
let metaLoadedAt = 0;

export async function loadMeta() {
  try {
    appMeta.value = await get('/meta');
    metaLoadedAt = Date.now();
  } catch (_) {
    appMeta.value = { edition: 'foss', billing_enabled: false };
  }
}

// refreshMetaIfStale reloads /meta when it is older than maxAgeMs. A failed
// reload keeps the current value rather than falling back to FOSS defaults.
export async function refreshMetaIfStale(maxAgeMs) {
  if (!metaIsStale(metaLoadedAt, Date.now(), maxAgeMs)) return;
  try {
    appMeta.value = await get('/meta', { noRedirect: true });
    metaLoadedAt = Date.now();
  } catch (_) {
    // Keep what we have; the next visibility change tries again.
  }
}

// completeLogin finishes any login that returned {token, user}: password
// after MFA, WebAuthn MFA, and passkey. Every one of those server handlers
// sets the HttpOnly yipyap_session cookie, which is what keeps the session
// across reloads and authenticates /ws. The in-memory token copy is only an
// Authorization header fallback for this tab.
export async function completeLogin(token, user) {
  setToken(token);
  setStaffReadonly(false);
  currentUser.value = user;
  await refreshMFAEnforcement();
  // Billing belongs to the org of the previous session, if any.
  billingStatus.value = null;
  try {
    currentOrg.value = await get('/org');
  } catch (_) {
    currentOrg.value = null;
  }
  // /meta returns plan, promo and retention only with a session.
  await loadMeta();
  connectWSUnlessBlocked();
}

export async function login(email, password) {
  let res;
  try {
    res = await post('/auth/login', { email, password }, { headers: ssoLinkHeaders() });
  } catch (err) {
    // The org enforces SSO: the sign-in page switches to the SSO form.
    noteSSORequired(err, email);
    // 403 with a body signals either account_disabled or email_not_verified.
    if (err.status === 403 && err.body) {
      if (err.body.email_not_verified) {
        return { email_not_verified: true, email: err.body.email };
      }
      if (err.body.account_disabled) {
        return { account_disabled: true };
      }
    }
    throw err;
  }
  if (res.mfa_required) {
    return { mfa_required: true, mfa_token: res.mfa_token, mfa_methods: res.mfa_methods };
  }
  if (res.account_disabled) {
    return { account_disabled: true };
  }
  if (res.email_not_verified) {
    return { email_not_verified: true, email: res.email };
  }
  // Server set the HttpOnly cookie; mirror token in-memory for fallback only.
  setToken(res.token);
  currentUser.value = res.user;
  await refreshMFAEnforcement();
  // Load the org (plan, name) so isPaidPlan gates correctly. The login
  // response carries no org, so fetch it now that the token is set.
  try {
    currentOrg.value = await get('/org');
  } catch (_) {
    currentOrg.value = res.org || null;
  }
  await loadMeta();
  connectWSUnlessBlocked();
  return res;
}

export async function register(orgName, email, password, confirmPassword) {
  const res = await post('/auth/register', {
    org_name: orgName,
    email,
    password,
    confirm_password: confirmPassword,
  });
  // SaaS: server returns {status:"verification_sent", email}. Don't log in.
  if (res && res.status === 'verification_sent') {
    return res;
  }
  // FOSS: server returns a session token.
  if (res && res.token) {
    setToken(res.token);
    currentUser.value = res.user;
    try {
      currentOrg.value = await get('/org');
    } catch (_) {
      currentOrg.value = res.org || null;
    }
    await loadMeta();
    connectWS();
  }
  return res;
}

export async function resendVerification(email) {
  return post('/auth/resend-verification', { email });
}

export async function verifyEmail(token) {
  return post('/auth/verify-email', { token });
}

export async function logout() {
  // Ask the server to clear the HttpOnly cookie.
  try {
    await post('/auth/logout', {});
  } catch (_) {
    // Best-effort - clear local state regardless.
  }
  setToken(null);
  setStaffReadonly(false);
  currentUser.value = null;
  currentOrg.value = null;
  setMFAEnforcement(null);
  setPasswordChangeRequired(false);
  clearMFAGraceDismissal();
  mfaEnrollmentHold.value = false;
  billingStatus.value = null;
  disconnectWS();
  // Drop the org fields the signed-in /meta carried.
  await loadMeta();
}

export async function loadUser() {
  authLoading.value = true;
  await loadMeta();
  try {
    // SaaS: ask the server who this session belongs to. /auth/refresh cannot
    // be used for read-only staff tokens (it is a write and the guard rejects
    // it), so this GET is the reliable way to detect impersonation on load.
    if (appMeta.value?.edition !== 'foss') {
      try {
        const info = await get('/auth/session', { noRedirect: true });
        setStaffReadonly(!!info.staff_readonly);
        if ('mfa_enforcement' in info) setMFAEnforcement(info.mfa_enforcement);
        if (info.message) setReadonlyMessage(info.message);
        if (info.staff_readonly) {
          setStaffSession(info.impersonation || null);
          const actor = info.impersonation?.actor;
          currentUser.value = {
            id: info.user_id,
            role: info.role,
            name: 'Support (read-only)',
            email: actor ? `Support: ${actor}` : 'Read-only support session',
          };
          try {
            currentOrg.value = await get('/org', { noRedirect: true });
          } catch (_) {
            currentOrg.value = null;
          }
          connectWS();
          return;
        }
      } catch (_) {
        // Endpoint may not exist on older deployments, or no session yet.
      }
    }

    try {
      // Try to refresh - the server will accept the HttpOnly cookie and issue a
      // fresh one. If this succeeds we know the session is valid.
      // Refresh may fail for impersonation tokens - that's OK, use as-is.
      try {
        const res = await post('/auth/refresh', {});
        if (res.token) setToken(res.token);
        if (res.user) currentUser.value = res.user;
      } catch (err) {
        // The org now enforces SSO and the server revoked this password
        // session. Send the person to the SSO form, not a read-only view.
        if (noteSSORequired(err)) {
          setToken(null);
          // The app shell reloads /login without state, so say why in the URL.
          if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
            window.location.replace('/login?reason=sso_required');
            return;
          }
          authLoading.value = false;
          return;
        }
        // Token may still be valid for reads even if refresh fails.
        // Set a synthetic user so the UI doesn't redirect to login.
        if (!currentUser.value && appMeta.value?.edition !== 'foss') {
          // Probe /org to see if the cookie-only session is still usable.
          try {
            const org = await get('/org', { noRedirect: true });
            currentOrg.value = org;
            currentUser.value = { id: 'session', role: 'viewer', name: 'Read-only session' };
            connectWSUnlessBlocked();
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

      if (appMeta.value?.billing_enabled && !mfaEnforcement.value?.blocked && !mustChangePassword()) {
        try {
          const billing = await get('/billing', { noRedirect: true });
          billingStatus.value = billing;
        } catch (_) {
          billingStatus.value = null;
        }
      }

      connectWSUnlessBlocked();
    } catch (_) {
      setToken(null);
      currentUser.value = null;
    }
  } finally {
    authLoading.value = false;
  }
}
