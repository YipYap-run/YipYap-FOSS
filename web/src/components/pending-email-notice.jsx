import { useState } from 'preact/hooks';
import { pendingEmailStatus } from '../utils/email-change';
import '../styles/account-email.css';

function formatExpiry(ms) {
  try {
    return new Date(ms).toLocaleString(undefined, {
      weekday: 'short', hour: 'numeric', minute: '2-digit',
    });
  } catch {
    return '';
  }
}

// PendingEmailNotice shows an email change waiting for confirmation in
// Account settings. Sending a new link needs the password again, so
// onResend fills the form below and moves focus there instead of sending
// straight away.
export function PendingEmailNotice({ user, onResend, onCancel }) {
  const [cancelling, setCancelling] = useState(false);
  const st = pendingEmailStatus(user);
  if (!st) return null;

  async function cancel() {
    setCancelling(true);
    try { await onCancel(); } finally { setCancelling(false); }
  }

  return (
    <section class={'email-pending' + (st.expired ? ' is-expired' : '')} aria-labelledby="email-pending-title">
      <h4 id="email-pending-title">
        {st.expired ? 'Your confirmation link expired' : 'Confirm your new email'}
      </h4>
      {st.expired ? (
        <p>
          The link sent to <strong>{st.email}</strong> no longer works. Your email is
          still {user.email}. Send a new link, or cancel the change.
        </p>
      ) : (
        <>
          <p>
            Check <strong>{st.email}</strong> to confirm. Your email stays {user.email} until
            you open the link we sent there
            {st.expiresAt ? <>, which works until {formatExpiry(st.expiresAt)}.</> : '.'}
          </p>
          <p>No email after a few minutes? Check your spam folder, or send a new link.</p>
        </>
      )}
      <div class="email-pending-actions">
        <button type="button" class={'btn btn-sm ' + (st.expired ? 'btn-primary' : 'btn-outline')}
          onClick={() => onResend(st.email)}>
          Send a new link
        </button>
        <button type="button" class="btn btn-outline btn-sm" onClick={cancel} disabled={cancelling}
          aria-busy={cancelling ? 'true' : undefined}>
          {cancelling ? 'Cancelling...' : 'Cancel email change'}
        </button>
      </div>
    </section>
  );
}
