import { signal } from '@preact/signals';
import { post } from '../api/client';

// Pending SSO link offer (decision core-auth 6). When an SSO sign-in matches
// an existing account by email, the server never links it on its own. It
// sends the browser to /login?sso_link=1&provider=X#sso_link_token=JWT. The
// token is short lived (10 minutes), is not a session, and only links for the
// account it names, and only after that account's owner signs in with a
// password, MFA or passkey and confirms.
//
// The offer lives in sessionStorage so it survives the password, MFA and
// passkey steps in this tab but not a new tab or a closed browser.

const STORAGE_KEY = 'yipyap.ssoLinkOffer';
const MAX_PROVIDER_LEN = 60;
const FALLBACK_TTL_MS = 10 * 60 * 1000;

// Bumped after a successful link so an open Settings, Security page reloads
// its linked identities list.
export const ssoLinksChanged = signal(0);

// { token, provider, expiresAt } or null.
export const ssoLinkOffer = signal(readStored());

function readStored() {
  let raw = null;
  try {
    raw = sessionStorage.getItem(STORAGE_KEY);
  } catch (_) {
    return null;
  }
  if (!raw) return null;
  try {
    const offer = JSON.parse(raw);
    if (!offer || typeof offer.token !== 'string' || !offer.token) return null;
    if (typeof offer.expiresAt !== 'number' || offer.expiresAt <= Date.now()) {
      removeStored();
      return null;
    }
    return {
      token: offer.token,
      provider: cleanProvider(offer.provider),
      expiresAt: offer.expiresAt,
    };
  } catch (_) {
    removeStored();
    return null;
  }
}

function removeStored() {
  try {
    sessionStorage.removeItem(STORAGE_KEY);
  } catch (_) {
    // Storage unavailable: the in-memory signal is all there is.
  }
}

// The provider name comes from the URL, so treat it as display text only:
// JSX escapes it, and it is trimmed so a crafted link cannot fill the screen.
function cleanProvider(p) {
  const s = typeof p === 'string' ? p.replace(/\s+/g, ' ').trim() : '';
  if (!s) return 'SSO';
  return s.length > MAX_PROVIDER_LEN ? s.slice(0, MAX_PROVIDER_LEN) + '...' : s;
}

// tokenExpiry reads exp from the JWT payload. The signature is not checked
// here (the server does that); this only decides when to stop offering.
export function tokenExpiry(token, now = Date.now()) {
  const fallback = now + FALLBACK_TTL_MS;
  const parts = String(token).split('.');
  if (parts.length !== 3) return fallback;
  try {
    const b64 = parts[1].replace(/-/g, '+').replace(/_/g, '/');
    const padded = b64 + '='.repeat((4 - (b64.length % 4)) % 4);
    const claims = JSON.parse(atob(padded));
    if (typeof claims.exp === 'number') {
      // Never trust a far-future exp from the URL beyond the server TTL.
      return Math.min(claims.exp * 1000, fallback);
    }
  } catch (_) {
    // Malformed payload: fall back to the server TTL.
  }
  return fallback;
}

