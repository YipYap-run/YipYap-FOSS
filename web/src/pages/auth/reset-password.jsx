import { useState, useEffect, useRef } from 'preact/hooks';
import { post } from '../../api/client';
import { FormError } from '../../components/ui';
import { MIN_PASSWORD_LENGTH, passwordProblem, describePasswordPolicyError } from '../../utils/password-policy';
import { PasswordHint } from '../../components/password-hint';

export function ResetPasswordPage() {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  // error is { field, message }; field is 'password', 'confirm' or '' for a
  // server error that is not about one field.
  const [error, setError] = useState(null);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  // The org turned on Enforce SSO after this link was sent.
  const [ssoRequired, setSsoRequired] = useState(false);
  const headingRef = useRef(null);

  const params = new URLSearchParams(window.location.search);
  const token = params.get('token');

  // The form is replaced by the result; move focus to it so keyboard and
  // screen reader users land on the outcome.
  useEffect(() => { if (done) headingRef.current?.focus(); }, [done]);

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    const problem = passwordProblem(password);
    if (problem) {
      setError(problem);
      return;
    }
    if (password !== confirm) {
      setError('The passwords do not match.');
      return;
    }
    setLoading(true);
    try {
      await post('/auth/reset-password', { token, password });
      setDone(true);
    } catch (err) {
      if (err?.body?.code === 'sso_required') {
        setSsoRequired(true);
        return;
      }
      setError(describePasswordPolicyError(err, password) ||
        (err?.status === 429 ? 'Too many attempts. Wait a minute and try again.' : '') ||
        err.message || 'Could not reset your password. Request a new link and try again.');
    } finally {
      setLoading(false);
    }
  }

  if (!token) {
    return (
      <div class="auth-page">
        <div class="auth-card">
          <div class="auth-header">
            <div class="logo" aria-hidden="true">YipYap</div>
            <h1 class="accept-invite-title">Invalid reset link</h1>
          </div>
          <p style="text-align:center;color:var(--color-text-secondary)">
            This password reset link is invalid or has expired.
          </p>
          <a href="/forgot-password" class="btn btn-primary btn-full" style="margin-top:16px">Request a new link</a>
          <p class="auth-footer"><a href="/login">Back to sign in</a></p>
        </div>
      </div>
    );
  }

  const fieldError = field => (error && (error.field === field || (!error.field && field === 'password')) ? error.message : '');
  const describe = (field, help) => [help, fieldError(field) ? `reset-${field}-error` : ''].filter(Boolean).join(' ') || undefined;

  return (
    <div class="auth-page">
      <main class="auth-card">
        <div class="auth-header">
          <div class="logo" aria-hidden="true">YipYap</div>
          <h1 ref={headingRef} tabIndex={-1} class="accept-invite-title">{done ? 'Password reset' : 'Set a new password'}</h1>
        </div>
        {ssoRequired ? (
          <div role="status">
            <p style="text-align:center;color:var(--color-text-secondary)">
              Your organization now requires SSO, so your account has no password to reset.
              Sign in with SSO using your work email.
            </p>
            <a href="/login?sso=1" class="btn btn-primary btn-full" style="margin-top:16px">Sign in with SSO</a>
          </div>
        ) : done ? (
          <div>
            <p style="text-align:center;color:var(--color-text-secondary)">
              Your password has been reset. Sign in with the new password.
            </p>
            <a href="/login" class="btn btn-primary btn-full" style="margin-top:16px">Sign in</a>
          </div>
        ) : (
          <form onSubmit={handleSubmit}>
            <FormError id="reset-error">{error}</FormError>
            <div class="form-group">
              <label for="password">New Password</label>
              <input id="password" type="password" value={password} required autocomplete="new-password"
                     aria-describedby={'reset-password-hint' + (error ? ' reset-error' : '')} minLength={MIN_PASSWORD_LENGTH}
                     onInput={e => setPassword(e.target.value)} />
              <PasswordHint id="reset-password-hint" value={password} />
            </div>
            <div class="form-group">
              <label for="confirm">Confirm Password</label>
              <input id="confirm" type="password" value={confirm} required autocomplete="new-password"
                     minLength={MIN_PASSWORD_LENGTH}
                     onInput={e => setConfirm(e.target.value)} placeholder="Repeat your password" />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={loading} aria-busy={loading ? 'true' : undefined}>
              {loading ? 'Resetting...' : 'Reset password'}
            </button>
          </form>
        )}
      </main>
    </div>
  );
}
