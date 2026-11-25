import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, del } from '../../api/client';
import { Modal, formatTime } from '../../components/ui';
import { ROLE_OPTIONS, roleLabel, assignableRoles } from '../../state/permissions.mjs';
import { appMeta, billingStatus } from '../../state/auth';
import { inviteRequestBody, inviteDeliveryView, inviteStatus, canResendInvite, sentence, inviteSeatLine, inviteSentToast, inviteExpiryDate } from './invites-view.mjs';

// Member invites (org-6, decision core-billing-org#1). Admins never pick a
// password for someone else: they invite an email with a role, and the
// invitee sets their own password from the 7-day accept link.

// InviteDialog creates an invite. An emailed invite closes the dialog with
// an "Invite sent to" toast; otherwise the dialog shows the link to copy
// (no SMTP) or the send failure.
export function InviteDialog({ open, actorRole, onClose, onInvited, toast }) {
  const roles = assignableRoles(actorRole);
  const seatLine = inviteSeatLine(appMeta.value?.billing_enabled, billingStatus.value);
  const [form, setForm] = useState({ email: '', role: 'member' });
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const [result, setResult] = useState(null);

  useEffect(() => {
    if (!open) return;
    setForm({ email: '', role: 'member' });
    setError('');
    setResult(null);
    // billingStatus is only loaded on a full page load, so after an in-app
    // sign-in it is empty. Fetch it here so the seat line shows the current
    // plan and price. A failure only hides the line.
    if (appMeta.value?.billing_enabled) {
      get('/billing', { noRedirect: true })
        .then(b => { billingStatus.value = b; })
        .catch(() => {});
    }
  }, [open]);

  async function submit(e) {
    e.preventDefault();
    if (saving) return;
    setSaving(true);
    setError('');
    try {
      const res = await post('/users/invites', inviteRequestBody(form));
      const view = inviteDeliveryView(res, 'sent');
      onInvited?.();
      if (view.kind === 'emailed' && toast) {
        toast(inviteSentToast(res?.invite?.email || form.email.trim()));
        onClose();
      } else {
        setResult(view);
      }
    } catch (err) {
      if (!err.readonly) setError(sentence(err.message) || 'Could not send the invite.');
      document.getElementById('invite-email')?.focus();
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={result ? result.title : 'Invite member'}>
      {result ? (
        <DeliveryResult view={result} onDone={onClose} />
      ) : (
        <form onSubmit={submit} class="invite-form">
          <p class="form-help">They get a link to set their own password and join. The link expires in 7 days.</p>
          {error && <div class="form-error" role="alert" id="invite-error">{error}</div>}
          <div class="form-group">
            <label for="invite-email">Email</label>
            <input id="invite-email" type="email" autocomplete="off" inputMode="email" maxLength={254} required
                   value={form.email} aria-invalid={error ? 'true' : undefined}
                   aria-describedby={error ? 'invite-error' : undefined}
                   onInput={e => setForm({ ...form, email: e.target.value })} />
          </div>
          <div class="form-group">
            <label for="invite-role">Role</label>
            <select id="invite-role" aria-describedby="invite-role-help" value={form.role}
                    onChange={e => setForm({ ...form, role: e.target.value })}>
              {roles.map(r => <option key={r.value} value={r.value}>{r.label}</option>)}
            </select>
            <p id="invite-role-help" class="form-help">{ROLE_OPTIONS.find(r => r.value === form.role)?.description}</p>
          </div>
          {seatLine && <p class="invite-seat-line" id="invite-seat-line">{seatLine}</p>}
          <div class="btn-group modal-actions">
            <button type="submit" class="btn btn-primary" disabled={saving} aria-busy={saving ? 'true' : undefined}
                    aria-describedby={seatLine ? 'invite-seat-line' : undefined}>
              {saving ? 'Sending...' : 'Send invite'}
            </button>
            <button type="button" class="btn" onClick={onClose} disabled={saving}>Cancel</button>
          </div>
        </form>
      )}
    </Modal>
  );
}

// DeliveryResult shows the outcome of a create or resend. When there is a
// link to share it puts a Copy button first and keeps the link selectable.
function DeliveryResult({ view, onDone }) {
  const [copyNote, setCopyNote] = useState('');
  const [justCopied, setJustCopied] = useState(false);
  const linkRef = useRef(null);
  const firstRef = useRef(null);
  const timer = useRef(null);
  useEffect(() => () => clearTimeout(timer.current), []);
  // The form that had focus is gone; start on the main action.
  useEffect(() => { firstRef.current?.focus(); }, [view]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(view.acceptURL);
      setCopyNote('Link copied to clipboard.');
      setJustCopied(true);
      clearTimeout(timer.current);
      timer.current = setTimeout(() => setJustCopied(false), 2000);
    } catch (_) {
      linkRef.current?.select();
      setCopyNote('Could not copy automatically. The link is selected, press Ctrl+C or Cmd+C.');
    }
  }

  return (
    <div class="invite-result">
      <p class={view.kind === 'failed' ? 'form-error' : undefined} role={view.kind === 'failed' ? 'alert' : undefined}>{view.message}</p>
      {view.acceptURL && (
        <div class="form-group">
          <label for="invite-link">Invite link</label>
          <input id="invite-link" ref={linkRef} type="text" readOnly value={view.acceptURL}
                 class="invite-link" onFocus={e => e.target.select()} />
          <p class="form-help">Anyone with this link can join as this person, so send it only to them.</p>
        </div>
      )}
      {view.warning && <p class="form-error" role="alert">{view.warning}</p>}
      <p class="form-help" role="status" aria-live="polite">{copyNote}</p>
      <div class="btn-group modal-actions">
        {view.acceptURL ? (
          <>
            <button type="button" class="btn btn-primary" ref={firstRef} onClick={copy}>{justCopied ? 'Copied' : 'Copy link'}</button>
            <button type="button" class="btn" onClick={onDone}>Done</button>
          </>
        ) : (
          <button type="button" class="btn btn-primary" ref={firstRef} onClick={onDone}>Done</button>
        )}
      </div>
    </div>
  );
}

