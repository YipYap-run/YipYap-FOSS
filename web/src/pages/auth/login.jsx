import { useState, useEffect, useRef } from 'preact/hooks';
import { recoverCredentials } from '../../state/recover';
import { login, mfaState, appMeta, authLoading, isLoggedIn } from '../../state/auth';
import {
  captureSSOLinkOffer, currentSSOLinkOffer,
  ssoSignInPrompt, ssoRequiredText,
} from '../../state/sso-link';
import { post } from '../../api/client';
import { route } from 'preact-router';
import { PasskeySignInButton, readStaffSessionNotice } from '../../edition/mfa-ui';
import '../../styles/sso-link.css';
import '../../styles/sso-domains.css';
import { FormError } from '../../components/ui';
import { pendingSSOReauth, clearSSOReauth } from '../../state/sso-reauth';

// The lookup only ever answers with the SSO start route. Anything else is
// not followed, so a tampered response cannot redirect off the site.
const SSO_START = /^\/api\/v1\/auth\/oidc\/[A-Za-z0-9_-]+$/;

function describeSSOLookupError(err) {
  if (err?.status === 429) return 'Too many attempts. Wait a minute and try again.';
  if (err?.status === 400) return 'Enter a valid email address.';
  if (err?.status === 404) {
    return 'SSO is not set up for this email domain. Sign in with your password, or ask your admin whether your organization uses SSO.';
  }
  return 'Could not start SSO sign-in. Try again in a moment.';
}

