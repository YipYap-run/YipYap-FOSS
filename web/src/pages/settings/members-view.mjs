// Pure helpers for the Members and Teams tabs (ux-3, org-3, ux-2). Kept free
// of JSX so `npm test` can run them under node:test. They follow the
// contract of UserHandler.List and TransferOwnership in
// internal/api/handlers/users.go.

export const MEMBERS_PAGE_SIZE = 50;
// Pickers show a short list of matches; the person types to narrow it.
export const PICKER_LIMIT = 20;
// The server caps a batch ID lookup at 100 IDs.
export const IDS_MAX = 100;

// usersQuery builds the GET /users path. Empty values are left out.
export function usersQuery({ q = '', role = '', limit, offset, ids } = {}) {
  const p = new URLSearchParams();
  const term = String(q || '').trim();
  if (term) p.set('q', term.slice(0, 200));
  if (role) p.set('role', role);
  if (ids) {
    p.set('ids', [...new Set(ids.filter(Boolean))].slice(0, IDS_MAX).join(','));
  } else {
    if (limit) p.set('limit', String(limit));
    if (offset) p.set('offset', String(offset));
  }
  const s = p.toString();
  return s ? `/users?${s}` : '/users';
}

// userPage reads a GET /users response. It accepts the old bare array too,
// so a stale server during a rolling deploy still renders.
export function userPage(data) {
  if (Array.isArray(data)) return { users: data, total: data.length };
  const users = Array.isArray(data?.users) ? data.users : [];
  const total = Number.isFinite(data?.total) ? data.total : users.length;
  return { users, total };
}

// pageSummary is the "51-100 of 230 members" line.
export function pageSummary(offset, shown, total, noun = 'members') {
  if (!total) return `0 ${noun}`;
  if (shown >= total && offset === 0) return `${total} ${total === 1 ? noun.replace(/s$/, '') : noun}`;
  return `${offset + 1}-${offset + shown} of ${total} ${noun}`;
}

// userLabel names a person for a list row: email, plus the name when set.
export function userLabel(u) {
  if (!u) return '';
  return u.name && u.name !== u.email ? `${u.name} (${u.email})` : (u.email || '');
}

// TRANSFER_CONSEQUENCE is the warning the Transfer Ownership modal shows
// (org-3).
export const TRANSFER_CONSEQUENCE = 'You will become an admin. This cannot be undone without the new owner.';

// transferRequestBody builds the POST /org/transfer-ownership body.
export function transferRequestBody(form) {
  const body = {
    user_id: String(form?.userId || ''),
    current_password: String(form?.password || ''),
  };
  const code = String(form?.mfaCode || '').trim();
  if (code) body.mfa_code = code;
  return body;
}

// transferErrorField maps a failed transfer to the field to mark invalid and
// focus: 'password', 'mfa' or 'target'. Unknown errors return ''.
export function transferErrorField(err) {
  const code = err?.body?.code;
  if (code === 'invalid_password') return 'password';
  if (code === 'mfa_required' || code === 'mfa_invalid') return 'mfa';
  if (err?.status === 404 || err?.status === 400) return 'target';
  return '';
}

// teamMemberLabel names a team member row from the GET /teams/{id} member
// fields (email, name), falling back to a looked-up user, never a raw ID.
export function teamMemberLabel(member, usersById) {
  const u = member?.email ? member : usersById?.[member?.user_id];
  if (u?.email) return userLabel(u);
  return 'Former member';
}

// pageNav is the pager label for a paged list: "Page 2 of 5".
export function pageNav(offset, total, size = MEMBERS_PAGE_SIZE) {
  const pages = Math.max(1, Math.ceil((total || 0) / size));
  const page = Math.min(pages, Math.floor((offset || 0) / size) + 1);
  return { page, pages, label: `Page ${page} of ${pages}` };
}

// pickerOptions lists the people a UserPicker offers: the search matches
// minus excluded IDs, with the chosen person kept at the top even when a
// new search no longer matches them, so narrowing the search never drops
// a choice the person already made.
export function pickerOptions(matches, exclude = [], selected = null) {
  const skip = new Set(exclude);
  const list = (matches || []).filter(u => u && !skip.has(u.id));
  if (selected?.id && !skip.has(selected.id) && !list.some(u => u.id === selected.id)) {
    return [selected, ...list];
  }
  return list;
}

// teamSaveError reads a failed team create or edit. A plan-limit 403 carries
// upgrade_required, which the form turns into an upgrade link.
export function teamSaveError(err) {
  const upgrade = err?.status === 403 && !!err?.body?.upgrade_required;
  const text = String(err?.body?.error || err?.message || '').trim();
  return { upgrade, text: text || (upgrade ? 'Your plan does not allow more teams.' : 'Could not save the team.') };
}

// parsePosition validates a rotation position: a whole number from 0 up.
// It returns null for anything else.
export function parsePosition(value) {
  const s = String(value ?? '').trim();
  if (!/^\d{1,6}$/.test(s)) return null;
  return Number(s);
}