// PendingInvites lists invites nobody has accepted yet, expired ones
// included, with Resend and Revoke. invites comes from GET /users/invites.
export function PendingInvites({ invites, actorRole, loadError, onRetry, onChanged, toast }) {
  const [busy, setBusy] = useState('');
  const [resent, setResent] = useState(null);
  const [revokeTarget, setRevokeTarget] = useState(null);
  const [revokeError, setRevokeError] = useState('');

  async function resend(inv) {
    if (busy) return;
    setBusy(inv.id);
    try {
      const res = await post(`/users/invites/${encodeURIComponent(inv.id)}/resend`, {});
      const view = inviteDeliveryView(res, 'resent');
      // An emailed resend needs no dialog; a link to share or a failed send does.
      if (view.kind === 'emailed') toast(`Invite resent to ${inv.email}. The old link no longer works.`);
      else setResent(view);
      onChanged();
    } catch (err) {
      if (!err.readonly) toast(sentence(err.message) || 'Could not resend the invite.', 'error');
      if (err.status === 404 || err.status === 409) onChanged();
    } finally {
      setBusy('');
    }
  }

  async function revoke() {
    const inv = revokeTarget;
    if (!inv || busy) return;
    setBusy(inv.id);
    setRevokeError('');
    try {
      await del(`/users/invites/${encodeURIComponent(inv.id)}`);
      toast(`Invite for ${inv.email} revoked.`);
      setRevokeTarget(null);
      onChanged();
    } catch (err) {
      if (err.status === 404 || err.status === 409) {
        // Accepted or revoked elsewhere in the meantime.
        toast(sentence(err.message) || 'This invite is no longer pending.', 'error');
        setRevokeTarget(null);
        onChanged();
      } else if (!err.readonly) {
        setRevokeError(sentence(err.message) || 'Could not revoke the invite.');
      }
    } finally {
      setBusy('');
    }
  }

  if (loadError) {
    return (
      <section class="invite-section" aria-labelledby="pending-invites-heading">
        <h4 id="pending-invites-heading">Pending invites</h4>
        <p class="form-error" role="alert">
          Could not load pending invites. {' '}
          <button type="button" class="btn btn-xs" onClick={onRetry}>Try again</button>
        </p>
      </section>
    );
  }
  if (!invites || invites.length === 0) return null;

  return (
    <section class="invite-section" aria-labelledby="pending-invites-heading">
      <h4 id="pending-invites-heading">Pending invites ({invites.length})</h4>
      <ul class="invite-list">
        {invites.map(inv => {
          const st = inviteStatus(inv);
          const canResend = canResendInvite(actorRole, inv);
          const expiry = inviteExpiryDate(inv);
          return (
            <li key={inv.id} class="invite-item">
              <div class="invite-item-text">
                <span class="invite-email">{inv.email}</span>
                <span class="invite-meta">
                  {roleLabel(inv.role)}
                  {inv.invited_by_name ? ` · Invited by ${inv.invited_by_name}` : ''}
                  {` · Sent ${formatTime(inv.created_at)}`}
                </span>
                <span class="invite-status">
                  <span class={`badge badge-sm ${st.tone === 'danger' ? 'badge-down' : st.tone === 'warning' ? 'badge-warning' : 'badge-muted'}`}>
                    {st.label}
                  </span>
                  {expiry && <time class="invite-expiry" dateTime={inv.expires_at}>{expiry}</time>}
                </span>
              </div>
              <div class="btn-group invite-actions">
                {canResend && (
                  <button type="button" class="btn btn-xs" disabled={busy === inv.id}
                          aria-label={`Resend invite to ${inv.email}`} onClick={() => resend(inv)}>
                    {busy === inv.id ? 'Sending...' : 'Resend'}
                  </button>
                )}
                <button type="button" class="btn btn-xs btn-danger" disabled={busy === inv.id}
                        aria-label={`Revoke invite for ${inv.email}`}
                        onClick={() => { setRevokeError(''); setRevokeTarget(inv); }}>
                  Revoke
                </button>
              </div>
            </li>
          );
        })}
      </ul>
      <Modal open={!!resent} onClose={() => setResent(null)} title={resent?.title || ''}>
        {resent && <DeliveryResult view={resent} onDone={() => setResent(null)} />}
      </Modal>
      <Modal open={!!revokeTarget} onClose={() => setRevokeTarget(null)} title="Revoke invite">
        <p>The link sent to <strong>{revokeTarget?.email}</strong> will stop working. You can invite them again later.</p>
        {revokeError && <p class="form-error" role="alert">{revokeError}</p>}
        <div class="btn-group modal-actions">
          <button type="button" class="btn btn-danger" disabled={!!busy} onClick={revoke}>
            {busy ? 'Revoking...' : 'Revoke invite'}
          </button>
          <button type="button" class="btn" data-autofocus onClick={() => setRevokeTarget(null)}>Cancel</button>
        </div>
      </Modal>
    </section>
  );
}
