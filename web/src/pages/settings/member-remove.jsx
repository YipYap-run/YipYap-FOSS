import { useState, useEffect } from 'preact/hooks';
import { get, del } from '../../api/client';
import { Modal } from '../../components/ui';
import { revokePrompt, removeMemberPath, removeMemberResultText, expiryStatus, keyCountText } from './api-keys-view.mjs';

// RemoveMemberDialog confirms removing a member. API keys belong to the org
// and survive the removal, so when the member created keys the dialog asks
// whether to revoke them in the same request (org-5, core-billing-org#6).
// onDone(messages) runs after a successful removal with the toasts to show.
export function RemoveMemberDialog({ member, onClose, onDone }) {
  const [keys, setKeys] = useState(null);
  const [loadError, setLoadError] = useState('');
  const [choice, setChoice] = useState('');
  const [choiceError, setChoiceError] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [removing, setRemoving] = useState(false);

  async function loadKeys() {
    setKeys(null);
    setLoadError('');
    try {
      const res = await get(`/users/${encodeURIComponent(member.id)}/api-keys`);
      setKeys(res?.keys || []);
    } catch (err) {
      setLoadError(err.message || 'Could not check this member\'s API keys.');
    }
  }

  useEffect(() => {
    if (!member) return;
    setChoice('');
    setChoiceError('');
    setSubmitError('');
    loadKeys();
  }, [member?.id]);

  const count = keys?.length || 0;

  async function submit(e) {
    e.preventDefault();
    if (removing || keys === null) return;
    if (count > 0 && !choice) {
      setChoiceError('Choose whether to revoke or keep the keys.');
      document.getElementById('remove-member-keys-legend')?.focus();
      return;
    }
    const revoke = choice === 'revoke';
    setRemoving(true);
    setSubmitError('');
    try {
      const res = await del(removeMemberPath(member.id, count, revoke));
      const messages = [{ text: removeMemberResultText(res, count, revoke) }];
      if (res?.billing_warning) messages.push({ text: res.billing_warning, type: 'error' });
      onDone(messages);
    } catch (err) {
      if (!err.readonly) setSubmitError(err.message || 'Could not remove the member.');
    } finally {
      setRemoving(false);
    }
  }

  const who = member?.name ? `${member.name} (${member.email})` : member?.email;

  return (
    <Modal open={!!member} onClose={() => { if (!removing) onClose(); }} title="Remove member">
      {member && (
        <form onSubmit={submit} noValidate class="member-remove-form">
          <p>Remove <strong>{who}</strong> from this organization? They lose access right away.</p>
          {keys === null && !loadError && (
            <p class="text-muted" role="status">Checking for API keys they created...</p>
          )}
          {loadError && (
            <div class="form-error" role="alert">
              <p>{loadError}</p>
              <button type="button" class="btn btn-xs" onClick={loadKeys}>Try again</button>
            </div>
          )}
          {count > 0 && (
            <fieldset class="form-group api-key-fieldset" aria-describedby={choiceError ? 'remove-member-keys-error' : undefined}>
              <legend id="remove-member-keys-legend" tabIndex={-1}>{revokePrompt(count)}</legend>
              <ul class="remove-member-keys">
                {keys.map(k => (
                  <li key={k.id}>
                    <strong>{k.name || 'Unnamed key'}</strong>{k.prefix && <> <code>{k.prefix}...</code></>}{' '}
                    <span class="text-muted">{expiryStatus(k).label}</span>
                  </li>
                ))}
              </ul>
              <label class="api-key-choice">
                <input type="radio" name="remove-member-keys" value="revoke" checked={choice === 'revoke'} onChange={() => { setChoice('revoke'); setChoiceError(''); }} />
                <span><strong>Revoke {count === 1 ? 'it' : `all ${count}`}</strong><span class="api-key-choice-help">Anything using {count === 1 ? 'this key' : 'these keys'} stops working.</span></span>
              </label>
              <label class="api-key-choice">
                <input type="radio" name="remove-member-keys" value="keep" checked={choice === 'keep'} onChange={() => { setChoice('keep'); setChoiceError(''); }} />
                <span><strong>Keep {count === 1 ? 'it' : 'them'} working</strong><span class="api-key-choice-help">The {keyCountText(count)} stay listed under API Keys as created by a removed member.</span></span>
              </label>
              {choiceError && <p id="remove-member-keys-error" class="field-error">{choiceError}</p>}
            </fieldset>
          )}
          {submitError && <p class="form-error" role="alert">{submitError}</p>}
          <div class="btn-group modal-actions">
            <button type="submit" class="btn btn-danger" disabled={removing || keys === null}
                    aria-busy={removing ? 'true' : undefined}>
              {removing ? 'Removing...' : 'Remove member'}
            </button>
            <button type="button" class="btn" onClick={onClose} disabled={removing}>Cancel</button>
          </div>
        </form>
      )}
    </Modal>
  );
}
