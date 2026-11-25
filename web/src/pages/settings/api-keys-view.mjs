// Pure helpers for the API Keys tab and the member removal prompt
// (apikey-1, apikey-2, org-5, decision core-billing-org#6). Kept free of JSX
// so `npm test` can run them under node:test. The scope vocabulary itself
// comes from GET /org/api-keys/scopes; nothing here hardcodes resources.

export const PRESET_READ_ONLY = 'read-only';
export const PRESET_FULL_ACCESS = 'full-access';
export const MODE_CUSTOM = 'custom';

const DAY_MS = 86400000;

// Expiry choices offered by the create form. 'days' entries send now+N
// days, 'date' sends the end of the picked day, 'never' sends "never".
export const EXPIRY_CHOICES = [
  { value: '30', label: '30 days', days: 30 },
  { value: '90', label: '90 days', days: 90 },
  { value: '180', label: '180 days', days: 180 },
  { value: '365', label: '1 year', days: 365 },
  { value: 'date', label: 'Custom date' },
  { value: 'never', label: 'No expiry' },
];

export function defaultExpiryChoice(defaultDays = 90) {
  const hit = EXPIRY_CHOICES.find(c => c.days === defaultDays);
  return hit ? hit.value : '90';
}

// Custom resource grants are kept as { [resource]: 'read' | 'write' }.
// A missing entry means no access.

// toggleGrant applies a checkbox change. Write implies read, so checking
// write keeps read on, and unchecking read drops write too.
export function toggleGrant(grants, resource, action, checked) {
  const next = { ...grants };
  const cur = next[resource];
  if (action === 'write') {
    if (checked) next[resource] = 'write';
    else if (cur === 'write') next[resource] = 'read';
  } else if (action === 'read') {
    if (checked) { if (!cur) next[resource] = 'read'; }
    else delete next[resource];
  }
  return next;
}

// setAllGrants grants every resource the same level: 'read' gives read on
// all, 'write' gives the strongest action each resource supports, 'none'
// clears everything. Used by the "Select all" shortcuts.
export function setAllGrants(resources, level) {
  const out = {};
  if (level === 'none') return out;
  for (const r of resources || []) {
    const actions = r.actions || [];
    if (level === 'write' && actions.includes('write')) out[r.key] = 'write';
    else if (actions.includes('read')) out[r.key] = 'read';
  }
  return out;
}

// buildScopes turns the form state into the scopes array for create, in
// vocabulary order so the request is stable. Unknown resources and actions
// a resource does not support are dropped here; the server validates again.
export function buildScopes(mode, grants, resources) {
  if (mode === PRESET_READ_ONLY || mode === PRESET_FULL_ACCESS) return [mode];
  const out = [];
  for (const r of resources || []) {
    const level = grants?.[r.key];
    if (!level) continue;
    const actions = r.actions || [];
    if (level === 'write' && actions.includes('write')) out.push(`${r.key}:write`);
    else if (actions.includes('read')) out.push(`${r.key}:read`);
  }
  return out;
}

function endOfLocalDay(dateStr) {
  const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateStr || '');
  if (!m) return null;
  const d = new Date(Number(m[1]), Number(m[2]) - 1, Number(m[3]), 23, 59, 59);
  return isNaN(d.getTime()) ? null : d;
}

function ymd(d) {
  const p = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
}

// customDateBounds returns the min and max for the date input. The earliest
// is tomorrow (the server wants at least an hour). The latest is one day
// inside the server cap because the key stays valid through the end of the
// picked day.
export function customDateBounds(now = new Date(), maxDays = 730) {
  const min = new Date(now.getFullYear(), now.getMonth(), now.getDate() + 1);
  const max = new Date(now.getFullYear(), now.getMonth(), now.getDate() + Math.max(1, maxDays - 1));
  return { min: ymd(min), max: ymd(max) };
}

// expiryPayload returns { value } to send as expires_at, or { error } with
// a message for the form. value is an RFC 3339 string or "never".
export function expiryPayload(choice, customDate, now = new Date(), maxDays = 730) {
  if (choice === 'never') return { value: 'never' };
  if (choice === 'date') {
    const d = endOfLocalDay(customDate);
    if (!d) return { error: 'Pick an expiry date.' };
    const { min, max } = customDateBounds(now, maxDays);
    if (customDate < min) return { error: 'Pick a date after today.' };
    if (customDate > max) return { error: `Pick a date within ${maxDays - 1} days, or choose No expiry.` };
    return { value: d.toISOString() };
  }
  const c = EXPIRY_CHOICES.find(x => x.value === choice && x.days);
  if (!c) return { error: 'Choose when the key expires.' };
  return { value: new Date(now.getTime() + c.days * DAY_MS).toISOString() };
}

// expiryPreview is the sentence under the expiry picker.
export function expiryPreview(choice, customDate, now = new Date(), fmt = formatDate) {
  if (choice === 'never') return 'This key works until someone revokes it.';
  const p = expiryPayload(choice, customDate, now);
  if (p.error) return '';
  return `Stops working on ${fmt(p.value)}.`;
}

