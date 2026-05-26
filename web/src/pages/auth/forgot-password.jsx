import { useState } from 'preact/hooks';
import { post } from '../../api/client';

export function ForgotPasswordPage() {
  const [email, setEmail] = useState('');
  const [sent, setSent] = useState(false);
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await post('/auth/forgot-password', { email });
      setSent(true);
    } catch (err) {
      setError(err.message || 'Failed to send reset email');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Reset your password</p>
        </div>
        {sent ? (
          <div>
            <p style="text-align:center;color:var(--color-text-secondary)">
              If an account exists for <strong>{email}</strong>, we've sent a password reset link. Check your inbox.
            </p>
            <a href="/login" class="btn btn-primary btn-full" style="margin-top:16px">Back to Sign In</a>
          </div>
        ) : (
          <form onSubmit={handleSubmit}>
            {error && <div class="form-error">{error}</div>}
            <div class="form-group">
              <label for="email">Email address</label>
              <input id="email" type="email" value={email} required autocomplete="email"
                     onInput={e => setEmail(e.target.value)} placeholder="you@company.com" />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
              {loading ? 'Sending...' : 'Send Reset Link'}
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
