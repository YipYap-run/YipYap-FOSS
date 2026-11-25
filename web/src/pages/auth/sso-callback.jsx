import { useEffect, useState } from 'preact/hooks';
import { loadUser, isLoggedIn, currentUser } from '../../state/auth';
import { route } from 'preact-router';
import '../../styles/sso-link.css';
import { FormError } from '../../components/ui';
import { consumeSSOReauthReturn } from '../../state/sso-reauth';

const RETRY = 'SSO sign-in did not finish. Try again.';
// Shown when the callback redirect landed here but no session came with it,
// for example a blocked or rejected cookie (ux-10).
export const NO_SESSION = 'We could not complete sign in. Try again or contact your administrator.';
const IDP_ERROR = 'Your identity provider returned an error. Try again, or ask your administrator.';

// The callback sends ?error=<server message>. Only messages listed here are
// shown; anything else (including provider error text, which an attacker
// could put in a crafted link) gets a generic message.
export const SSO_ERROR_MESSAGES = {
  'missing state cookie': 'SSO sign-in did not finish. Start again from the sign-in page in this browser.',
  'invalid or expired sign-in attempt, please try again': 'This sign-in attempt expired. Start again from the sign-in page.',
  'state mismatch': RETRY,
  'missing authorization code': RETRY,
  'connection not found': 'This SSO connection no longer exists. Ask your administrator.',
  'this sign-in connection is disabled': 'Your administrator has turned off this SSO connection. Sign in another way, or ask your administrator.',
  'failed to build oauth config': 'This SSO connection is not set up correctly. Ask your administrator.',
  'token exchange failed': IDP_ERROR,
  'failed to get user info': IDP_ERROR,
  'user info does not match the ID token': IDP_ERROR,
  'no user info endpoint available': 'This SSO connection is not set up correctly. Ask your administrator.',
  'id token nonce mismatch': RETRY,
  'id token verification failed': IDP_ERROR,
  'the identity provider did not return an ID token': IDP_ERROR,
  'the identity provider did not return a subject': IDP_ERROR,
  'the identity provider did not return an email address': 'Your identity provider did not share an email address. Ask your administrator to allow the email scope.',
  'the identity provider has not verified this email address': 'Your identity provider has not verified your email address. Verify it with your provider, then try again.',
  'account not found': 'The YipYap account linked to this identity no longer exists. Ask your administrator.',
  'account does not belong to this organization': 'This identity is linked to an account in a different organization.',
  'this account is disabled': 'Your account is disabled. Ask an admin in your organization if you think this is a mistake.',
  'an account with this email exists in another organization': 'Your email belongs to an account in a different YipYap organization. Sign in with your password instead.',
  'no matching account found and auto-provisioning is disabled': 'No YipYap account matches your identity. Ask your administrator to invite you.',
  'failed to provision user': RETRY,
  'failed to issue link offer': RETRY,
  'failed to issue token': RETRY,
};

// Errors that a fresh attempt can fix. The error card offers a direct way
// back to the work email form for these.
const RETRYABLE = new Set([
  RETRY,
  NO_SESSION,
  IDP_ERROR,
  SSO_ERROR_MESSAGES['missing state cookie'],
  SSO_ERROR_MESSAGES['invalid or expired sign-in attempt, please try again'],
]);

export function isRetryableSSOError(msg) {
  return RETRYABLE.has(msg);
}

export function describeSSOError(raw) {
  if (!raw) return RETRY;
  if (Object.prototype.hasOwnProperty.call(SSO_ERROR_MESSAGES, raw)) return SSO_ERROR_MESSAGES[raw];
  if (raw.startsWith('the identity provider returned an error')) return IDP_ERROR;
  return RETRY;
}

export function SSOCallbackPage() {
  const [error, setError] = useState(null);

  useEffect(() => {
    // Errors come in the query string since fragments are not sent to the server.
    const err = new URLSearchParams(window.location.search).get('error');
    if (err !== null) {
      document.title = 'SSO sign-in failed | YipYap';
      setError(describeSSOError(err));
      return;
    }
    document.title = 'Signing in | YipYap';

    // The backend set an HttpOnly session cookie on the redirect.
    loadUser().then(() => {
      if (isLoggedIn.value && currentUser.value) {
        // A re-auth started from Settings goes back there.
        route(consumeSSOReauthReturn() || '/', true);
      } else {
        document.title = 'SSO sign-in failed | YipYap';
        setError(NO_SESSION);
      }
    }).catch(() => {
      document.title = 'SSO sign-in failed | YipYap';
      setError(NO_SESSION);
    });
  }, []);

  useEffect(() => {
    if (error) {
      const h = document.getElementById('sso-callback-title');
      if (h) h.focus();
    }
  }, [error]);

  if (error) {
    return (
      <div class="auth-page">
        <div class="auth-card">
          <div class="auth-header">
            <h1 id="sso-callback-title" class="auth-title" tabIndex={-1}>SSO sign-in failed</h1>
          </div>
          <FormError>{error}</FormError>
          {isRetryableSSOError(error) ? (
            <div class="sso-callback-actions">
              <a href="/login?sso=1" class="btn btn-primary btn-full">Try SSO again</a>
              <a href="/login" class="btn btn-outline btn-full">Sign in another way</a>
            </div>
          ) : (
            <a href="/login" class="btn btn-primary btn-full">Back to sign in</a>
          )}
        </div>
      </div>
    );
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="auth-title">Completing sign in...</h1>
        </div>
        <div class="loading-screen" role="status" aria-label="Signing you in"><div class="spinner" /></div>
      </div>
    </div>
  );
}
