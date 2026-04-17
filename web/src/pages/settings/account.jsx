import { useState } from 'preact/hooks';
import { put, setToken } from '../../api/client';
import { currentUser } from '../../state/auth';
import { theme, setOLED, isDark } from '../../state/theme';

export function AccountTab() {
  // Password change state
  const [currentPw, setCurrentPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [confirmPw, setConfirmPw] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [pwLoading, setPwLoading] = useState(false);
  const [pwError, setPwError] = useState('');
  const [pwSuccess, setPwSuccess] = useState('');

  // Email change state
  const [newEmail, setNewEmail] = useState('');
  const [emailPw, setEmailPw] = useState('');
  const [emailMfa, setEmailMfa] = useState('');
  const [emailLoading, setEmailLoading] = useState(false);
  const [emailError, setEmailError] = useState('');
  const [emailSuccess, setEmailSuccess] = useState('');

  async function handlePasswordChange(e) {
    e.preventDefault();
    if (newPw !== confirmPw) { setPwError('Passwords do not match'); return; }
    setPwLoading(true); setPwError(''); setPwSuccess('');
    try {
      const res = await put('/auth/password', {
        current_password: currentPw,
        new_password: newPw,
        mfa_code: mfaCode || undefined,
      });
      setToken(res.token);
      currentUser.value = res.user;
      setPwSuccess('Password updated');
      setCurrentPw(''); setNewPw(''); setConfirmPw(''); setMfaCode('');
    } catch (err) {
      setPwError(err.message || 'Failed to update password');
    } finally { setPwLoading(false); }
  }

  async function handleEmailChange(e) {
    e.preventDefault();
    setEmailLoading(true); setEmailError(''); setEmailSuccess('');
    try {
      const res = await put('/auth/email', {
        current_password: emailPw,
        new_email: newEmail,
        mfa_code: emailMfa || undefined,
      });
      setToken(res.token);
      currentUser.value = res.user;
      setEmailSuccess('Email updated');
      setNewEmail(''); setEmailPw(''); setEmailMfa('');
    } catch (err) {
      setEmailError(err.message || 'Failed to update email');
    } finally { setEmailLoading(false); }
  }

  return (
    <>
      <div class="card">
        <div class="section-header"><h3>Change Password</h3></div>
        <form onSubmit={handlePasswordChange}>
          <div class="form-group">
            <label>Current Password</label>
            <input type="password" value={currentPw} onInput={e => setCurrentPw(e.target.value)} required />
          </div>
          <div class="form-group">
            <label>New Password</label>
            <input type="password" value={newPw} onInput={e => setNewPw(e.target.value)} required />
          </div>
          <div class="form-group">
            <label>Confirm New Password</label>
            <input type="password" value={confirmPw} onInput={e => setConfirmPw(e.target.value)} required />
          </div>
          {currentUser.value?.mfa_app_enabled && (
            <div class="form-group">
              <label>MFA Code</label>
              <input type="text" inputMode="numeric" maxLength="6" value={mfaCode} onInput={e => setMfaCode(e.target.value)} placeholder="6-digit code or backup code" />
            </div>
          )}
          {pwError && <div class="form-error">{pwError}</div>}
          {pwSuccess && <div class="form-success" style="color: var(--color-up); margin-bottom: 8px">{pwSuccess}</div>}
          <button type="submit" class="btn btn-primary" disabled={pwLoading}>
            {pwLoading ? 'Updating...' : 'Update Password'}
          </button>
        </form>
      </div>

      <div class="card" style="margin-top: 16px">
        <div class="section-header"><h3>Change Email</h3></div>
        <form onSubmit={handleEmailChange}>
          <div class="form-group">
            <label>Current Email</label>
            <input type="email" value={currentUser.value?.email} disabled />
          </div>
          <div class="form-group">
            <label>New Email</label>
            <input type="email" value={newEmail} onInput={e => setNewEmail(e.target.value)} required />
          </div>
          <div class="form-group">
            <label>Current Password</label>
            <input type="password" value={emailPw} onInput={e => setEmailPw(e.target.value)} required />
          </div>
          {currentUser.value?.mfa_app_enabled && (
            <div class="form-group">
              <label>MFA Code</label>
              <input type="text" inputMode="numeric" maxLength="6" value={emailMfa} onInput={e => setEmailMfa(e.target.value)} />
            </div>
          )}
          {emailError && <div class="form-error">{emailError}</div>}
          {emailSuccess && <div class="form-success" style="color: var(--color-up); margin-bottom: 8px">{emailSuccess}</div>}
          <button type="submit" class="btn btn-primary" disabled={emailLoading}>
            {emailLoading ? 'Updating...' : 'Update Email'}
          </button>
        </form>
      </div>

      {isDark() && (
        <div class="card" style="margin-top: 16px">
          <div class="section-header"><h3>Display</h3></div>
          <div class="form-group" style="display: flex; align-items: center; justify-content: space-between">
            <div>
              <label style="margin-bottom: 2px">OLED Dark Mode</label>
              <p style="font-size: 0.8125rem; color: var(--color-text-muted); margin: 0">
                Pure black backgrounds for OLED screens. Reduces battery usage on mobile.
              </p>
            </div>
            <label class="toggle-switch">
              <input
                type="checkbox"
                checked={theme.value === 'oled'}
                onChange={e => setOLED(e.target.checked)}
              />
              <span class="toggle-slider" />
            </label>
          </div>
        </div>
      )}
    </>
  );
}
