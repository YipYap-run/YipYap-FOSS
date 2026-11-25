import { useState, useEffect } from 'preact/hooks';
import { post } from '../../api/client';
import { Modal } from '../../components/ui';
import { currentUser } from '../../state/auth';
import { sentence } from './invites-view.mjs';
import { transferRequestBody, transferErrorField, TRANSFER_CONSEQUENCE } from './members-view.mjs';
import { UserPicker } from './user-picker.jsx';

// TransferOwnershipDialog hands the org to an admin (org-3). The server
// needs user_id, the owner's current password and, when the owner has MFA,
// a code; it answers step-up failures with a 403 and a code naming the
// field, which this dialog marks and focuses. onDone(newOwnerEmail) runs on
// success.
export function TransferOwnershipDialog({ open, onClose, onDone }) {
  const [userId, setUserId] = useState('');
  const [password, setPassword] = useState('');
  const [mfaCode, setMfaCode] = useState('');
  // Show the code field up front for app MFA; the server also asks for it
  // when the owner only has a security key, so a mfa_required reply shows it.
  const [askMfa, setAskMfa] = useState(false);
  const [error, setError] = useState('');
  const [errorField, setErrorField] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setUserId('');
    setPassword('');
    setMfaCode('');
    setAskMfa(!!currentUser.value?.mfa_app_enabled);
    setError('');
    setErrorField('');
  }, [open]);

  function fail(field, message) {
    setErrorField(field);
    setError(message);
    const target = { target: 'transfer-target', password: 'transfer-password', mfa: 'transfer-mfa' }[field];
    if (target) setTimeout(() => document.getElementById(target)?.focus(), 0);
  }

  async function submit(e) {
    e.preventDefault();
    if (saving) return;
    if (!userId) return fail('target', 'Choose the admin who will become the owner.');
    if (!password) return fail('password', 'Enter your current password.');
    if (askMfa && !mfaCode.trim()) return fail('mfa', 'Enter a code from your authenticator app or a backup code.');
    setSaving(true);
    setError('');
    setErrorField('');
    try {
      const res = await post('/org/transfer-ownership', transferRequestBody({ userId, password, mfaCode }));
      setSaving(false);
      onDone(res?.new_owner || '');
      return;
    } catch (err) {
      if (!err.readonly) {
        const field = transferErrorField(err);
        if (field === 'mfa') setAskMfa(true);
        fail(field, sentence(err.message) || 'Could not transfer ownership.');
      }
    }
    setSaving(false);
  }

  const described = field => (errorField === field ? 'transfer-error' : undefined);

  return (
    <Modal open={open} onClose={onClose} title="Transfer ownership">
      <p style="margin-bottom: 0.5rem;">Choose an admin to become the new owner.</p>
      <p class="form-warning" style="margin-bottom: 1rem;"><strong>{TRANSFER_CONSEQUENCE}</strong></p>
      <form onSubmit={submit} noValidate>
        {error && <div class="form-error" role="alert" id="transfer-error">{error}</div>}
        <UserPicker id="transfer-target" label="New owner" role="admin" value={userId} onChange={setUserId}
                    required invalid={errorField === 'target'} describedBy={described('target')} />
        <div class="form-group">
          <label for="transfer-password">Your current password</label>
          <input id="transfer-password" type="password" autocomplete="current-password" required
                 value={password} onInput={e => setPassword(e.target.value)}
                 aria-invalid={errorField === 'password' ? 'true' : undefined}
                 aria-describedby={described('password')} />
        </div>
        {askMfa && (
          <div class="form-group">
            <label for="transfer-mfa">Authentication code</label>
            <input id="transfer-mfa" type="text" inputMode="text" autocomplete="one-time-code" maxLength={32}
                   value={mfaCode} onInput={e => setMfaCode(e.target.value)}
                   aria-invalid={errorField === 'mfa' ? 'true' : undefined}
                   aria-describedby={['transfer-mfa-help', described('mfa')].filter(Boolean).join(' ')} />
            <p id="transfer-mfa-help" class="form-help">A 6-digit code from your authenticator app, or a backup code.</p>
          </div>
        )}
        <div class="btn-group modal-actions">
          <button type="submit" class="btn btn-danger" disabled={saving} aria-busy={saving ? 'true' : undefined}>
            {saving ? 'Transferring...' : 'Transfer ownership'}
          </button>
          <button type="button" class="btn" onClick={onClose} disabled={saving}>Cancel</button>
        </div>
      </form>
    </Modal>
  );
}