export function LoginPage() {
  const [password, setPassword] = useState('');
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);
  // Set by the operator session exchange on a refused token, or by the
  // session banner when the session times out.
  const [staffNotice] = useState(readStaffSessionNotice);
  const [altLoginLoading, setAltLoginLoading] = useState(false);
  // 'password' or 'sso'. ?sso=1 opens the work email form directly.
  // A refresh refused by Enforce SSO lands here with reason=sso_required.
  const [ssoRequiredByURL] = useState(() =>
    typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('reason') === 'sso_required');
  // Settings sent an account without a password here to confirm a security
  // change with a fresh SSO sign-in (reason=reauth).
  const [reauth] = useState(() =>
    typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('reason') === 'reauth'
      ? pendingSSOReauth() : null);
  // ?email= comes from the sign-up page when the address already has an
  // account.
  const [email, setEmail] = useState(() => reauth?.email ||
    (typeof window !== 'undefined' && (new URLSearchParams(window.location.search).get('email') || '').slice(0, 254)) || '');
  const [mode, setMode] = useState(() =>
    typeof window !== 'undefined' && (ssoRequiredByURL || reauth ||
      new URLSearchParams(window.location.search).get('sso') === '1') ? 'sso' : 'password');
  // Set after a sign-in was refused because the org enforces SSO:
  // { linked, provider }. Shown above the SSO form.
  const [ssoNotice, setSsoNotice] = useState(() => (ssoRequiredByURL ? { linked: false } : null));
  const [ssoLoading, setSsoLoading] = useState(false);
  const [ssoError, setSsoError] = useState(null);
  const ssoEmailRef = useRef(null);
  const passwordEmailRef = useRef(null);
  const modeChanged = useRef(false);
  // Set when the API client ended a session because the account was
  // disabled mid-session.
  const [signedOutDisabled] = useState(() =>
    typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('reason') === 'disabled');
  // Set when the API client found that this session was revoked (signed out
  // from another device, or ended by a password or role change).
  const [signedOutRemotely] = useState(() =>
    typeof window !== 'undefined' && new URLSearchParams(window.location.search).get('reason') === 'signed_out');

  // An SSO sign-in matched this account by email. Take the one-time offer out
  // of the URL now; the banner asks to link it after the user signs in.
  const [linkOffer, setLinkOffer] = useState(() => captureSSOLinkOffer() || currentSSOLinkOffer());

  // A sign-in attempt was refused because the org enforces SSO (here or on
  // the second-factor page). Open the SSO form with the email filled in and
  // explain why, instead of leaving the error on the password form.
  const prompt = ssoSignInPrompt.value;
  useEffect(() => {
    if (!prompt) return;
    ssoSignInPrompt.value = null;
    setSsoNotice({ linked: prompt.linked, provider: prompt.provider });
    if (prompt.email) setEmail(prompt.email);
    setPassword('');
    setLinkOffer(null);
    modeChanged.current = true;
    setError(null);
    setSsoError(null);
    setMode('sso');
  }, [prompt]);

  // Already signed in (the offer came back to a tab with a session): go
  // straight to the app, where the link banner is shown.
  useEffect(() => {
    if (linkOffer && !authLoading.value && isLoggedIn.value) route('/', true);
  }, [linkOffer, authLoading.value, isLoggedIn.value]);

  // Move focus to the email field of the form that just appeared, so
  // keyboard and screen reader users land where they need to type.
  useEffect(() => {
    if (!modeChanged.current) return;
    const el = mode === 'sso' ? ssoEmailRef.current : passwordEmailRef.current;
    if (el) el.focus();
  }, [mode]);

  // Coming back from the identity provider with the Back button restores
  // this page from the back/forward cache with the button still busy.
  useEffect(() => {
    const onShow = (e) => { if (e.persisted) setSsoLoading(false); };
    window.addEventListener('pageshow', onShow);
    return () => window.removeEventListener('pageshow', onShow);
  }, []);

  function switchMode(next) {
    // Leaving the SSO form drops a pending re-auth return to Settings.
    if (next === 'password') clearSSOReauth();
    modeChanged.current = true;
    setError(null);
    setSsoError(null);
    setSsoNotice(null);
    setMode(next);
  }

  async function handleSSO(e) {
    e.preventDefault();
    if (ssoLoading) return;
    const value = email.trim();
    if (!value || !value.includes('@')) {
      setSsoError('Enter your work email, for example you@company.com.');
      if (ssoEmailRef.current) ssoEmailRef.current.focus();
      return;
    }
    setSsoError(null);
    setSsoLoading(true);
    try {
      const res = await post('/auth/sso/lookup', { email: value });
      if (!res || typeof res.url !== 'string' || !SSO_START.test(res.url)) {
        throw new Error('unexpected SSO lookup response');
      }
      // Full navigation: the server redirects to the identity provider.
      // Keep the button busy until the page unloads.
      window.location.assign(res.url);
      return;
    } catch (err) {
      setSsoError(describeSSOLookupError(err));
      if (ssoEmailRef.current) ssoEmailRef.current.focus();
    }
    setSsoLoading(false);
  }

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      const result = await login(email, password);
      if (result.account_disabled) {
        // The server checked the password before answering account_disabled,
        // so hand it to the restore page in memory instead of asking again.
        recoverCredentials.value = { email, password };
        route(`/account/recover?email=${encodeURIComponent(email)}`);
        return;
      }
      if (result.email_not_verified) {
        route(`/verify-email-sent?email=${encodeURIComponent(result.email || email)}`);
        return;
      }
      if (result.mfa_required) {
        mfaState.value = { mfa_token: result.mfa_token, mfa_methods: result.mfa_methods, email };
        route('/auth/mfa-challenge');
        return;
      }
      route('/');
    } catch (err) {
      // sso_required is handled by the prompt effect above.
      if (err?.body?.code !== 'sso_required') setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  // SSO is a SaaS feature; the FOSS build has no lookup route.
  const showSSO = appMeta.value?.edition !== 'foss';

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Sign in to your monitoring dashboard</p>
        </div>
        {signedOutRemotely && !linkOffer && !ssoNotice && (
          <div class="sso-link-notice" role="status">
            <p>
              Your session ended. It was signed out from another device, your password or role
              changed, or your organization started requiring SSO. Sign in again to continue.
            </p>
          </div>
        )}
        {linkOffer && (
          <div class="sso-link-notice" role="status">
            <p>
              <strong>An account with this email already exists.</strong>
            </p>
            <p>
              Sign in with your password to confirm it is you, then we will connect
              {' '}{linkOffer.provider} for you. Nothing is linked until you confirm.
            </p>
          </div>
        )}
        {mode === 'sso' && showSSO ? (
          <form onSubmit={handleSSO} noValidate aria-labelledby="sso-signin-heading">
            <h2 id="sso-signin-heading" class="sso-signin-heading">Sign in with SSO</h2>
            {reauth && !ssoNotice && (
              <div class="sso-link-notice" role="status" id="sso-reauth-notice">
                <p>
                  Sign in with SSO again to confirm it is you. You will return to
                  Settings, Security, and have 10 minutes to add or remove a sign-in method.
                </p>
              </div>
            )}
            {ssoNotice && (
              <div class="sso-link-notice sso-required-notice" role="status" id="sso-required-notice">
                <p>{ssoRequiredText(ssoNotice)}</p>
              </div>
            )}
            <p class="sso-signin-intro" id="sso-signin-help">
              Enter your work email. We will send you to your organization's identity provider.
            </p>
            <FormError id="sso-email-error">{ssoError}</FormError>
            <div class="form-group">
              <label for="sso-email">Work email</label>
              <input id="sso-email" ref={ssoEmailRef} type="email" value={email} autocomplete="email"
                     inputMode="email" autocapitalize="none" spellcheck={false} required
                     aria-invalid={ssoError ? 'true' : 'false'}
                     aria-describedby={[ssoError && 'sso-email-error', ssoNotice && 'sso-required-notice', reauth && !ssoNotice && 'sso-reauth-notice', 'sso-signin-help'].filter(Boolean).join(' ')}
                     onInput={e => { setEmail(e.target.value); if (ssoError) setSsoError(null); }}
                     placeholder="you@company.com" />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={ssoLoading}
                    aria-busy={ssoLoading ? 'true' : 'false'}>
              {ssoLoading ? 'Finding your sign-in...' : 'Continue'}
            </button>
            <p class="sso-signin-back">
              <button type="button" class="sso-link-button" onClick={() => switchMode('password')}
                      disabled={ssoLoading}>
                Sign in with a password instead
              </button>
            </p>
          </form>
        ) : (
          <>
            <form onSubmit={handleSubmit}>
              {staffNotice && <div class="form-error" role="status">{staffNotice}</div>}
              {signedOutDisabled && !error && (
                <div class="form-error" role="status">
                  You were signed out because your account is scheduled for deletion. To keep
                  it, sign in within 4 days of confirming the deletion and we will email you a
                  link to restore it.
                </div>
              )}
              <FormError id="login-error">{error}</FormError>
              <div class="form-group">
                <label for="email">Email</label>
                <input id="email" ref={passwordEmailRef} type="email" value={email} required autocomplete="email"
                       onInput={e => setEmail(e.target.value)} placeholder="you@company.com" />
              </div>
              <div class="form-group">
                <label for="password">Password</label>
                <input id="password" type="password" value={password} required autocomplete="current-password"
                       onInput={e => setPassword(e.target.value)} placeholder="Your password" />
              </div>
              <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
                {loading ? 'Signing in...' : 'Sign In'}
              </button>
            </form>
            {showSSO && (
              <div class="alt-logins">
                <div class="divider"><span>or</span></div>
                <button type="button" class="btn btn-outline btn-full" onClick={() => switchMode('sso')}
                        disabled={loading || altLoginLoading}>
                  Sign in with SSO
                </button>
                <PasskeySignInButton email={email} disabled={loading}
                                     onLoadingChange={setAltLoginLoading} onError={setError} />
              </div>
            )}
          </>
        )}
        <p class="auth-footer">
          Don't have an account? <a href="/register">Create one</a>
        </p>
        <p class="auth-footer auth-footer-secondary">
          <a href="/forgot-password">Forgot password?</a>
        </p>
      </div>
    </div>
  );
}
