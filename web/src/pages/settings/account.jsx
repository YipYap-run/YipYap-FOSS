import { useState, useEffect } from 'preact/hooks';
import { put, post, del, setToken } from '../../api/client';
import { currentUser, currentOrg, appMeta, resendVerification } from '../../state/auth';
import { emailChangeError, emailChangeSuccess, needsEmailVerification, resendOutcome, RESEND_COOLDOWN_SECS } from './email-change.mjs';
import { theme, setOLED, isDark } from '../../state/theme';
import { FormError, FormSuccess } from '../../components/ui';
import { loadAccountSecurity, StepUp, describeStepUpError as describeAccountError, keyOnly, proofFor } from '../../edition/mfa-ui';
import { MIN_PASSWORD_LENGTH, passwordProblem, describePasswordPolicyError } from '../../utils/password-policy';
import { describeEmailChangeError } from '../../utils/email-change';
import { PasswordHint } from '../../components/password-hint';
import { PendingEmailNotice } from '../../components/pending-email-notice';

// describeDeleteError maps POST /auth/delete-account failures. 404 means
// the server has no email set up, so the confirmation link cannot be sent.
function describeDeleteError(err) {
  if (err?.status === 404) {
    return 'Account deletion needs email, and this server cannot send it. Contact your administrator.';
  }
  if (err?.status === 409) {
    return 'You own this organization and it still has other members. Transfer ownership or remove the other members first.';
  }
  if (err?.status === 400 && /confirm_phrase/.test(err.message || '')) {
    return 'Type the confirmation sentence exactly as shown.';
  }
  return describeAccountError(err, 'Could not delete your account. Try again.');
}

// NoPasswordCard replaces the password form for an account that signs in
// with SSO only. A password can be added through the reset email, unless
// the organization enforces SSO.
function NoPasswordCard({ ssoEnforced }) {
  return (
    <div class="card">
      <div class="section-header"><h3>Password</h3></div>
      <p class="form-help" style="margin: 0 0 12px">
        Your account signs in with SSO and has no password.
        {ssoEnforced
          ? ' Your organization requires SSO, so password sign-in is turned off.'
          : ' You can add one if you also want to sign in with your email and a password. We email you a link to set it.'}
      </p>
      {!ssoEnforced && (
        <a href={'/forgot-password?email=' + encodeURIComponent(currentUser.value?.email || '')} class="btn btn-outline">
          Email me a link to set a password
        </a>
      )}
    </div>
  );
}

// NeedsPasswordNote explains why a password-confirmed change is not
// available to an account without a password.
function NeedsPasswordNote({ ssoEnforced, action }) {
  return (
    <p class="form-help" style="margin: 0 0 12px">
      {ssoEnforced
        ? `To ${action}, ask an admin in your organization. Your account signs in with SSO and has no password to confirm the change with.`
        : `To ${action}, add a password first (see Password above). Your account signs in with SSO and the change is confirmed with your password.`}
    </p>
  );
}

