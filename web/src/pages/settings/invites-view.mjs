// Pure helpers for member invites and the accept page (org-6, decision
// core-billing-org#1). Kept free of JSX so `npm test` can run them under
// node:test. They follow the contract of internal/api/handlers/user_invites.go.

import { hasSubscription, formatCents } from './billing-view.mjs';

export const FREE_MAX_MEMBERS = 5;
export const PASSWORD_MIN = 8;
// bcrypt rejects input over 72 bytes; the server enforces the same bound.
export const PASSWORD_MAX_BYTES = 72;
export const NAME_MAX = 200;

const DAY_MS = 86400000;

// sentence capitalizes a server message ("this invite has expired. ...") so
// it reads as UI copy, and adds a final period when missing.
export function sentence(msg) {
  const s = String(msg || '').trim();
  if (!s) return '';
  const out = s[0].toUpperCase() + s.slice(1);
  return /[.!?]$/.test(out) ? out : out + '.';
}

// inviteRequestBody builds the POST /users/invites body. Only email and
// role are sent; a password field is never part of an invite.
export function inviteRequestBody(form) {
  return { email: String(form?.email || '').trim(), role: form?.role || 'member' };
}

// inviteDeliveryView describes the result of a create or resend for the
// dialog: whether the email went out, the link to share when SMTP is not
// configured, and any warning.
export function inviteDeliveryView(res, verb = 'sent') {
  const email = res?.invite?.email || 'the invitee';
  const expires = res?.invite?.expires_at || null;
  if (res?.email_sent) {
    return {
      kind: 'emailed',
      title: verb === 'resent' ? 'Invite resent' : 'Invite sent',
      message: `We emailed an invite link to ${email}. It expires in 7 days.`,
      acceptURL: '',
      warning: '',
      expires,
    };
  }
  if (res?.accept_url) {
    return {
      kind: 'link',
      title: verb === 'resent' ? 'New invite link' : 'Invite created',
      message: `Email is not set up on this server, so no email was sent. Send this link to ${email} yourself. It works once and expires in 7 days.`,
      acceptURL: res.accept_url,
      warning: res.warning ? sentence(res.warning) : '',
      expires,
    };
  }
  return {
    kind: 'failed',
    title: 'Invite saved, email not sent',
    message: sentence(res?.warning) || `The invite for ${email} was saved, but the email could not be sent. Try Resend in a few minutes.`,
    acceptURL: '',
    warning: '',
    expires,
  };
}

// roleWithArticle phrases a role for the accept page, matching the invite
// email ("as an admin", "as a viewer").
export function roleWithArticle(role) {
  if (role === 'admin') return 'an admin';
  if (role === 'viewer') return 'a viewer, with read-only access';
  return 'a member';
}

// inviteStatus labels a pending invite row. Expired invites stay in the list
// so they can be resent.
export function inviteStatus(invite, now = Date.now()) {
  const exp = invite?.expires_at ? new Date(invite.expires_at).getTime() : NaN;
  if (invite?.expired || (!isNaN(exp) && exp <= now)) {
    return { expired: true, label: 'Expired', tone: 'danger' };
  }
  if (isNaN(exp)) return { expired: false, label: 'Pending', tone: 'muted' };
  const days = Math.ceil((exp - now) / DAY_MS);
  if (days <= 1) return { expired: false, label: 'Expires within a day', tone: 'warning' };
  return { expired: false, label: `Expires in ${days} days`, tone: 'muted' };
}

// canResendInvite mirrors ResendInvite: only the owner resends an admin
// invite. Owners and admins resend the rest.
export function canResendInvite(actorRole, invite) {
  if (actorRole !== 'owner' && actorRole !== 'admin') return false;
  if (invite?.role === 'admin') return actorRole === 'owner';
  return true;
}

// freeSeatUse counts members plus unexpired pending invites against the Free
// limit, as CreateInvite does. Expired invites do not hold a seat.
export function freeSeatUse(memberCount, invites, now = Date.now()) {
  const pending = (invites || []).filter(i => !inviteStatus(i, now).expired).length;
  const used = (memberCount || 0) + pending;
  return { used, pending, full: used >= FREE_MAX_MEMBERS };
}

// inviteTokenFromSearch reads ?token= from a location.search string.
export function inviteTokenFromSearch(search) {
  try {
    const t = new URLSearchParams(search || '').get('token');
    return t && t.length <= 256 ? t : '';
  } catch (_) {
    return '';
  }
}

function utf8Length(s) {
  return new TextEncoder().encode(s).length;
}

