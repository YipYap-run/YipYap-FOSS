import { useState } from 'preact/hooks';
import { register } from '../../state/auth';
import { route } from 'preact-router';

export function RegisterPage() {
  const [orgName, setOrgName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState(null);
  const [loading, setLoading] = useState(false);

  async function handleSubmit(e) {
    e.preventDefault();
    setError(null);
    setLoading(true);
    try {
      await register(orgName, email, password);
      route('/');
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Create your monitoring account</p>
        </div>
        <form onSubmit={handleSubmit}>
          {error && <div class="form-error">{error}</div>}
          <div class="form-group">
            <label for="org">Organization Name</label>
            <input id="org" type="text" value={orgName} required
                   onInput={e => setOrgName(e.target.value)} placeholder="Acme Corp" />
          </div>
          <div class="form-group">
            <label for="email">Email</label>
            <input id="email" type="email" value={email} required autocomplete="email"
                   onInput={e => setEmail(e.target.value)} placeholder="you@company.com" />
          </div>
          <div class="form-group">
            <label for="password">Password</label>
            <input id="password" type="password" value={password} required autocomplete="new-password"
                   onInput={e => setPassword(e.target.value)} placeholder="At least 8 characters" minLength={8} />
          </div>
          <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
            {loading ? 'Creating account...' : 'Create Account'}
          </button>
        </form>
        <p class="auth-footer">
          Already have an account? <a href="/login">Sign in</a>
        </p>
      </div>
    </div>
  );
}
