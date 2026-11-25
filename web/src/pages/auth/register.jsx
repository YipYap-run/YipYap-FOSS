import { useState } from 'preact/hooks';
import { register } from '../../state/auth';
import { route } from 'preact-router';
import { FormError } from '../../components/ui';
import { PasswordHint } from '../../components/password-hint';
import { MIN_PASSWORD_LENGTH, passwordProblem, describePasswordPolicyError } from '../../utils/password-policy';

export function RegisterPage() {
  const [orgName, setOrgName] = useState('');
  const [email, setEmail] = useState('');
  const [password, setPassword] = useState('');
  const [confirmPassword, setConfirmPassword] = useState('');
  const [error, setError] = useState(null);
  // Which field the error is about ('email', 'password', 'confirm'), so the
  // input can point at it with aria-describedby and aria-invalid.
  const [errorField, setErrorField] = useState('');
  // Set when the email already has an account, to offer sign-in and reset.
  const [emailTaken, setEmailTaken] = useState(false);
  const [loading, setLoading] = useState(false);

  function fail(msg, field) {
    setError(msg);
    setErrorField(field || '');
  }

  async function handleSubmit(e) {
    e.preventDefault();
    fail(null);
    setEmailTaken(false);
    const problem = passwordProblem(password);
    if (problem) { fail(problem, 'password'); return; }
    if (password !== confirmPassword) {
      fail('The passwords do not match.', 'confirm');
      return;
    }
    setLoading(true);
    try {
      const res = await register(orgName, email, password, confirmPassword);
      if (res && res.status === 'verification_sent') {
        route('/verify-email-sent?email=' + encodeURIComponent(res.email || email));
        return;
      }
      route('/');
    } catch (err) {
      const code = err?.body?.code;
      if (code === 'email_taken') {
        setEmailTaken(true);
        fail('An account with this email already exists.', 'email');
      } else if (code === 'invalid_email') {
        fail('Enter a valid email address, like name@company.com.', 'email');
      } else if (code === 'password_policy') {
        fail(describePasswordPolicyError(err, password), 'password');
      } else if (err?.status === 429) {
        fail('Too many attempts. Wait a minute and try again.');
      } else {
        fail(err?.message || 'Could not create your account. Try again.');
      }
    } finally {
      setLoading(false);
    }
  }

  const trimmed = email.trim();
  const describe = (field, base) =>
    [base, errorField === field && error ? 'register-error' : ''].filter(Boolean).join(' ') || undefined;

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Create your monitoring account</p>
        </div>
        <form onSubmit={handleSubmit}>
          <FormError id="register-error">
            {error && (emailTaken ? (
              <>
                {error}{' '}
                <a href={'/login?email=' + encodeURIComponent(trimmed)}>Sign in</a>
                {' or '}
                <a href={'/forgot-password?email=' + encodeURIComponent(trimmed)}>reset your password</a>.
              </>
            ) : error)}
          </FormError>
          <div class="form-group">
            <label for="org">Organization Name</label>
            <input id="org" type="text" value={orgName} required autocomplete="organization"
                   onInput={e => setOrgName(e.target.value)} placeholder="Acme Corp" />
          </div>
          <div class="form-group">
            <label for="email">Email</label>
            <input id="email" type="email" value={email} required autocomplete="email"
                   aria-invalid={errorField === 'email' ? 'true' : undefined}
                   aria-describedby={describe('email')}
                   onInput={e => { setEmail(e.target.value); if (errorField === 'email') fail(null); setEmailTaken(false); }}
                   placeholder="you@company.com" />
          </div>
          <div class="form-group">
            <label for="password">Password</label>
            <input id="password" type="password" value={password} required autocomplete="new-password"
                   aria-invalid={errorField === 'password' ? 'true' : undefined}
                   aria-describedby={describe('password', 'password-hint')}
                   onInput={e => setPassword(e.target.value)} minLength={MIN_PASSWORD_LENGTH} />
            <PasswordHint id="password-hint" value={password} />
          </div>
          <div class="form-group">
            <label for="confirm-password">Confirm Password</label>
            <input id="confirm-password" type="password" value={confirmPassword} required autocomplete="new-password"
                   aria-invalid={errorField === 'confirm' ? 'true' : undefined}
                   aria-describedby={describe('confirm')}
                   onInput={e => setConfirmPassword(e.target.value)} minLength={MIN_PASSWORD_LENGTH} />
          </div>
          <button type="submit" class="btn btn-primary btn-full" disabled={loading}>
            {loading ? 'Creating account...' : 'Create Account'}
          </button>
        </form>
        <p class="auth-footer">
          Already have an account? <a href={trimmed ? '/login?email=' + encodeURIComponent(trimmed) : '/login'}>Sign in</a>
        </p>
      </div>
    </div>
  );
}