// checkAcceptForm validates the accept form before it is sent. The server
// checks the same bounds; this gives the message next to the field.
// It returns { field, message } or null.
export function checkAcceptForm({ name, password, confirm }) {
  if ((name || '').trim().length > NAME_MAX) {
    return { field: 'name', message: `Name must be at most ${NAME_MAX} characters.` };
  }
  if ((password || '').length < PASSWORD_MIN) {
    return { field: 'password', message: `Password must be at least ${PASSWORD_MIN} characters.` };
  }
  if (utf8Length(password) > PASSWORD_MAX_BYTES) {
    return { field: 'password', message: 'Password is too long. Use at most 72 characters (fewer if it has accented letters or emoji).' };
  }
  if (password !== confirm) {
    return { field: 'confirm', message: 'Passwords do not match.' };
  }
  return null;
}

// acceptErrorView maps an accept error (GET or POST) to what the page shows.
// kind 'fatal' replaces the form (the link cannot be used), 'form' keeps the
// form and shows the message above it.
export function acceptErrorView(err) {
  const status = err?.status;
  const code = err?.body?.code;
  const msg = sentence(err?.body?.error || err?.message);
  if (status === 409 && code === 'account_exists') {
    return {
      kind: 'fatal',
      title: 'You already have an account',
      message: 'An account with this email already exists, and an account belongs to one organization. Sign in with it, or ask the admin to invite a different email.',
      action: 'login',
    };
  }
  if (status === 404) {
    return {
      kind: 'fatal',
      title: 'Invite link not valid',
      message: 'This link is not valid. Check that you copied the whole link, or ask an admin of the organization for a new invite.',
      action: 'none',
    };
  }
  if (status === 410 || status === 409) {
    const used = /already used/i.test(msg);
    return {
      kind: 'fatal',
      title: used ? 'Invite already used' : 'Invite no longer valid',
      message: msg || 'This invite can no longer be used. Ask an admin of the organization for a new one.',
      action: used ? 'login' : 'none',
    };
  }
  if (status === 403) {
    return { kind: 'form', title: 'Member limit reached', message: msg || 'This organization has reached its member limit. Ask an admin to upgrade the plan, then try again.' };
  }
  if (status === 429) {
    return { kind: 'form', title: 'Too many attempts', message: 'Too many attempts. Wait a minute and try again.' };
  }
  if (status === 400) {
    return { kind: 'form', title: 'Check the form', message: msg || 'Check the form and try again.' };
  }
  return { kind: 'form', title: 'Something went wrong', message: msg || 'Could not reach the server. Check your connection and try again.' };
}

// inviteSeatLine is the seat cost line in the invite dialog. A seat is
// billed when the invitee accepts, and only while the org has a live Pro
// subscription (billing.SeatManager.Sync skips everything else). billing is
// the GET /api/v1/billing response; '' when no seat would be billed.
export function inviteSeatLine(billingEnabled, billing) {
  if (!billingEnabled || !billing) return '';
  if (billing.plan !== 'pro' && billing.plan !== 'enterprise') return '';
  if (!hasSubscription(billing)) return '';
  const price = formatCents(billing.seat_price_cents, billing.seat_currency);
  return price
    ? `Adds 1 seat (${price}/mo, prorated) when they accept.`
    : 'Adds 1 seat when they accept.';
}

// inviteSentToast is the toast after an emailed invite.
export function inviteSentToast(email) {
  return `Invite sent to ${email}.`;
}

// acceptResult reads the POST /invites/accept response. signedIn is true
// when the server started a session (token and user are set); otherwise
// the page sends the invitee to sign in.
export function acceptResult(res) {
  const signedIn = !!(res?.signed_in && res?.token && res?.user);
  return {
    signedIn,
    token: signedIn ? res.token : '',
    user: signedIn ? res.user : null,
    email: res?.user?.email || res?.email || '',
  };
}

// PASSWORD_RULE states the password rules on the accept page.
export const PASSWORD_RULE = `At least ${PASSWORD_MIN} characters, up to ${PASSWORD_MAX_BYTES}. A passphrase of a few words works well.`;

// inviteExpiryDate is the exact expiry shown next to the status badge on a
// pending invite row, so it does not live only in a hover tooltip (which
// keyboard and touch users cannot reach). '' when unknown.
export function inviteExpiryDate(invite) {
  const exp = invite?.expires_at ? new Date(invite.expires_at) : null;
  if (!exp || isNaN(exp.getTime())) return '';
  return exp.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

// roleChangedToast confirms a role change by naming the person and the role.
export function roleChangedToast(email, roleLabelText) {
  return `${email} is now ${roleLabelText === 'Admin' ? 'an' : 'a'} ${roleLabelText}.`;
}

// canManageMember reports whether actorRole gets the role select and the
// row actions for member. It mirrors UserHandler.Update and Delete: owners
// and admins manage everyone except themselves and the owner (ownership
// moves through Transfer ownership).
export function canManageMember(actorRole, actorId, member) {
  if (!member || member.id === actorId || member.role === 'owner') return false;
  return actorRole === 'owner' || actorRole === 'admin';
}