export function formatDate(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'short', day: 'numeric' });
}

// expiryStatus describes a listed key's expiry for the list: a label and a
// tone ('expired', 'soon' within 14 days, 'ok', or 'never').
export function expiryStatus(key, now = new Date(), fmt = formatDate) {
  if (!key?.expires_at) return { tone: 'never', label: 'No expiry' };
  const at = new Date(key.expires_at);
  const left = at.getTime() - now.getTime();
  if (key.expired || left <= 0) return { tone: 'expired', label: `Expired ${fmt(key.expires_at)}` };
  const days = Math.ceil(left / DAY_MS);
  if (days <= 14) {
    return { tone: 'soon', label: days <= 1 ? 'Expires within a day' : `Expires in ${days} days` };
  }
  return { tone: 'ok', label: `Expires ${fmt(key.expires_at)}` };
}

// scopeSummary is the access line shown for a key: a preset name, or the
// resources it can touch with read or read and write.
export function scopeSummary(key, resources) {
  const scopes = key?.scopes || [];
  if (key?.unrestricted || scopes.length === 0) return 'Full access (created before scopes)';
  if (scopes.includes(PRESET_FULL_ACCESS)) return 'Full access';
  const labels = {};
  for (const r of resources || []) labels[r.key] = r.label;
  const parts = [];
  let readOnly = false;
  for (const s of scopes) {
    if (s === PRESET_READ_ONLY || s === 'read') { readOnly = true; continue; }
    const [res, action] = s.split(':');
    const name = labels[res] || res;
    if (action === 'write' || !action) parts.push(`${name} (read and write)`);
    else parts.push(`${name} (read)`);
  }
  if (readOnly && parts.length === 0) return 'Read-only';
  if (readOnly) return `Read-only, plus ${parts.join(', ')}`;
  return parts.join(', ');
}

// creatorLabel names who created a key. Keys belong to the org and outlive
// the person who made them.
export function creatorLabel(key) {
  if (key?.creator_removed) return 'A removed member';
  if (key?.creator_name && key?.creator_email) return `${key.creator_name} (${key.creator_email})`;
  return key?.creator_email || key?.creator_name || 'Unknown';
}

export function keyCountText(n) {
  return n === 1 ? '1 key' : `${n} keys`;
}

// revokePrompt is the question in the member removal dialog.
export function revokePrompt(count) {
  if (!count) return '';
  return `This person created ${keyCountText(count)}. Revoke ${count === 1 ? 'it' : 'them'}?`;
}

// removeMemberPath builds the DELETE path. The flag is sent only when the
// person created keys, so a removal with nothing to revoke matches the old
// request exactly.
export function removeMemberPath(userId, keyCount, revoke) {
  const base = `/users/${encodeURIComponent(userId)}`;
  if (!keyCount) return base;
  return `${base}?revoke_api_keys=${revoke ? 'true' : 'false'}`;
}

// removeMemberResultText is the success toast for DELETE /users/{id}. The
// response is null (204) or { revoked_api_keys, billing_warning }.
export function removeMemberResultText(res, keyCount, revoke) {
  const n = res?.revoked_api_keys;
  if (typeof n === 'number') {
    return n > 0 ? `Member removed. ${keyCountText(n)} revoked.` : 'Member removed. No keys needed revoking.';
  }
  if (keyCount && !revoke) return `Member removed. Their ${keyCountText(keyCount)} still work.`;
  return 'Member removed';
}

// validateCreate checks the form before sending. Returns field errors keyed
// by field name, empty when the form can be submitted.
export function validateCreate({ name, mode, grants, resources, expiry, customDate, now = new Date(), maxDays = 730 }) {
  const errors = {};
  const n = (name || '').trim();
  if (!n) errors.name = 'Enter a name so you can tell this key apart later.';
  else if ([...n].length > 100) errors.name = 'Use 100 characters or fewer.';
  if (buildScopes(mode, grants, resources).length === 0) {
    errors.scopes = 'Choose at least one resource, or pick Read-only or Full access.';
  }
  const exp = expiryPayload(expiry, customDate, now, maxDays);
  if (exp.error) errors.expiry = exp.error;
  return errors;
}

// keyLabel names a key in prompts: its name plus the prefix when the server
// has one (keys made before prefixes were stored have none).
export function keyLabel(key) {
  const name = key?.name || 'Unnamed key';
  return key?.prefix ? `${name} (${key.prefix}...)` : name;
}

// revokeConsequence is the sentence after "Revoke <key>?" in the revoke
// dialog. An expired key already fails, so revoking only cleans it up.
export function revokeConsequence(key, now = new Date()) {
  if (expiryStatus(key, now).tone === 'expired') {
    return 'It has already expired, so nothing using it works now. Revoking removes it from this list.';
  }
  return 'Anything using it stops working right away. This cannot be undone.';
}

// shouldConfirmDiscard decides whether closing the new-key dialog needs a
// second step. The secret is shown once, so closing before a successful
// copy asks first. A confirmed close (force) always goes through.
export function shouldConfirmDiscard({ secret, copied, force = false }) {
  if (force) return false;
  return !!secret && !copied;
}
