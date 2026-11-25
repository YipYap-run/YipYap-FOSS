// Client mirror of domain.NormalizeChannelPermissions, used only to show
// the form state. The server validates and returns the stored result.

export const ROLE_ORDER = ['owner', 'admin', 'member', 'viewer'];

export const ROLE_LABELS = { owner: 'Owner', admin: 'Admin', member: 'Member', viewer: 'Viewer' };

// normalizePermissions returns { manage_roles, test_roles } with the owner
// in both lists, every manager able to test, viewers never managing, and
// roles in canonical order.
export function normalizePermissions({ manage_roles = [], test_roles = [] } = {}) {
  const manage = new Set(manage_roles.filter(r => r !== 'viewer' && ROLE_ORDER.includes(r)));
  manage.add('owner');
  const test = new Set(test_roles.filter(r => ROLE_ORDER.includes(r)));
  for (const r of manage) test.add(r);
  return {
    manage_roles: ROLE_ORDER.filter(r => manage.has(r)),
    test_roles: ROLE_ORDER.filter(r => test.has(r)),
  };
}

// toggleRole flips role in the given list and returns the normalized result.
export function toggleRole(perms, list, role) {
  const key = list === 'manage' ? 'manage_roles' : 'test_roles';
  const cur = new Set(perms[key] || []);
  if (cur.has(role)) cur.delete(role); else cur.add(role);
  const next = { ...perms, [key]: [...cur] };
  // Dropping a manager does not silently drop their test right, but adding
  // a manager always grants test.
  return normalizePermissions(next);
}

// lockReason explains why a checkbox cannot be changed, or returns '' when
// it can.
export function lockReason(perms, list, role) {
  if (role === 'owner') return 'Owners always have access.';
  if (list === 'manage' && role === 'viewer') return 'Viewers cannot change channels.';
  if (list === 'test' && (perms.manage_roles || []).includes(role)) return 'Included because this role can manage channels.';
  return '';
}

const PLURAL = { owner: 'owners', admin: 'admins', member: 'members', viewer: 'viewers' };

// describeRoles turns ['owner', 'admin'] into "owners and admins".
export function describeRoles(roles = []) {
  const names = ROLE_ORDER.filter(r => roles.includes(r)).map(r => PLURAL[r]);
  if (names.length === 0) return '';
  if (names.length === 1) return names[0];
  return names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1];
}


// channelAccessNote is the short line shown on the Channels tab when the
// caller cannot do everything. It names who can, from the API response, so
// it stays right when an org changes the defaults. Returns '' when the
// caller has full access or permissions have not loaded.
export function channelAccessNote(p) {
  if (!p || p.can_manage) return '';
  const managers = describeRoles(p.manage_roles);
  const who = managers ? 'Only ' + managers + ' can' : 'Your role cannot';
  if (p.can_test) return who + ' add or change channels. You can send test notifications.';
  const testers = describeRoles(p.test_roles);
  return who + ' add or change channels' + (testers ? ', and only ' + testers + ' can send tests.' : '.');
}

// samePermissions compares two permission sets after normalizing.
export function samePermissions(a, b) {
  const x = normalizePermissions(a);
  const y = normalizePermissions(b);
  return x.manage_roles.join() === y.manage_roles.join() && x.test_roles.join() === y.test_roles.join();
}

// selfLockoutWarning warns an admin who is about to remove their own role
// from the manage list, and says so when the test right goes too. Owners
// are always included, so they never see it.
export function selfLockoutWarning(perms, role) {
  if (!role || role === 'owner' || !PLURAL[role]) return '';
  if (perms.manage_roles.includes(role)) return '';
  const what = perms.test_roles.includes(role)
    ? 'add or change channels'
    : 'add or change channels or send test notifications';
  return 'After you save, ' + PLURAL[role] + ' (including you) will no longer be able to ' + what + '. You can still edit these settings.';
}

// firstEnabledCheckbox returns the first checkbox in root a keyboard user
// can act on, or null. Used to keep focus inside the form when the button
// that had it disappears or is disabled.
export function firstEnabledCheckbox(root) {
  if (!root || typeof root.querySelectorAll !== 'function') return null;
  for (const el of root.querySelectorAll('input[type="checkbox"]')) {
    if (!el.disabled) return el;
  }
  return null;
}

