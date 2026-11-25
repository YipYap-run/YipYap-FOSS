// Pure helpers for the Organization tab and the Security tab's org MFA
// section (org-7, org-8, decision core-billing-org#7). Kept free of Preact so
// node --test can cover them.

export const SLUG_CHANGE_WARNING = 'Changing the slug changes your public status page URLs.';

export const MFA_GRACE_MIN = 0;
export const MFA_GRACE_MAX = 30;

const SLUG_RE = /^[a-z0-9]+(-[a-z0-9]+)*$/;
const RESERVED = new Set(['api', 'internal', 'assets', 'static']);

// normalizeSlug mirrors the server: trims and lowercases only.
export function normalizeSlug(raw) {
  return String(raw ?? '').trim().toLowerCase();
}

// slugError returns the message the server would give for this slug, or ''.
export function slugError(raw) {
  const s = normalizeSlug(raw);
  if (!s) return 'Slug cannot be empty.';
  if (s.length < 2 || s.length > 63) return 'Slug must be 2 to 63 characters.';
  if (!SLUG_RE.test(s)) return 'Use lowercase letters, numbers and single hyphens. It cannot start or end with a hyphen.';
  if (RESERVED.has(s)) return 'That slug is reserved.';
  return '';
}

// orgPatchBody builds the PATCH /org body. The slug is sent only when it
// changed, so saving a rename never touches public status page URLs.
export function orgPatchBody(form, org) {
  const body = { name: String(form.name ?? '').trim(), oncall_display: form.oncall_display };
  const slug = normalizeSlug(form.slug);
  if (slug !== (org?.slug || '')) body.slug = slug;
  return body;
}

// slugChanged reports whether saving this form needs the URL confirmation.
export function slugChanged(form, org) {
  return 'slug' in orgPatchBody(form, org);
}

// graceDaysError validates the grace input the same way the server does.
export function graceDaysError(raw) {
  const s = String(raw ?? '').trim();
  if (!/^\d+$/.test(s)) return `Enter a whole number of days from ${MFA_GRACE_MIN} to ${MFA_GRACE_MAX}.`;
  const n = Number(s);
  if (n < MFA_GRACE_MIN || n > MFA_GRACE_MAX) return `Enter a whole number of days from ${MFA_GRACE_MIN} to ${MFA_GRACE_MAX}.`;
  return '';
}

// mfaGapText describes how many active members have no second factor.
export function mfaGapText(status) {
  if (!status || typeof status.members_without_mfa !== 'number') return '';
  const n = status.members_without_mfa;
  const total = status.members_total;
  if (n === 0) return `All ${total} ${total === 1 ? 'member has' : 'members have'} MFA set up.`;
  return `${n} of ${total} ${total === 1 ? 'member has' : 'members have'} not set up MFA yet.`;
}

// orgMFASectionMode decides what the Security tab's org section shows:
// 'hidden' for members and viewers, 'upgrade' for admins on Free, 'manage'
// for admins on a paid plan.
export function orgMFASectionMode(role, paid) {
  if (role !== 'owner' && role !== 'admin') return 'hidden';
  return paid ? 'manage' : 'upgrade';
}