export function AccountTab() {
  // Step-up options for this account (code, key or both).
  const [sec, setSec] = useState(null);
  useEffect(() => { loadAccountSecurity().then(setSec); }, []);
  // A backup code is used up on success, so the count is read again.
  const reloadSecurity = () => loadAccountSecurity().then(setSec);
  // Which form is waiting on the browser key prompt ('pw', 'email', 'delete').
  const [keyPending, setKeyPending] = useState('');
  // SSO-only accounts have no password to confirm these changes with.
  const noPassword = !!sec && !sec.has_password;
  const ssoEnforced = !!currentOrg.value?.sso_enforced;

  // Password change state
  const [currentPw, setCurrentPw] = useState('');
  const [newPw, setNewPw] = useState('');
  const [confirmPw, setConfirmPw] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  const [pwLoading, setPwLoading] = useState(false);
  const [pwError, setPwError] = useState('');
  const [pwSuccess, setPwSuccess] = useState('');

  // Delete account state
  const [delPw, setDelPw] = useState('');
  const [delMfa, setDelMfa] = useState('');
  const [delPhrase, setDelPhrase] = useState('');
  const [delLoading, setDelLoading] = useState(false);
  const [delError, setDelError] = useState('');
  const [delSuccess, setDelSuccess] = useState('');

  // Email change state
  const [newEmail, setNewEmail] = useState('');
  const [emailPw, setEmailPw] = useState('');
  const [emailMfa, setEmailMfa] = useState('');
  const [emailLoading, setEmailLoading] = useState(false);
  const [emailError, setEmailError] = useState('');
  const [emailSuccess, setEmailSuccess] = useState('');

  // Verification resend state (shown while the address is unverified).
  const [resendSending, setResendSending] = useState(false);
  const [resendCooldown, setResendCooldown] = useState(0);
  const [resendMsg, setResendMsg] = useState(null); // {kind, message}

  useEffect(() => {
    if (resendCooldown <= 0) return undefined;
    const t = setTimeout(() => setResendCooldown(c => c - 1), 1000);
    return () => clearTimeout(t);
  }, [resendCooldown]);

  async function handleResendVerification() {
    const email = currentUser.value?.email;
    if (!email) return;
    setResendSending(true); setResendMsg(null);
    let out;
    try {
      out = resendOutcome(await resendVerification(email), null);
    } catch (err) {
      out = resendOutcome(null, err);
    }
    setResendMsg(out);
    setResendCooldown(out.cooldown);
    if (out.kind === 'verified' && currentUser.value) {
      currentUser.value = { ...currentUser.value, email_verified_at: new Date().toISOString() };
    }
    setResendSending(false);
  }

  async function handlePasswordChange(e, viaKey = false) {
    e?.preventDefault();
    if (viaKey && !e?.target?.form?.reportValidity?.()) return;
    const problem = passwordProblem(newPw);
    if (problem) { setPwError(problem); return; }
    if (newPw !== confirmPw) { setPwError('The new passwords do not match.'); return; }
    setPwLoading(true); setPwError(''); setPwSuccess('');
    if (viaKey || keyOnly(sec)) setKeyPending('pw');
    try {
      const proof = await proofFor(sec, viaKey, mfaCode);
      setKeyPending('');
      const res = await put('/auth/password', {
        current_password: currentPw,
        new_password: newPw,
        ...proof,
      });
      setToken(res.token);
      currentUser.value = res.user;
      setPwSuccess('Password updated. Your other sessions were signed out.');
      setCurrentPw(''); setNewPw(''); setConfirmPw(''); setMfaCode('');
      reloadSecurity();
    } catch (err) {
      setPwError(describePasswordPolicyError(err, newPw) ||
        describeAccountError(err, 'Could not update your password. Try again.'));
    } finally { setPwLoading(false); setKeyPending(''); }
  }

  async function handleEmailChange(e, viaKey = false) {
    e?.preventDefault();
    if (viaKey && !e?.target?.form?.reportValidity?.()) return;
    const next = newEmail.trim();
    const current = currentUser.value?.email || '';
    if (next.toLowerCase() === current.toLowerCase()) {
      setEmailError('That is already your email address.');
      return;
    }
    setEmailLoading(true); setEmailError(''); setEmailSuccess('');
    if (viaKey || keyOnly(sec)) setKeyPending('email');
    try {
      const proof = await proofFor(sec, viaKey, emailMfa);
      setKeyPending('');
      const res = await put('/auth/email', {
        current_password: emailPw,
        new_email: next,
        ...proof,
      });
      if (res?.status === 'confirmation_sent') {
        // Nothing changes until the link mailed to the new address is used.
        // The pending notice above the form announces the next step.
        currentUser.value = {
          ...currentUser.value,
          pending_email: res.pending_email,
          pending_email_requested_at: res.pending_email_requested_at,
        };
        setEmailSuccess(`Confirmation link sent to ${res.pending_email}.`);
      } else {
        // Self-hosted without email delivery: the change applies at once.
        if (res?.token) setToken(res.token);
        if (res?.user) currentUser.value = res.user;
        setEmailSuccess(emailChangeSuccess(res, next) || 'Email updated.');
        setResendMsg(null);
        if (res?.verification_required) setResendCooldown(RESEND_COOLDOWN_SECS);
      }
      setNewEmail(''); setEmailPw(''); setEmailMfa('');
      reloadSecurity();
    } catch (err) {
      setEmailError(emailChangeError(err) || describeEmailChangeError(err) ||
        describeAccountError(err, 'Could not update your email. Try again.'));
    } finally { setEmailLoading(false); setKeyPending(''); }
  }

  async function handleCancelEmailChange() {
    setEmailError(''); setEmailSuccess('');
    try {
      await del('/auth/email/pending');
    } catch (err) {
      // 404: nothing pending any more (confirmed or cancelled elsewhere).
      if (err?.status !== 404) {
        setEmailError(describeAccountError(err, 'Could not cancel the email change. Try again.'));
        return;
      }
    }
    currentUser.value = { ...currentUser.value, pending_email: undefined, pending_email_requested_at: undefined };
    setEmailSuccess('Email change cancelled. Your email stays the same.');
  }

  // Sending a new link goes through the same form, since it needs the
  // password (and step-up) again.
  function handleResendEmailChange(addr) {
    setNewEmail(addr); setEmailError(''); setEmailSuccess('');
    document.getElementById('acct-email-password')?.focus();
  }

  async function handleDeleteAccount(e, viaKey = false) {
    e?.preventDefault();
    if (viaKey && !e?.target?.form?.reportValidity?.()) return;
    setDelLoading(true); setDelError(''); setDelSuccess('');
    if (viaKey || keyOnly(sec)) setKeyPending('delete');
    try {
      const proof = await proofFor(sec, viaKey, delMfa);
      setKeyPending('');
      await post('/auth/delete-account', {
        password: delPw,
        confirm_phrase: delPhrase,
        ...proof,
      });
      setDelSuccess('Check your email for a confirmation link. It works for 1 hour, and your account stays active until you open it.');
      setDelPw(''); setDelMfa(''); setDelPhrase('');
      reloadSecurity();
    } catch (err) {
      setDelError(describeDeleteError(err));
    } finally { setDelLoading(false); setKeyPending(''); }
  }

  return (
    <>
      {noPassword ? (
        <NoPasswordCard ssoEnforced={ssoEnforced} />
      ) : (
      <div class="card">
        <div class="section-header"><h3>Change Password</h3></div>
        <form onSubmit={handlePasswordChange}>
          <div class="form-group">
            <label for="acct-current-password">Current Password</label>
            <input id="acct-current-password" type="password" autoComplete="current-password" value={currentPw} onInput={e => setCurrentPw(e.target.value)} required aria-describedby={pwError ? 'acct-pw-error' : undefined} />
          </div>
          <div class="form-group">
            <label for="acct-new-password">New Password</label>
            <input id="acct-new-password" type="password" autoComplete="new-password" value={newPw} onInput={e => setNewPw(e.target.value)} required
              minLength={MIN_PASSWORD_LENGTH} aria-describedby="acct-new-password-hint" />
            <PasswordHint id="acct-new-password-hint" value={newPw} />
          </div>
          <div class="form-group">
            <label for="acct-confirm-password">Confirm New Password</label>
            <input id="acct-confirm-password" type="password" autoComplete="new-password" value={confirmPw} onInput={e => setConfirmPw(e.target.value)} required
              minLength={MIN_PASSWORD_LENGTH} aria-describedby={pwError ? 'acct-pw-error' : undefined} />
          </div>
          <StepUp sec={sec} idPrefix="acct-pw" code={mfaCode} setCode={setMfaCode}
            errorId={pwError ? 'acct-pw-error' : undefined} busy={pwLoading} keyPending={keyPending === 'pw'}
            onKey={e => handlePasswordChange(e, true)} keyLabel="Verify with security key and update password" />
          <FormError id="acct-pw-error">{pwError}</FormError>
          <FormSuccess>{pwSuccess}</FormSuccess>
          <button type="submit" class="btn btn-primary" disabled={pwLoading}>
            {keyPending === 'pw' ? 'Waiting for your key...' : pwLoading ? 'Updating...' : 'Update Password'}
          </button>
        </form>
      </div>
      )}

      <div class="card" style="margin-top: 16px">
        <div class="section-header"><h3 id="change-email-heading">Change Email</h3></div>
        <p style="font-size: 0.875rem; color: var(--color-text-muted); margin: 0 0 12px">
          Only you can change your email address. Admins and owners cannot change it for you.
        </p>
        {needsEmailVerification(currentUser.value) && (
          <div class="email-verify-notice" role="region" aria-labelledby="email-verify-heading">
            <p id="email-verify-heading" style="margin: 0 0 4px; font-weight: 600">
              {currentUser.value.email} is not verified yet
            </p>
            <p style="margin: 0 0 8px; font-size: 0.875rem">
              Open the link we sent to that address. Until you do, you cannot sign in with your
              password or reset it.
            </p>
            <button
              type="button"
              class="btn btn-secondary btn-sm"
              onClick={handleResendVerification}
              disabled={resendSending || resendCooldown > 0 || resendMsg?.kind === 'unavailable'}
              aria-busy={resendSending ? 'true' : undefined}
              aria-describedby="email-verify-status"
            >
              {resendSending
                ? 'Sending...'
                : resendCooldown > 0
                  ? `Resend link (wait ${resendCooldown}s)`
                  : 'Resend verification link'}
            </button>
            <div id="email-verify-status" aria-live="polite" style="font-size: 0.875rem; margin-top: 6px">
              {resendMsg && (
                <span
                  class={resendMsg.kind === 'ok' || resendMsg.kind === 'verified' ? 'form-success' : 'form-error'}
                  style={resendMsg.kind === 'ok' || resendMsg.kind === 'verified' ? 'color: var(--color-up-text)' : undefined}
                >
                  {resendMsg.message}
                </span>
              )}
            </div>
          </div>
        )}
        {noPassword ? (
          <NeedsPasswordNote ssoEnforced={ssoEnforced} action="change your email" />
        ) : (
        <>
        <PendingEmailNotice user={currentUser.value} onResend={handleResendEmailChange} onCancel={handleCancelEmailChange} />
        <form onSubmit={handleEmailChange} aria-labelledby="change-email-heading">
          <div class="form-group">
            <label for="acct-current-email">Current Email</label>
            <input id="acct-current-email" type="email" value={currentUser.value?.email} disabled />
          </div>
          <div class="form-group">
            <label for="acct-new-email">New Email</label>
            <input id="acct-new-email" type="email" autoComplete="email" value={newEmail} onInput={e => setNewEmail(e.target.value)} required
              aria-describedby={'acct-new-email-help' + (emailError ? ' acct-email-error' : '')} />
            <p id="acct-new-email-help" class="form-help">
              {appMeta.value?.edition === 'foss'
                ? 'If this server sends email, we mail a confirmation link to the new address and the change applies once you open it.'
                : 'We email a confirmation link to the new address. The change applies once you open it, and we let your current address know.'}
            </p>
          </div>
          <div class="form-group">
            <label for="acct-email-password">Current Password</label>
            <input id="acct-email-password" type="password" autoComplete="current-password" value={emailPw} onInput={e => setEmailPw(e.target.value)} required />
          </div>
          <StepUp sec={sec} idPrefix="acct-email" code={emailMfa} setCode={setEmailMfa}
            errorId={emailError ? 'acct-email-error' : undefined} busy={emailLoading} keyPending={keyPending === 'email'}
            onKey={e => handleEmailChange(e, true)} keyLabel="Verify with security key and update email" />
          <FormError id="acct-email-error">{emailError}</FormError>
          <FormSuccess>{emailSuccess}</FormSuccess>
          <button type="submit" class="btn btn-primary" disabled={emailLoading}>
            {keyPending === 'email' ? 'Waiting for your key...' : emailLoading ? 'Saving...' : 'Change Email'}
          </button>
        </form>
        </>
        )}
      </div>

      {isDark() && (
        <div class="card" style="margin-top: 16px">
          <div class="section-header"><h3>Display</h3></div>
          <div class="form-group" style="display: flex; align-items: center; justify-content: space-between">
            <div>
              <label for="acct-oled" style="margin-bottom: 2px">OLED Dark Mode</label>
              <p id="acct-oled-help" style="font-size: 0.8125rem; color: var(--color-text-muted); margin: 0">
                Pure black backgrounds for OLED screens. Reduces battery usage on mobile.
              </p>
            </div>
            <label class="toggle-switch">
              <input
                id="acct-oled"
                type="checkbox"
                aria-describedby="acct-oled-help"
                checked={theme.value === 'oled'}
                onChange={e => setOLED(e.target.checked)}
              />
              <span class="toggle-slider" />
            </label>
          </div>
        </div>
      )}

      <div class="card" style="margin-top: 16px">
        <div class="section-header"><h3 id="acct-signout-heading">Sign out everywhere</h3></div>
        <p id="acct-signout-help" style="font-size: 0.875rem; color: var(--color-text-secondary); margin: 0 0 12px">
          See every browser and device signed in to your account, sign out one you do not
          recognize, or sign out of all other sessions at once.
        </p>
        <a href="/settings/sessions" class="btn btn-outline" aria-describedby="acct-signout-help">
          Manage sessions
        </a>
      </div>

      <div class="card" style="margin-top: 16px">
        <div class="section-header"><h3 style="color: var(--color-down-text)">Delete Account</h3></div>
        <p style="font-size: 0.875rem; color: var(--color-text-muted); margin: 0 0 12px">
          Your account will be disabled right away and deleted after 4 days. To keep it, sign in
          during that time and we will email you a link to restore it.
        </p>
        <p style="font-size: 0.875rem; color: var(--color-text-muted); margin: 0 0 12px">
          Support tickets, incidents and past on-call overrides you created stay with your
          organization and show "Deleted user" as the author. Your name, email, phone, password,
          sign-in methods, team memberships, upcoming on-call overrides and the API keys you
          created are removed.
        </p>
        {noPassword ? (
          <NeedsPasswordNote ssoEnforced={ssoEnforced} action="delete your account" />
        ) : (
        <form onSubmit={handleDeleteAccount}>
          <div class="form-group">
            <label for="acct-delete-password">Password</label>
            <input id="acct-delete-password" type="password" autoComplete="current-password" value={delPw} onInput={e => setDelPw(e.target.value)} required aria-describedby={delError ? 'acct-delete-error' : undefined} />
          </div>
          <div class="form-group">
            <label for="acct-delete-phrase">Confirmation</label>
            <input id="acct-delete-phrase" type="text" autoComplete="off" value={delPhrase} onInput={e => setDelPhrase(e.target.value)} required aria-describedby="acct-delete-phrase-help" />
            <p id="acct-delete-phrase-help" style="font-size: 0.75rem; color: var(--color-text-muted); margin: 4px 0 0">
              Type: <strong>I am sure I wish to delete my account</strong>
            </p>
          </div>
          <StepUp sec={sec} idPrefix="acct-delete" code={delMfa} setCode={setDelMfa}
            errorId={delError ? 'acct-delete-error' : undefined}
            busy={delLoading || !delPw || delPhrase !== 'I am sure I wish to delete my account'}
            keyPending={keyPending === 'delete'}
            onKey={e => handleDeleteAccount(e, true)} keyLabel="Verify with security key and delete account" />
          <FormError id="acct-delete-error">{delError}</FormError>
          <FormSuccess>{delSuccess}</FormSuccess>
          <button
            type="submit"
            class="btn btn-primary"
            style="background: var(--color-down); border-color: var(--color-down)"
            disabled={delLoading || !delPw || delPhrase !== 'I am sure I wish to delete my account'}
          >
            {keyPending === 'delete' ? 'Waiting for your key...' : delLoading ? 'Deleting...' : 'Delete My Account'}
          </button>
        </form>
        )}
      </div>
    </>
  );
}