// captureSSOLinkOffer reads an offer from the current URL, stores it, and
// removes the token from the address bar and history so it is not copied,
// bookmarked, or left behind. Returns the offer, or null when the URL has
// none.
export function captureSSOLinkOffer() {
  if (typeof window === 'undefined') return null;
  const params = new URLSearchParams(window.location.search);
  const hash = new URLSearchParams((window.location.hash || '').replace(/^#/, ''));
  const token = hash.get('sso_link_token');
  if (params.get('sso_link') !== '1' && !token) return null;
  const provider = params.get('provider') || '';

  // Strip the offer from the URL either way.
  params.delete('sso_link');
  params.delete('provider');
  const qs = params.toString();
  try {
    window.history.replaceState(window.history.state, '', window.location.pathname + (qs ? `?${qs}` : ''));
  } catch (_) {
    // Older browsers: leaving the URL as is only costs tidiness.
  }

  if (!token) return null;
  const offer = {
    token,
    provider: cleanProvider(provider),
    expiresAt: tokenExpiry(token),
  };
  return storeOffer(offer);
}

function storeOffer(offer) {
  if (offer.expiresAt <= Date.now()) return null;
  ssoLinkOffer.value = offer;
  try {
    sessionStorage.setItem(STORAGE_KEY, JSON.stringify(offer));
  } catch (_) {
    // Storage unavailable: the offer lasts until the next full page load.
  }
  return offer;
}

export function clearSSOLinkOffer() {
  ssoLinkOffer.value = null;
  removeStored();
}

// currentSSOLinkOffer returns the pending offer, dropping it if it expired.
export function currentSSOLinkOffer() {
  const offer = ssoLinkOffer.value;
  if (offer && offer.expiresAt <= Date.now()) {
    clearSSOLinkOffer();
    return null;
  }
  return offer;
}

// ssoLinkHeaders returns the header that carries a pending offer on a
// password, MFA or passkey sign-in. When the org enforces SSO, that sign-in
// cannot open a session, so the server links the identity during it instead
// and answers 403 sso_required with sso_linked true (decision web-presence 3).
export function ssoLinkHeaders() {
  const offer = currentSSOLinkOffer();
  return offer ? { 'X-SSO-Link-Token': offer.token } : {};
}

// ssoSignInPrompt is set when a password, MFA, passkey or refresh step was
// refused because the org enforces SSO. The sign-in page reads it once, opens
// the Sign in with SSO form with the email filled in, and explains why.
// { linked, provider, email } or null.
export const ssoSignInPrompt = signal(null);

// noteSSORequired handles an sso_required refusal. It drops the pending link
// offer either way: a refused sign-in cannot open a session, so the offer is
// either used up (sso_linked true) or no longer usable here (the next SSO
// sign-in makes a new one). Returns true for an sso_required refusal.
export function noteSSORequired(err, email) {
  if (err?.body?.code !== 'sso_required') return false;
  const linked = !!err.body.sso_linked;
  const offer = currentSSOLinkOffer();
  if (offer) clearSSOLinkOffer();
  if (linked) ssoLinksChanged.value++;
  ssoSignInPrompt.value = {
    linked,
    provider: linked && offer ? offer.provider : '',
    email: typeof email === 'string' ? email.trim() : '',
  };
  return true;
}

// ssoRequiredText is the explanation shown above the SSO form after a refusal.
export function ssoRequiredText(prompt) {
  if (prompt?.linked) {
    const name = prompt.provider && prompt.provider !== 'SSO' ? prompt.provider : 'Your SSO identity';
    return `${name} is now connected to your account. Your organization requires SSO, so continue with your work email to finish signing in.`;
  }
  return 'Your organization requires SSO. Password and passkey sign-in are turned off for your account. Continue with your work email to sign in through your identity provider.';
}

// acceptSSOLinkOffer asks the server to link the offered identity to the
// signed-in account. The offer is cleared on success and on any answer that
// retrying cannot fix. Returns the linked identity.
export async function acceptSSOLinkOffer() {
  const offer = currentSSOLinkOffer();
  if (!offer) {
    const err = new Error('expired');
    err.body = { code: 'link_token_invalid' };
    throw err;
  }
  try {
    const res = await post('/auth/oidc/link', { link_token: offer.token });
    clearSSOLinkOffer();
    ssoLinksChanged.value++;
    return res;
  } catch (err) {
    const code = err?.body?.code;
    // A wrong account or SSO session can still be fixed by signing in again
    // in this tab, so keep the offer for those. Everything else is final.
    if (code !== 'link_token_other_user' && code !== 'password_session_required' && !(err?.status >= 500)) {
      clearSSOLinkOffer();
    }
    throw err;
  }
}

// describeSSOLinkError turns a link failure into text a person can act on.
export function describeSSOLinkError(err, provider) {
  const name = provider || 'SSO';
  switch (err?.body?.code) {
    case 'link_token_invalid':
      return `This offer to link ${name} has expired. Sign out, choose Sign in with SSO and enter your work email again to get a new one.`;
    case 'link_token_other_user':
      return `This offer to link ${name} is for a different YipYap account. Sign out, then sign in to the account that uses the same email as your ${name} identity.`;
    case 'password_session_required':
      return `You are signed in with SSO. To link ${name}, sign out and sign in with your password or passkey first.`;
  }
  if (err?.status === 409 || err?.status === 404) {
    return err.message ? capitalize(err.message) + '.' : `Could not link ${name}.`;
  }
  if (err?.readonly) return err.message;
  return `Could not link ${name}. Try again in a moment.`;
}

function capitalize(s) {
  return s.charAt(0).toUpperCase() + s.slice(1);
}
