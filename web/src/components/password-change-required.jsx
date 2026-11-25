import { useEffect, useState } from 'preact/hooks';
import { put, setToken } from '../api/client';
import { currentUser, logout, loadUser } from '../state/auth';
import { setPasswordChangeRequired } from '../state/session';
import { MIN_PASSWORD_LENGTH as MIN_LENGTH, passwordProblem, describePasswordPolicyError } from '../utils/password-policy';
import { loadAccountSecurity, StepUp, describeStepUpError, keyOnly, proofFor } from '../edition/mfa-ui';
import { PasswordHint } from './password-hint';
import '../styles/mfa.css';

function describeError(err) {
  const msg = err?.message || '';
  if (/invalid current password/i.test(msg)) return 'The temporary password is not correct.';
  if (/different from the current/i.test(msg)) return 'Choose a password that is different from the temporary one.';
  return msg || 'Could not change your password. Try again.';
}

// PasswordChangeScreen replaces the app after an admin reset this user's
// password. The server rejects every other call with password_change_required
// until a new password is set, so this is the only thing on offer besides
// signing out.
export function PasswordChangeScreen() {
  const user = currentUser.value;
  const [sec, setSec] = useState(null);
  const [current, setCurrent] = useState('');
  const [next, setNext] = useState('');
  const [confirm, setConfirm] = useState('');
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const [keyPending, setKeyPending] = useState(false);

  useEffect(() => {
    document.title = 'Choose a new password | YipYap';
    const heading = document.getElementById('pw-change-title');
    if (heading) heading.focus();
    loadAccountSecurity().then(setSec);
  }, []);

  async function onSubmit(e, viaKey = false) {
    e?.preventDefault();
    if (!current) { setError('Enter the temporary password you signed in with.'); return; }
    const problem = passwordProblem(next);
    if (problem) { setError(problem); return; }
    if (next !== confirm) { setError('The new passwords do not match.'); return; }
    if (next === current) { setError('Choose a password that is different from the temporary one.'); return; }
    setBusy(true);
    setError('');
    if (viaKey || keyOnly(sec)) setKeyPending(true);
    try {
      const proof = await proofFor(sec, viaKey, code);
      setKeyPending(false);
      const res = await put('/auth/password', {
        current_password: current,
        new_password: next,
        ...proof,
      });
      if (res?.token) setToken(res.token);
      if (res?.user) currentUser.value = res.user;
      setPasswordChangeRequired(false);
      document.title = 'YipYap';
      // Load the org, billing, MFA policy and live updates that were blocked.
      await loadUser();
    } catch (err) {
      if (err?.body?.code === 'mfa_proof_required' && !sec) {
        loadAccountSecurity().then(setSec);
      }
      setError(describePasswordPolicyError(err, next) ||
        describeStepUpError(err, describeError(err)));
      setBusy(false);
    } finally {
      setKeyPending(false);
    }
  }

  async function onSignOut() {
    await logout();
    window.location.href = '/login';
  }

  const errId = error ? 'pw-change-error' : undefined;

  return (
    <div class="auth-page mfa-enroll-page">
      <main class="mfa-enroll-card" aria-labelledby="pw-change-title">
        <header class="mfa-enroll-header">
          <p class="mfa-enroll-brand">YipYap</p>
          <h1 id="pw-change-title" tabIndex={-1}>Choose a new password</h1>
          <p>
            An admin in your organization reset your password. Pick a new one to keep
            using YipYap. Only you will know it.
          </p>
          {user?.email && <p class="mfa-muted mfa-small">Signed in as {user.email}</p>}
        </header>

        <form onSubmit={onSubmit} noValidate>
          {/* Lets password managers save the new password against this account. */}
          {user?.email && (
            <input type="email" name="username" autocomplete="username" value={user.email} hidden readOnly />
          )}
          <div class="form-group">
            <label class="form-label" for="pw-change-current">Temporary password</label>
            <input id="pw-change-current" class="form-input" type="password" autocomplete="current-password"
              value={current} aria-describedby={errId}
              onInput={e => { setCurrent(e.target.value); setError(''); }} />
          </div>
          <div class="form-group">
            <label class="form-label" for="pw-change-new">New password</label>
            <input id="pw-change-new" class="form-input" type="password" autocomplete="new-password"
              minLength={MIN_LENGTH} value={next} aria-describedby={`pw-change-hint${errId ? ' ' + errId : ''}`}
              onInput={e => { setNext(e.target.value); setError(''); }} />
            <PasswordHint id="pw-change-hint" value={next} class="mfa-muted mfa-small" />
          </div>
          <div class="form-group">
            <label class="form-label" for="pw-change-confirm">Confirm new password</label>
            <input id="pw-change-confirm" class="form-input" type="password" autocomplete="new-password"
              value={confirm} aria-describedby={errId}
              onInput={e => { setConfirm(e.target.value); setError(''); }} />
          </div>
          <StepUp sec={sec} idPrefix="pw-change" code={code} setCode={v => { setCode(v); setError(''); }}
            errorId={errId} busy={busy} keyPending={keyPending}
            onKey={e => onSubmit(e, true)} keyLabel="Verify with security key and save" />
          {error && <p id="pw-change-error" class="form-error" role="alert">{error}</p>}
          <div class="btn-group">
            <button type="submit" class="btn btn-primary" disabled={busy || keyPending}>
              {keyPending ? 'Waiting for your key...' : busy ? 'Saving…' : 'Save new password'}
            </button>
          </div>
        </form>

        <footer class="mfa-enroll-footer">
          <p class="mfa-muted mfa-small">
            Did not expect this? Ask an admin in your organization before you continue.
          </p>
          <button type="button" class="btn btn-outline btn-sm" onClick={onSignOut}>Sign out</button>
        </footer>
      </main>
    </div>
  );
}
