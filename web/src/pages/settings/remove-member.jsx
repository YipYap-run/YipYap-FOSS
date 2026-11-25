import { useState, useEffect } from 'preact/hooks';
import { del } from '../../api/client';
import { Modal, FormError } from '../../components/ui';

// describeRemoveError turns a DELETE /users/{id} failure into a sentence.
// 404 means the member is already gone (removed in another tab or by
// another admin); 500 is a server failure and the member is unchanged.
export function describeRemoveError(err) {
  if (err?.status === 404) return 'This member was already removed. The list has been refreshed.';
  if (err?.status === 403) return 'You do not have permission to remove this member.';
  if (err?.status >= 500) return 'Could not remove this member. Nothing was changed. Try again.';
  return err?.message || 'Could not remove this member. Try again.';
}

// RemoveMemberDialog confirms deleting a member's account. Admin removal
// deletes the account right away (there is no 4-day restore window as with
// self-service deletion), so the dialog spells out what stays and what goes
// before the admin commits.
export function RemoveMemberDialog({ member, onClose, onRemoved }) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => { setBusy(false); setError(''); }, [member?.id]);

  async function confirmRemove() {
    if (!member) return;
    setBusy(true);
    setError('');
    try {
      await del(`/users/${member.id}`);
      onRemoved(member, null);
    } catch (err) {
      setBusy(false);
      if (err?.status === 404) {
        onRemoved(member, err);
        return;
      }
      setError(describeRemoveError(err));
    }
  }

  const who = member ? (member.name ? `${member.name} (${member.email})` : member.email) : '';

  return (
    <Modal open={!!member} onClose={busy ? undefined : onClose} title="Remove member">
      <p id="remove-member-summary" style="margin: 0 0 12px; overflow-wrap: anywhere">
        <strong>{who}</strong> will be removed from your organization and their account deleted
        right away. This cannot be undone.
      </p>
      <ul style="margin: 0 0 12px; padding-left: 20px; font-size: 0.875rem; line-height: 1.5">
        <li>They are signed out on every device.</li>
        <li>API keys they created stop working.</li>
        <li>They are taken off their teams and any upcoming on-call overrides.</li>
        <li>
          Support tickets, incidents and past on-call overrides they wrote stay, with
          "Deleted user" shown as the author.
        </li>
      </ul>
      <FormError id="remove-member-error">{error}</FormError>
      <div class="btn-group" style="justify-content: flex-end; flex-wrap: wrap">
        <button type="button" class="btn" onClick={onClose} disabled={busy}>Cancel</button>
        <button
          type="button"
          class="btn btn-danger"
          onClick={confirmRemove}
          disabled={busy}
          aria-describedby={error ? 'remove-member-summary remove-member-error' : 'remove-member-summary'}
        >
          {busy ? 'Removing...' : 'Remove and delete account'}
        </button>
      </div>
    </Modal>
  );
}
