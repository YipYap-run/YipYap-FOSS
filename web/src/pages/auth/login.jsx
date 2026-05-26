import { useState, useEffect } from 'preact/hooks';
import { login, mfaState, currentUser, currentOrg, appMeta } from '../../state/auth';
import { setToken, get, api } from '../../api/client';
import { route } from 'preact-router';
import { connectWS } from '../../api/ws';
import { prepareAssertionOptions, serializeAssertion } from '../../utils/webauthn';

export function LoginPage() {
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);
  const [ssoProviders, setSsoProviders] = useState([]);

  useEffect(() => {
    get('/auth/oidc/connections').then(setSsoProviders).catch(() => {});
  }, []);

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const result = await login(email, password);
      if (result.account_disabled) {
        route(`/account/recover?email=${encodeURIComponent(email)}`);
        return;
      }
      if (result.email_not_verified) {
        route(`/verify-email-sent?email=${encodeURIComponent(result.email || email)}`);
        return;
      }
      if (result.mfa_required) {
        mfaState.value = { mfa_token: result.mfa_token, mfa_methods: result.mfa_methods };
        route('/auth/mfa-challenge');
        return;
      }
      route('/');
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  async function passkeyLogin() {
    try {
      const beginRes = await api('/auth/passkey/login/begin', {
        method: 'POST',
        body: JSON.stringify({}),
      });
      const session_id = beginRes.session_id;
      const opts = beginRes.options || beginRes;
      const publicKey = prepareAssertionOptions(opts.publicKey || opts);
      const assertion = await navigator.credentials.get({ publicKey });
      const serialized = serializeAssertion(assertion);

      const res = await api('/auth/passkey/login/finish?session_id=' + encodeURIComponent(session_id), {
        method: 'POST',
        body: JSON.stringify(serialized),
      });

      setToken(res.token);
      currentUser.value = res.user;
      try {
        currentOrg.value = await api('/org');
      } catch (_) {}
      connectWS();
      route('/');
    } catch (err) {
      // The WebAuthn API throws a small set of DOMException names. Map
      // each to a concrete, user-actionable message. The default browser
      // text ("The request is not allowed by the user agent or the
      // platform in the current context, possibly because the user
      // denied permission") is confusing when the real cause is usually
      // 'no passkey registered for this site yet'.
      let msg = err.message || 'Passkey sign-in failed';
      if (err && err.name === 'NotAllowedError') {
        msg = 'No passkey found for this browser or device. Sign in with your email and password, then register a passkey under Settings > Security.';
      } else if (err && err.name === 'SecurityError') {
        msg = 'Passkey sign-in requires a secure (HTTPS) connection.';
      } else if (err && err.name === 'InvalidStateError') {
        msg = 'A passkey for this account already exists on this device but is not usable. Try another device or re-register the passkey.';
      } else if (err && err.name === 'AbortError') {
        msg = 'Passkey sign-in was cancelled.';
      } else if (err && err.name === 'NotSupportedError') {
        msg = 'Your browser or device does not support passkeys. Sign in with your email and password.';
      }
      setError(msg);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Sign in to your monitoring dashboard</p>
        </div>
        <form onSubmit={handleSubmit}>
          {error && <div class="form-error">{error}</div>}
          <div class="form-group">
            <label for="email">Email</label>
            <input id="email" type="email" value={email} required autocomplete="email"
                   onInput={e => setEmail(e.target.value)} placeholder="you@company.com" />
          </div>
          <div class="form-group">
            <label for="password">Password</label>
            <input id="password" type="password" value={password} required autocomplete="current-password"
                   onInput={e => setPassword(e.target.value)} placeholder="Your password" />
          </div>
          <div style="text-align: right; margin-top: -4px; margin-bottom: 8px">
            <a href="/forgot-password" class="text-link-sm">Forgot password?</a>
          </div>
          <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
            {loading ? 'Signing in...' : 'Sign In'}
          </button>
        </form>
        {ssoProviders.length > 0 && (
          <div class="sso-section">
            <div class="divider"><span>or</span></div>
            {ssoProviders.map(p => (
              <a key={p.id} href={`/api/v1/auth/oidc/${p.id}`} class="btn btn-outline btn-full sso-btn">
                Sign in with {p.provider}
              </a>
            ))}
          </div>
        )}
        {appMeta.value?.edition !== 'foss' && (
          <>
            <div class="divider"><span>or</span></div>
            <button type="button" class="btn btn-outline btn-full" onClick={passkeyLogin}>
              Sign in with passkey
            </button>
          </>
        )}
        <p class="auth-footer">
          Don't have an account? <a href="/register">Create one</a>
        </p>
      </div>
    </div>
  );
}
