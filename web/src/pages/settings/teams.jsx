import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, useToast, ToastContainer } from '../../components/ui';
import { currentUser } from '../../state/auth';
import { canWrite } from '../../state/permissions.mjs';
import { sentence } from './invites-view.mjs';
import { teamMemberLabel, teamSaveError, parsePosition } from './members-view.mjs';
import { UpgradeLink } from '../../components/upgrade-link';
import { UserPicker } from './user-picker.jsx';

function nextPosition(members) {
  return (members || []).reduce((max, m) => Math.max(max, m.position || 0), 0) + 1;
}

// errorText turns a failed request into toast copy.
function errorText(err, fallback) {
  return sentence(err?.message) || fallback;
}

// TeamsTab manages teams and their on-call rotation members (ux-2, ux-3).
// Moved out of settings/index.jsx. Every change reports success or failure,
// a failed load shows an error with Retry instead of the empty state, and
// the member picker searches the whole org instead of the first page.
export function TeamsTab() {
  // Viewers are read-only (decision core-billing-org#5).
  const writer = canWrite(currentUser.value?.role);
  const [teams, setTeams] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ name: '' });
  // formError is { text, upgrade }; a plan-limit 403 sets upgrade.
  const [formError, setFormError] = useState(null);
  const [editId, setEditId] = useState(null);
  const [saving, setSaving] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [membersTeam, setMembersTeam] = useState(null);
  const [members, setMembers] = useState([]);
  const [membersError, setMembersError] = useState(null);
  const [membersLoading, setMembersLoading] = useState(false);
  const [memberForm, setMemberForm] = useState({ user_id: '', position: 1 });
  const [memberBusy, setMemberBusy] = useState(false);
  // memberError is { field: 'user' | 'position' | '', text }.
  const [memberError, setMemberError] = useState(null);
  const [removeTarget, setRemoveTarget] = useState(null);
  const { toasts, toast } = useToast();

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      const data = await get('/teams');
      setTeams(data?.teams || data || []);
    } catch (err) {
      setLoadError(err.message || 'Could not load teams.');
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  // Refresh after a change without replacing the list with a spinner.
  async function refresh() {
    try {
      const data = await get('/teams');
      setTeams(data?.teams || data || []);
    } catch (err) {
      if (!err.readonly) toast(errorText(err, 'Could not refresh the team list.'), 'error');
    }
  }

  function openForm(team) {
    setEditId(team ? team.id : null);
    setForm({ name: team?.name || '' });
    setFormError(null);
    setShowModal(true);
  }

  async function save(e) {
    e.preventDefault();
    if (saving) return;
    const name = form.name.trim();
    if (!name) {
      setFormError({ text: 'Enter a team name.', upgrade: false });
      document.getElementById('team-name')?.focus();
      return;
    }
    setSaving(true);
    setFormError(null);
    try {
      const body = { name };
      if (editId) await patch(`/teams/${encodeURIComponent(editId)}`, body);
      else await post('/teams', body);
      setShowModal(false);
      toast(editId ? `Saved ${name}.` : `Created ${name}.`);
      setEditId(null);
      await refresh();
    } catch (err) {
      if (!err.readonly) {
        setFormError(teamSaveError(err));
        setTimeout(() => document.getElementById('team-name')?.focus(), 0);
      }
    }
    setSaving(false);
  }

  async function confirmDelete() {
    const team = deleteTarget;
    if (!team || saving) return;
    setSaving(true);
    try {
      await del(`/teams/${encodeURIComponent(team.id)}`);
      setDeleteTarget(null);
      toast(`Deleted ${team.name}.`);
      await refresh();
    } catch (err) {
      if (!err.readonly) toast(errorText(err, 'Could not delete the team.'), 'error');
    }
    setSaving(false);
  }

  async function loadMembers(teamId) {
    setMembersLoading(true);
    setMembersError(null);
    try {
      const detail = await get(`/teams/${encodeURIComponent(teamId)}`);
      const list = detail?.members || [];
      setMembers(list);
      setMemberForm({ user_id: '', position: String(nextPosition(list)) });
    } catch (err) {
      setMembers([]);
      setMembersError(err.message || 'Could not load team members.');
    }
    setMembersLoading(false);
  }

  function openMembers(team) {
    setMembersTeam(team);
    setMembers([]);
    setMemberError(null);
    loadMembers(team.id);
  }

  async function addMember(e) {
    e.preventDefault();
    if (memberBusy || !membersTeam) return;
    const position = parsePosition(memberForm.position);
    if (!memberForm.user_id) return memberFail('user', 'Choose a member to add.');
    if (position === null) return memberFail('position', 'Enter a whole number, 0 or more.');
    setMemberBusy(true);
    setMemberError(null);
    try {
      await post(`/teams/${encodeURIComponent(membersTeam.id)}/members`, { user_id: memberForm.user_id, position });
      toast(`Added to ${membersTeam.name}.`);
      await loadMembers(membersTeam.id);
      document.getElementById('team-member-user-search')?.focus();
    } catch (err) {
      if (!err.readonly) memberFail('', errorText(err, 'Could not add the member.'));
    }
    setMemberBusy(false);
  }

  function memberFail(field, text) {
    setMemberError({ field, text });
    const target = { user: 'team-member-user', position: 'team-member-position' }[field];
    if (target) setTimeout(() => document.getElementById(target)?.focus(), 0);
  }

  // The inline remove confirmation replaces the Remove button, so move focus
  // to its safe choice, and back to the row's Remove button on Keep.
  function askRemove(m) {
    setRemoveTarget(m);
    setTimeout(() => document.getElementById(`team-member-keep-${m.user_id}`)?.focus(), 0);
  }

  function keepMember() {
    const m = removeTarget;
    setRemoveTarget(null);
    if (m) setTimeout(() => document.getElementById(`team-member-remove-${m.user_id}`)?.focus(), 0);
  }

  async function confirmRemoveMember() {
    const m = removeTarget;
    if (!m || memberBusy || !membersTeam) return;
    setMemberBusy(true);
    try {
      await del(`/teams/${encodeURIComponent(membersTeam.id)}/members/${encodeURIComponent(m.user_id)}`);
      setRemoveTarget(null);
      toast(`Removed ${teamMemberLabel(m)} from ${membersTeam.name}.`);
      await loadMembers(membersTeam.id);
      document.getElementById('team-members-title')?.focus();
    } catch (err) {
      if (!err.readonly) toast(errorText(err, 'Could not remove the member.'), 'error');
    }
    setMemberBusy(false);
  }

  function closeMembers() {
    setMembersTeam(null);
    setMembers([]);
    setMembersError(null);
    setRemoveTarget(null);
    setMemberError(null);
  }

  if (loading) return <LoadingPage />;
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Teams</h3>
        {writer && (
          <button type="button" class="btn btn-sm btn-primary" onClick={() => openForm(null)}>Add team</button>
        )}
      </div>
      <div class="settings-list">
        {teams.map(t => (
          <div key={t.id} class="settings-item">
            <div><strong>{t.name}</strong></div>
            <div class="btn-group">
              <button type="button" class="btn btn-xs" aria-label={`Members of ${t.name}`} onClick={() => openMembers(t)}>Members</button>
              {writer && <button type="button" class="btn btn-xs" aria-label={`Edit ${t.name}`} onClick={() => openForm(t)}>Edit</button>}
              {writer && <button type="button" class="btn btn-xs btn-danger" aria-label={`Delete ${t.name}`} onClick={() => setDeleteTarget(t)}>Delete</button>}
            </div>
          </div>
        ))}
        {teams.length === 0 && <p class="text-muted">No teams yet. Teams group members for on-call rotations and escalation targeting.</p>}
      </div>

      <Modal open={showModal} onClose={() => setShowModal(false)} title={editId ? 'Edit team' : 'Add team'}>
        <form onSubmit={save} noValidate>
          {formError && (
            <div class="form-error" role="alert" id="team-form-error">
              {formError.text}
              {formError.upgrade && <> <UpgradeLink fallback="Ask an owner or admin to upgrade.">See plans</UpgradeLink></>}
            </div>
          )}
          <div class="form-group">
            <label for="team-name">Name</label>
            <input id="team-name" type="text" value={form.name} maxLength={200} required
                   onInput={e => setForm({ ...form, name: e.target.value })}
                   aria-invalid={formError ? 'true' : undefined}
                   aria-describedby={formError ? 'team-form-error' : undefined} />
          </div>
          <div class="btn-group modal-actions">
            <button type="submit" class="btn btn-primary" disabled={saving} aria-busy={saving ? 'true' : undefined}>
              {saving ? 'Saving...' : 'Save'}
            </button>
            <button type="button" class="btn" onClick={() => setShowModal(false)} disabled={saving}>Cancel</button>
          </div>
        </form>
      </Modal>

      <Modal open={!!deleteTarget} onClose={() => setDeleteTarget(null)} title="Delete team">
        <p style="margin-bottom: 1rem;">
          Delete <strong>{deleteTarget?.name}</strong> and its member list? Check schedules and escalation policies that use this team.
        </p>
        <div class="btn-group modal-actions">
          <button type="button" class="btn btn-danger" onClick={confirmDelete} disabled={saving} aria-busy={saving ? 'true' : undefined}>
            {saving ? 'Deleting...' : 'Delete team'}
          </button>
          <button type="button" class="btn" onClick={() => setDeleteTarget(null)} disabled={saving}>Cancel</button>
        </div>
      </Modal>

      <Modal open={!!membersTeam} onClose={closeMembers} title={membersTeam ? `${membersTeam.name} members` : 'Team members'}>
        <h4 id="team-members-title" class="sr-only" tabIndex={-1}>Current members</h4>
        {membersLoading && members.length === 0 ? (
          <p class="text-muted" aria-live="polite">Loading members...</p>
        ) : membersError ? (
          <ErrorMessage error={membersError} onRetry={() => loadMembers(membersTeam.id)} />
        ) : members.length > 0 ? (
          <table class="data-table" style="width:100%; margin-bottom: 16px;">
            <caption class="sr-only">Members of {membersTeam?.name}</caption>
            <thead>
              <tr><th scope="col">Member</th><th scope="col">Position</th>{writer && <th scope="col"><span class="sr-only">Actions</span></th>}</tr>
            </thead>
            <tbody>
              {members.map(m => (
                <tr key={m.user_id}>
                  <td><strong>{teamMemberLabel(m)}</strong></td>
                  <td>{m.position}</td>
                  {writer && (
                    <td>
                      {removeTarget?.user_id === m.user_id ? (
                        <span class="btn-group">
                          <button type="button" class="btn btn-xs btn-danger" onClick={confirmRemoveMember} disabled={memberBusy}
                                  aria-label={`Confirm removing ${teamMemberLabel(m)}`} aria-busy={memberBusy ? 'true' : undefined}>
                            {memberBusy ? 'Removing...' : 'Confirm remove'}
                          </button>
                          <button type="button" class="btn btn-xs" id={`team-member-keep-${m.user_id}`} onClick={keepMember} disabled={memberBusy}
                                  aria-label={`Keep ${teamMemberLabel(m)}`}>Keep</button>
                        </span>
                      ) : (
                        <button type="button" class="btn btn-xs btn-danger" id={`team-member-remove-${m.user_id}`}
                                aria-label={`Remove ${teamMemberLabel(m)}`}
                                onClick={() => askRemove(m)} disabled={memberBusy}>Remove</button>
                      )}
                    </td>
                  )}
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p class="text-muted" style="margin-bottom: 16px;">{writer ? 'No members yet. Add one below.' : 'No members yet.'}</p>
        )}
        {writer && !membersError && membersTeam && (
          <div style="border-top: 1px solid var(--color-border); padding-top: 16px;">
            <h4 style="margin-bottom: 12px;">Add member</h4>
            <form onSubmit={addMember} noValidate>
              {memberError && <div class="form-error" role="alert" id="team-member-error">{memberError.text}</div>}
              <UserPicker id="team-member-user" label="Member" value={memberForm.user_id}
                          exclude={members.map(m => m.user_id)} required
                          invalid={memberError?.field === 'user'}
                          describedBy={memberError?.field === 'user' ? 'team-member-error' : undefined}
                          onChange={v => setMemberForm(f => ({ ...f, user_id: v }))} />
              <div class="form-group">
                <label for="team-member-position">Rotation position</label>
                <input id="team-member-position" type="number" min="0" step="1" inputMode="numeric" value={memberForm.position}
                       aria-invalid={memberError?.field === 'position' ? 'true' : undefined}
                       aria-describedby={memberError?.field === 'position' ? 'team-member-position-help team-member-error' : 'team-member-position-help'}
                       onInput={e => setMemberForm(f => ({ ...f, position: e.target.value }))} />
                <p id="team-member-position-help" class="form-help">Lower numbers are earlier in the on-call rotation.</p>
              </div>
              <button type="submit" class="btn btn-primary" disabled={memberBusy} aria-busy={memberBusy ? 'true' : undefined}>
                {memberBusy ? 'Adding...' : 'Add member'}
              </button>
            </form>
          </div>
        )}
      </Modal>
    </div>
  );
}
