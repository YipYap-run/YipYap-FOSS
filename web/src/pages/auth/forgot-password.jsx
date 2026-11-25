import { useState, useEffect, useRef } from 'preact/hooks';
import { post } from '../../api/client';
import { FormError } from '../../components/ui';
import { appMeta } from '../../state/auth';

export function ForgotPasswordPage() {
  // Settings links here with ?email= for an SSO account adding a password.
  const [email, setEmail] = useState(() => {
    if (typeof window === 'undefined') return '';
    const v = new URLSearchParams(window.location.search).get('email') || '';
    return v.length <= 254 ? v : '';
  });
  const [sent, setSent] = useState(false);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);
  const headingRef = useRef(null);

  // The form is replaced by the confirmation; move focus to it.
  useEffect(() => { if (sent) headingRef.current?.focus(); }, [sent]);

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await post('/auth/forgot-password', { email });
      setSent(true);
    } catch (err) {
      setError(err.message || 'Could not send the reset email. Try again.');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <div class="logo" aria-hidden="true">YipYap</div>
          <h1 ref={headingRef} tabIndex={-1} class="accept-invite-title">{sent ? 'Check your email' : 'Reset your password'}</h1>
        </div>
        {sent ? (
          <div role="status">
            <p style="text-align:center;color:var(--color-text-secondary)">
              If an account exists for <strong>{email}</strong>, we sent a password reset link. Check your inbox.
            </p>
            {appMeta.value?.edition !== 'foss' && (
              <p style="text-align:center;color:var(--color-text-secondary)">
                If your organization requires SSO, no reset email is sent.{' '}
                <a href="/login?sso=1">Sign in with SSO</a> instead.
              </p>
            )}
            <a href="/login" class="btn btn-primary btn-full" style="margin-top:16px">Back to Sign In</a>
          </div>
        ) : (
          <form onSubmit={handleSubmit}>
            <FormError id="forgot-error">{error}</FormError>
            <div class="form-group">
              <label for="email">Email address</label>
              <input id="email" type="email" value={email} required autocomplete="email"
                     aria-invalid={error ? 'true' : undefined} aria-describedby={error ? 'forgot-error' : undefined}
                     onInput={e => setEmail(e.target.value)} placeholder="you@company.com" />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={loading} aria-busy={loading ? 'true' : undefined}>
              {loading ? 'Sending...' : 'Send reset link'}
            </button>
          </form>
        )}
        {!sent && (
          <p class="auth-footer">
            Remember your password? <a href="/login">Sign in</a>
          </p>
        )}
      </div>
    </div>
  );
}
