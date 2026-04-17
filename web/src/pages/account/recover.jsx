import { useState } from 'preact/hooks';
import { post } from '../../api/client';
import { route } from 'preact-router';

export function AccountRecoverPage() {
  const params = typeof window !== 'undefined' ? new URLSearchParams(window.location.search) : null;
  const [email, setEmail] = useState(params?.get('email') || '');
  const [password, setPassword] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  async function handleSubmit(e) {
    e.preventDefault();
    setLoading(true);
    setError('');
    setSuccess('');
    try {
      await post('/auth/recover-account', { email, password });
      setSuccess('Recovery email sent. Check your inbox for a link to re-enable your account.');
    } catch (err) {
      setError(err.message || 'Failed to request recovery');
    } finally {
      setLoading(false);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Your account is scheduled for deletion</p>
        </div>
        <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 16px">
          Want to keep your account? Enter your credentials below and we'll send you a recovery link.
        </p>
        <form onSubmit={handleSubmit}>
          {error && <div class="form-error">{error}</div>}
          {success && <div class="form-success" style="color: var(--color-up); margin-bottom: 8px">{success}</div>}
          <div class="form-group">
            <label>Email</label>
            <input type="email" value={email} onInput={e => setEmail(e.target.value)} required placeholder="you@company.com" />
          </div>
          <div class="form-group">
            <label>Password</label>
            <input type="password" value={password} onInput={e => setPassword(e.target.value)} required />
          </div>
          <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
            {loading ? 'Sending...' : 'Send Recovery Email'}
          </button>
        </form>
        <p class="auth-footer">
          <a href="/login">Back to login</a>
        </p>
      </div>
    </div>
  );
}
