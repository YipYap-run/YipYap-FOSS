import { useState } from 'preact/hooks';
import { post } from '../../api/client';

export function ResetPasswordPage() {
  const [password, setPassword] = useState('');
  const [confirm, setConfirm] = useState('');
  const [error, setError] = useState(null);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);

  const params = new URLSearchParams(window.location.search);
  const token = params.get('token');

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    if (password !== confirm) {
      setError('Passwords do not match');
      return;
    }
    if (password.length < 8) {
      setError('Password must be at least 8 characters');
      return;
    }
    setLoading(true);
    try {
      await post('/auth/reset-password', { token, password });
      setDone(true);
    } catch (err) {
      setError(err.message || 'Failed to reset password');
    } finally {
      setLoading(false);
    }
  }

  if (!token) {
    return (
      <div class="auth-page">
        <div class="auth-card">
          <div class="auth-header">
            <h1 class="logo">YipYap</h1>
            <p>Invalid Reset Link</p>
          </div>
          <p style="text-align:center;color:var(--text-secondary)">
            This password reset link is invalid or has expired.
          </p>
          <a href="/login" class="btn btn-primary btn-full" style="margin-top:16px">Back to Sign In</a>
        </div>
      </div>
    );
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Set a new password</p>
        </div>
        {done ? (
          <div>
            <p style="text-align:center;color:var(--text-secondary)">
              Your password has been reset.
            </p>
            <a href="/login" class="btn btn-primary btn-full" style="margin-top:16px">Sign In</a>
          </div>
        ) : (
          <form onSubmit={handleSubmit}>
            {error && <div class="form-error">{error}</div>}
            <div class="form-group">
              <label for="password">New Password</label>
              <input id="password" type="password" value={password} required autocomplete="new-password"
                     onInput={e => setPassword(e.target.value)} placeholder="At least 8 characters" />
            </div>
            <div class="form-group">
              <label for="confirm">Confirm Password</label>
              <input id="confirm" type="password" value={confirm} required autocomplete="new-password"
                     onInput={e => setConfirm(e.target.value)} placeholder="Repeat your password" />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
              {loading ? 'Resetting...' : 'Reset Password'}
            </button>
          </form>
        )}
      </div>
    </div>
  );
}
