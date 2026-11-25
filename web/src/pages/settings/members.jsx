import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, formatTime, useToast, ToastContainer } from '../../components/ui';
import { currentOrg, currentUser, appMeta } from '../../state/auth';
import { RemoveMemberDialog } from './member-remove.jsx';
import { InviteDialog, PendingInvites } from './member-invites.jsx';
import { freeSeatUse, FREE_MAX_MEMBERS, PASSWORD_MIN, sentence, roleChangedToast, canManageMember } from './invites-view.mjs';
import { isOrgAdmin, roleLabel, roleSelectOptions, ROLE_OPTIONS } from '../../state/permissions.mjs';
import { usersQuery, userPage, pageSummary, pageNav, MEMBERS_PAGE_SIZE } from './members-view.mjs';
import { TransferOwnershipDialog } from './transfer-ownership.jsx';
import { MemberPagingModal } from './contact-methods.jsx';

// MembersTab lists the org's members and, for owners and admins, the invite
// flow, pending invites, role changes and removal (org-6, decision
// core-billing-org#1). Moved out of settings/index.jsx so the members area
// can change without touching the shared settings page.
export function MembersTab() {
  const [members, setMembers] = useState([]);
  // total counts every member, not just this page; the seat line uses it.
  const [total, setTotal] = useState(0);
  const [offset, setOffset] = useState(0);
  const [search, setSearch] = useState('');
  const [query, setQuery] = useState('');
  const [searching, setSearching] = useState(false);
  const [orgTotal, setOrgTotal] = useState(0);
  const [adminCount, setAdminCount] = useState(0);
  const [loading, setLoading] = useState(true);
  const [showInvite, setShowInvite] = useState(false);
  const [showTransfer, setShowTransfer] = useState(false);
  const [resetTarget, setResetTarget] = useState(null);
  const [pagingTarget, setPagingTarget] = useState(null);
  const [removeTarget, setRemoveTarget] = useState(null);
  const [resetPassword, setResetPassword] = useState('');
  const [resetError, setResetError] = useState('');
  const [invites, setInvites] = useState([]);
  const [invitesError, setInvitesError] = useState(null);
  const [roleBusy, setRoleBusy] = useState('');
  const [saving, setSaving] = useState(false);
  const { toasts, toast } = useToast();
  const [loadError, setLoadError] = useState(null);
  const role = currentUser.value?.role;
  const myId = currentUser.value?.id;
  const isOwnerOrAdmin = isOrgAdmin(role);

  // Pending invites are owner/admin only on the server; other roles skip
  // the request.
  async function loadInvites() {
    if (!isOwnerOrAdmin) return;
    setInvitesError(null);
    try {
      const data = await get('/users/invites');
      setInvites(Array.isArray(data) ? data : []);
    } catch (err) {
      if (!err.readonly) setInvitesError(err.message || 'Could not load invites.');
    }
  }

  // loadMembers fetches one page. With no search the total is the org's
  // member count; a search total only counts matches, so the seat line keeps
  // the last unfiltered count.
  async function loadMembers(nextOffset = offset, q = query) {
    const page = userPage(await get(usersQuery({ q, limit: MEMBERS_PAGE_SIZE, offset: nextOffset })));
    // Stepping past the end (the last person on a page was removed) goes
    // back one page.
    if (page.users.length === 0 && nextOffset > 0 && page.total > 0) {
      return loadMembers(Math.max(0, nextOffset - MEMBERS_PAGE_SIZE), q);
    }
    setMembers(page.users);
    setTotal(page.total);
    setOffset(nextOffset);
    if (!q) setOrgTotal(page.total);
  }

  // Transfer Ownership is offered only when the org has an admin; ask the
  // server instead of looking at the current page.
  async function loadAdminCount() {
    if (role !== 'owner') return;
    try {
      setAdminCount(userPage(await get(usersQuery({ role: 'admin', limit: 1 }))).total);
    } catch (_) {
      setAdminCount(0);
    }
  }

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      await Promise.all([loadMembers(0, ''), loadInvites(), loadAdminCount()]);
      setSearch('');
      setQuery('');
    } catch (err) {
      setLoadError(err.message || 'Could not load members.');
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  // Search as the person types, after a short pause.
  const firstSearch = useRef(true);
  useEffect(() => {
    if (firstSearch.current) { firstSearch.current = false; return; }
    setSearching(true);
    const t = setTimeout(async () => {
      const q = search.trim();
      try {
        await loadMembers(0, q);
        setQuery(q);
      } catch (err) {
        if (!err.readonly) toast(sentence(err.message) || 'Could not search members.', 'error');
      }
      setSearching(false);
    }, 250);
    return () => clearTimeout(t);
  }, [search]);

  async function goToPage(nextOffset) {
    try {
      await loadMembers(nextOffset, query);
      document.getElementById('members-heading')?.focus();
    } catch (err) {
      if (!err.readonly) toast(sentence(err.message) || 'Could not load that page.', 'error');
    }
  }

  // Refresh after a change without swapping the table for a spinner, so
  // focus stays on the control the person just used.
  async function refresh() {
    try {
      await Promise.all([loadMembers(offset, query), loadInvites(), loadAdminCount()]);
      // A search total is not the org total; refresh the seat count too.
      if (query) setOrgTotal(userPage(await get(usersQuery({ limit: 1 }))).total);
    } catch (err) {
      if (!err.readonly) toast(sentence(err.message) || 'Could not refresh the member list.', 'error');
    }
  }

  async function changeRole(member, newRole) {
    if (roleBusy || newRole === member.role) return;
    setRoleBusy(member.id);
    try {
      await patch(`/users/${encodeURIComponent(member.id)}`, { role: newRole });
      toast(roleChangedToast(member.email, roleLabel(newRole)));
    } catch (err) {
      if (!err.readonly) toast(sentence(err.message) || 'Could not change the role.', 'error');
    }
    // Reload either way so the select shows the role the server kept.
    await refresh();
    setRoleBusy('');
  }

  // The removal dialog asks about API keys the member created, then sends
  // the DELETE itself and reports back here.
  function memberRemoved(messages) {
    setRemoveTarget(null);
    for (const m of messages) toast(m.text, m.type);
    refresh();
  }

  async function resetMemberPassword(e) {
    e.preventDefault();
    if (!resetTarget || saving) return;
    if (resetPassword.length < PASSWORD_MIN) {
      setResetError(`Use at least ${PASSWORD_MIN} characters.`);
      document.getElementById('reset-password')?.focus();
      return;
    }
    setSaving(true);
    setResetError('');
    try {
      await post(`/users/${encodeURIComponent(resetTarget.id)}/reset-password`, { temporary_password: resetPassword });
      toast(`Temporary password set for ${resetTarget.email}. Share it with them privately. They must change it when they next sign in.`);
      setResetTarget(null);
      setResetPassword('');
    } catch (err) {
      if (!err.readonly) setResetError(sentence(err.message) || 'Could not reset the password.');
    }
    setSaving(false);
  }

  // After a transfer the server already reads the new role from the
  // database; update the local copy so owner-only controls go away.
  function ownershipTransferred(newOwnerEmail) {
    setShowTransfer(false);
    currentUser.value = { ...currentUser.value, role: 'admin' };
    toast(`${newOwnerEmail || 'The new owner'} now owns this organization. You are an admin.`);
    refresh();
  }

  if (loading) return <LoadingPage />;
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  // The Free cap counts unexpired pending invites as well as members, the
  // same way the server does when an invite is created.
  const seats = freeSeatUse(orgTotal, invites);
  const freeLimited = appMeta.value?.billing_enabled && currentOrg.value?.plan === 'free' && seats.full;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3 id="members-heading" tabIndex={-1}>Members</h3>
        {isOwnerOrAdmin && (
          freeLimited
            ? <a href="/settings/billing" class="btn btn-sm btn-primary">Upgrade to add more members</a>
            : <button type="button" class="btn btn-sm btn-primary" onClick={() => setShowInvite(true)}>Invite member</button>
        )}
      </div>
      {isOwnerOrAdmin && freeLimited && (
        <p class="form-help">
          The Free plan allows {FREE_MAX_MEMBERS} members, and pending invites count toward it.
          {seats.pending > 0 && ' Revoke a pending invite to invite someone else.'}
        </p>
      )}
      <div class="form-group members-search" role="search">
        <label for="members-search">Search members</label>
        <input id="members-search" type="search" value={search} autocomplete="off"
               placeholder="Email or name" onInput={e => setSearch(e.target.value)}
               aria-describedby="members-count" />
      </div>
      <p id="members-count" class="form-help" aria-live="polite">
        {searching ? 'Searching...' : query ? `${pageSummary(offset, members.length, total, 'matches')} for "${query}"` : pageSummary(offset, members.length, total)}
      </p>
      <div class="settings-list">
        {members.length === 0 ? (
          <p class="text-muted">{query ? 'No members match that search.' : 'No members found.'}</p>
        ) : (
          <table class="data-table members-table" role="table">
            <caption class="sr-only">Organization members</caption>
            <thead role="rowgroup">
              <tr role="row">
                <th role="columnheader" scope="col">Email</th>
                <th role="columnheader" scope="col">Name</th>
                <th role="columnheader" scope="col">Role</th>
                <th role="columnheader" scope="col">Joined</th>
                {isOwnerOrAdmin && <th role="columnheader" scope="col"><span class="sr-only">Actions</span></th>}
              </tr>
            </thead>
            <tbody role="rowgroup">
              {members.map(m => {
                const manage = canManageMember(role, myId, m);
                const busy = roleBusy === m.id;
                return (
                  <tr key={m.id} role="row">
                    <td role="cell" data-label="Email" class="member-email">
                      {m.email}
                      {m.id === myId && <span class="text-muted"> (you)</span>}
                    </td>
                    <td role="cell" data-label="Name">{m.name || <span class="text-muted">Not set</span>}</td>
                    <td role="cell" data-label="Role">
                      {manage ? (
                        <select value={m.role} aria-label={`Role for ${m.email}`} disabled={busy}
                                aria-busy={busy ? 'true' : undefined}
                                onChange={e => changeRole(m, e.target.value)}>
                          {roleSelectOptions(role, m.role).map(r => (
                            <option key={r.value} value={r.value} disabled={r.disabled}>{r.label}</option>
                          ))}
                        </select>
                      ) : (
                        roleLabel(m.role)
                      )}
                    </td>
                    <td role="cell" data-label="Joined">{formatTime(m.created_at)}</td>
                    {isOwnerOrAdmin && (
                      <td role="cell" data-label="" class="member-actions">
                        <div class="btn-group">
                          <button type="button" class="btn btn-xs" aria-label={`How ${m.name || m.email} is paged`}
                                  onClick={() => setPagingTarget(m)}>
                            Paging
                          </button>
                          {manage && (
                            <>
                              <button type="button" class="btn btn-xs" aria-label={`Reset password for ${m.email}`}
                                    onClick={() => { setResetTarget(m); setResetPassword(''); setResetError(''); }}>
                                Reset password
                              </button>
                              <button type="button" class="btn btn-xs btn-danger" aria-label={`Remove ${m.email}`}
                                      onClick={() => setRemoveTarget(m)}>
                                Remove
                              </button>
                            </>
                          )}
                        </div>
                      </td>
                    )}
                  </tr>
                );
              })}
            </tbody>
          </table>
        )}
        {total > MEMBERS_PAGE_SIZE && (
          <nav class="checks-pagination" aria-label="Member list pages">
            <span class="text-muted">{pageNav(offset, total).label}</span>
            <div class="btn-group">
              <button type="button" class="btn btn-sm" disabled={offset === 0} aria-label="Previous page of members"
                      onClick={() => goToPage(Math.max(0, offset - MEMBERS_PAGE_SIZE))}>Previous</button>
              <button type="button" class="btn btn-sm" disabled={offset + members.length >= total} aria-label="Next page of members"
                      onClick={() => goToPage(offset + MEMBERS_PAGE_SIZE)}>Next</button>
            </div>
          </nav>
        )}
      </div>
      {isOwnerOrAdmin && (
        <PendingInvites invites={invites} actorRole={role} loadError={invitesError}
                        onRetry={loadInvites} onChanged={loadInvites} toast={toast} />
      )}
      <h4 class="role-legend-title">What each role can do</h4>
      <dl class="role-legend">
        {ROLE_OPTIONS.map(r => (
          <div key={r.value}><dt>{r.label}</dt><dd>{r.description}</dd></div>
        ))}
      </dl>
      {role === 'owner' && (
        <div style="margin-top: 1rem;">
          {adminCount > 0 ? (
            <button type="button" class="btn btn-sm" onClick={() => setShowTransfer(true)}>Transfer ownership</button>
          ) : (
            <p class="form-help">To transfer ownership, first make the new owner an admin.</p>
          )}
        </div>
      )}
      {isOwnerOrAdmin && (
        <InviteDialog open={showInvite} actorRole={role} onClose={() => setShowInvite(false)} onInvited={loadInvites} toast={toast} />
      )}
      <MemberPagingModal member={pagingTarget} onClose={() => setPagingTarget(null)} />
      <Modal open={!!resetTarget} onClose={() => setResetTarget(null)} title="Reset password">
        <p class="text-muted" style="margin-bottom: 1rem;">
          Set a temporary password for <strong>{resetTarget?.email}</strong>. They must change it when they next sign in. Share it with them privately.
        </p>
        <form onSubmit={resetMemberPassword} noValidate>
          {resetError && <div class="form-error" role="alert" id="reset-password-error">{resetError}</div>}
          <div class="form-group">
            <label for="reset-password">Temporary password</label>
            <input id="reset-password" type="password" autocomplete="new-password" required minLength={PASSWORD_MIN}
                   value={resetPassword} onInput={e => setResetPassword(e.target.value)}
                   aria-invalid={resetError ? 'true' : undefined}
                   aria-describedby={resetError ? 'reset-password-help reset-password-error' : 'reset-password-help'} />
            <p id="reset-password-help" class="form-help">At least {PASSWORD_MIN} characters.</p>
          </div>
          <div class="btn-group modal-actions">
            <button type="submit" class="btn btn-primary" disabled={saving} aria-busy={saving ? 'true' : undefined}>
              {saving ? 'Resetting...' : 'Reset password'}
            </button>
            <button type="button" class="btn" onClick={() => setResetTarget(null)} disabled={saving}>Cancel</button>
          </div>
        </form>
      </Modal>
      <RemoveMemberDialog member={removeTarget} onClose={() => setRemoveTarget(null)} onDone={memberRemoved} />
      {role === 'owner' && (
        <TransferOwnershipDialog open={showTransfer} onClose={() => setShowTransfer(false)} onDone={ownershipTransferred} />
      )}
    </div>
  );
}
