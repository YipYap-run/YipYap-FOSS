import { useEffect, useState } from 'preact/hooks';
import { setToken } from '../../api/client';
import { loadUser } from '../../state/auth';
import { route } from 'preact-router';

export function SSOCallbackPage() {
  const [error, setError] = useState(null);

  const ERROR_MESSAGES = {
    'missing state cookie': 'SSO login failed. Please try again.',
    'invalid state cookie': 'SSO login failed. Please try again.',
    'state mismatch': 'SSO login failed. Please try again.',
    'missing authorization code': 'SSO login failed. Please try again.',
    'connection not found': 'SSO login failed. Please try again.',
    'failed to build oauth config': 'SSO login failed. Please try again.',
    'token exchange failed': 'The identity provider returned an error.',
    'failed to get user info': 'The identity provider returned an error.',
    'failed to issue token': 'SSO login failed. Please try again.',
    'no matching account found and auto-provisioning is disabled': 'No account found for your identity. Contact your administrator.',
  };

  useEffect(() => {
    // Errors come in query string (?error=...) since fragments aren't sent to server.
    const queryParams = new URLSearchParams(window.location.search);
    const err = queryParams.get('error');

    if (err) {
      setError(ERROR_MESSAGES[err] || 'SSO login failed. Please try again.');
      return;
    }

    // The backend sets an HttpOnly session cookie on the redirect, which is
    // the authoritative session. The token may also appear in the URL fragment
    // (#token=...) for the in-memory Authorization header fallback - read it
    // if present, but it is not required.
    const fragment = new URLSearchParams(window.location.hash.slice(1));
    const token = fragment.get('token');
    if (token) {
      setToken(token);
    }

    // loadUser will use the cookie (via credentials: 'same-origin') to verify
    // the session, so this succeeds even if the fragment token is absent.
    loadUser().then(() => {
      route('/');
    });
  }, []);

  if (error) {
    return (
      <div class="auth-page">
        <div class="auth-card">
          <div class="auth-header">
            <h1 class="logo">YipYap</h1>
            <p>SSO Login Failed</p>
          </div>
          <div class="form-error">{error}</div>
          <a href="/login" class="btn btn-primary btn-full">Back to Login</a>
        </div>
      </div>
    );
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Completing sign in...</p>
        </div>
        <div class="loading-screen"><div class="spinner" /></div>
      </div>
    </div>
  );
}
