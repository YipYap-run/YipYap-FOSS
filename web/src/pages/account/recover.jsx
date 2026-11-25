import { useState, useRef, useEffect } from 'preact/hooks';
import { post } from '../../api/client';
import { FormError, FormSuccess } from '../../components/ui';
import { takeRecoverCredentials } from '../../state/recover';

// describeRecoverError turns a POST /auth/recover-account failure into a
// sentence. The endpoint answers 404 when the server has no email set up,
// and 401 for a wrong password, an unknown email or an account that is not
// scheduled for deletion (one answer for all three, on purpose).
export function describeRecoverError(err) {
  if (err?.status === 404) {
    return 'This server cannot send email, so the restore link cannot be sent. Contact your administrator before the 4 days are up.';
  }
  if (err?.status === 401) {
    return 'We could not match that email and password to an account scheduled for deletion. Check both and try again.';
  }
  if (err?.status === 429) return 'Too many attempts. Wait a minute and try again.';
  return err?.message || 'Could not send the restore link. Try again.';
}

export function AccountRecoverPage() {
  const params = typeof window !== 'undefined' ? new URLSearchParams(window.location.search) : null;
  const [email, setEmail] = useState(params?.get('email') || '');
  // Arriving from a sign-in that just checked the password: keep it in
  // component memory and skip the password field.
  const [verified] = useState(() => takeRecoverCredentials(params?.get('email') || ''));
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [sent, setSent] = useState(false);
  const statusRef = useRef(null);

  useEffect(() => {
    if (sent && statusRef.current) statusRef.current.focus();
  }, [sent]);

  async function handleSubmit(e) {
    e.preventDefault();
    setLoading(true);
    setError('');
    try {
      await post('/auth/recover-account', verified
        ? { email: verified.email, password: verified.password }
        : { email, password });
      setPassword('');
      setSent(true);
    } catch (err) {
      setError(describeRecoverError(err));
    } finally {
      setLoading(false);
    }
  }

  const target = verified ? verified.email : email;

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Your account is scheduled for deletion</p>
        </div>
        {sent ? (
          <div ref={statusRef} tabIndex={-1} style="outline: none">
            <FormSuccess>
              We sent a restore link to <strong style="overflow-wrap: anywhere">{target}</strong>.
            </FormSuccess>
            <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin: 12px 0 16px">
              The link works for 1 hour. Open it to keep your account. If it does not arrive in a
              few minutes, check your spam folder or send it again.
            </p>
            <button type="button" class="btn btn-outline btn-full" onClick={() => { setSent(false); setError(''); }}>
              Send it again
            </button>
          </div>
        ) : (
          <>
            <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 16px">
              Your account is disabled and will be deleted 4 days after you confirmed the deletion.
              To keep it, we will email you a link to restore it.
            </p>
            {!verified && (
              <p id="recover-why-password" style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 16px">
                We ask for your password again so we know the request comes from you. Your
                account is signed out while it is disabled, so we cannot check that any other way.
              </p>
            )}
            <form onSubmit={handleSubmit}>
              <FormError id="recover-error">{error}</FormError>
              {verified ? (
                <p style="font-size: 0.875rem; margin: 0 0 16px; overflow-wrap: anywhere">
                  We will send the link to <strong>{verified.email}</strong>.
                </p>
              ) : (
                <>
                  <div class="form-group">
                    <label for="recover-email">Email</label>
                    <input id="recover-email" type="email" autoComplete="email" value={email}
                      onInput={e => setEmail(e.target.value)} required placeholder="you@company.com"
                      aria-describedby={error ? 'recover-error' : undefined} />
                  </div>
                  <div class="form-group">
                    <label for="recover-password">Password</label>
                    <input id="recover-password" type="password" autoComplete="current-password"
                      aria-describedby={error ? 'recover-why-password recover-error' : 'recover-why-password'}
                      value={password} onInput={e => setPassword(e.target.value)} required />
                  </div>
                </>
              )}
              <button type="submit" class="btn btn-primary btn-full" disabled={loading} aria-busy={loading ? 'true' : 'false'}>
                {loading ? 'Sending...' : 'Email me a restore link'}
              </button>
            </form>
          </>
        )}
        <p class="auth-footer">
          <a href="/login">Back to sign in</a>
        </p>
      </div>
    </div>
  );
}
