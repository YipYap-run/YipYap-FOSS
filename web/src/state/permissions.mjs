// Role rules the SPA uses to show or hide controls. They mirror the server
// (internal/api/middleware/role_write.go and the route groups in server.go and
// routes_full.go); the server is the authority and still rejects anything the
// UI gets wrong.
//
// Viewer: read-only, may acknowledge alerts paged or routed to them.
// Member: everything except org settings, billing, API keys and SSO.
// Owner/Admin: everything.

const WRITER_ROLES = new Set(['owner', 'admin', 'member']);
const ADMIN_ROLES = new Set(['owner', 'admin']);

// Settings tabs only owners and admins see. Their endpoints are owner/admin
// only on the server, so showing them to others would only lead to 403s.
export const ADMIN_ONLY_SETTINGS_TABS = ['org', 'billing', 'api-keys', 'sso', 'integrations', 'support'];

// Settings tabs that exist only to change things; hidden from viewers.
export const WRITER_ONLY_SETTINGS_TABS = ['import'];

// Timeline events that name the user an alert was sent or routed to. Keep in
// sync with pagedEventTypes in internal/api/handlers/alerts_ack_access.go.
const PAGED_EVENT_TYPES = new Set(['notified', 'escalated', 'ownership_transferred', 'routed']);

export function canWrite(role) {
  return WRITER_ROLES.has(role);
}

export function isOrgAdmin(role) {
  return ADMIN_ROLES.has(role);
}

export function canSeeSettingsTab(role, key) {
  if (ADMIN_ONLY_SETTINGS_TABS.includes(key)) return isOrgAdmin(role);
  if (WRITER_ONLY_SETTINGS_TABS.includes(key)) return canWrite(role);
  return true;
}

// alertPagedToUser reports whether the alert timeline shows it was paged or
// routed to userId.
export function alertPagedToUser(events, userId) {
  if (!userId || !Array.isArray(events)) return false;
  return events.some(e => e && e.target_user_id === userId && PAGED_EVENT_TYPES.has(e.event_type));
}

// canAckAlert: writers may ack any alert; viewers only alerts paged to them.
export function canAckAlert(role, userId, events) {
  if (canWrite(role)) return true;
  if (role === 'viewer') return alertPagedToUser(events, userId);
  return false;
}

// settingsTabDeniedNote is the note shown when a deep link names a settings
// tab this role cannot open, or null when the role can open it.
export function settingsTabDeniedNote(role, key, label) {
  if (canSeeSettingsTab(role, key)) return null;
  if (ADMIN_ONLY_SETTINGS_TABS.includes(key)) return `Only owners and admins can manage ${label}.`;
  return `${label} is not available to viewers.`;
}

// Role names and what each can do, for the Members tab and invite form.
export const ROLE_OPTIONS = [
  { value: 'owner', label: 'Owner', description: 'Everything, including billing and transferring ownership.' },
  { value: 'admin', label: 'Admin', description: 'Everything except transferring ownership.' },
  { value: 'member', label: 'Member', description: 'Manage monitors, alerts, schedules and channels. No org settings, billing, API keys or SSO.' },
  { value: 'viewer', label: 'Viewer', description: 'Read-only. Can acknowledge alerts paged to them.' },
];

export function roleLabel(role) {
  const opt = ROLE_OPTIONS.find(r => r.value === role);
  return opt ? opt.label : (role || '');
}

// assignableRoles lists the roles actorRole may give another user. It mirrors
// UserHandler.Create and Update: only the owner grants admin, and owner moves
// through the ownership transfer flow, never a role change.
export function assignableRoles(actorRole) {
  if (actorRole === 'owner') return ROLE_OPTIONS.filter(r => r.value !== 'owner');
  if (actorRole === 'admin') return ROLE_OPTIONS.filter(r => r.value === 'member' || r.value === 'viewer');
  return [];
}

// roleSelectOptions is assignableRoles plus the user's current role (disabled)
// when the actor cannot grant it, so the select shows the real value.
export function roleSelectOptions(actorRole, currentRole) {
  const opts = assignableRoles(actorRole);
  if (opts.some(r => r.value === currentRole)) return opts;
  const cur = ROLE_OPTIONS.find(r => r.value === currentRole);
  return cur ? [{ ...cur, disabled: true }, ...opts] : opts;
}

// viewOnlyNote is the short explanation shown where write controls are hidden
// from viewers, or null for roles that can write.
export function viewOnlyNote(role) {
  if (canWrite(role)) return null;
  return 'You have view-only access. Ask an owner or admin if you need to make changes.';
}

// Settings tabs whose write controls are hidden from viewers.
const VIEWER_READ_ONLY_SETTINGS_TABS = ['channels', 'escalation', 'teams', 'monitor-states', 'maintenance', 'status-page'];

// settingsTabReadOnlyNote explains why a visible settings tab has no write
// controls for this role, or null when the role can change it.
export function settingsTabReadOnlyNote(role, key) {
  if (key === 'members' && !isOrgAdmin(role)) {
    return 'Only owners and admins can invite members or change roles.';
  }
  if (VIEWER_READ_ONLY_SETTINGS_TABS.includes(key)) return viewOnlyNote(role);
  return null;
}
