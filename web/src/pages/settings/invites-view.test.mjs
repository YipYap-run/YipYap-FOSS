import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  sentence, inviteRequestBody, inviteDeliveryView, inviteStatus, canResendInvite,
  freeSeatUse, inviteTokenFromSearch, roleWithArticle, checkAcceptForm, acceptErrorView,
  inviteSeatLine, inviteSentToast, acceptResult, PASSWORD_RULE,
  inviteExpiryDate, roleChangedToast, canManageMember,
} from './invites-view.mjs';

const DAY = 86400000;
const NOW = Date.UTC(2026, 10, 24, 12, 0, 0);

test('sentence capitalizes server messages and ends them with a period', () => {
  assert.equal(sentence('this invite has expired. Ask an admin to resend it'), 'This invite has expired. Ask an admin to resend it.');
  assert.equal(sentence('Done.'), 'Done.');
  assert.equal(sentence(''), '');
  assert.equal(sentence(undefined), '');
});

test('invite body sends only email and role, never a password', () => {
  const body = inviteRequestBody({ email: '  a@example.com ', role: 'viewer', password: 'x' });
  assert.deepEqual(body, { email: 'a@example.com', role: 'viewer' });
  assert.deepEqual(inviteRequestBody({ email: 'b@example.com' }), { email: 'b@example.com', role: 'member' });
});

test('delivery view: emailed invite shows no link', () => {
  const v = inviteDeliveryView({ invite: { email: 'a@example.com' }, email_sent: true, accept_url: 'ignored' });
  assert.equal(v.kind, 'emailed');
  assert.equal(v.title, 'Invite sent');
  assert.equal(v.acceptURL, '');
  assert.match(v.message, /a@example\.com/);
  assert.equal(inviteDeliveryView({ invite: {}, email_sent: true }, 'resent').title, 'Invite resent');
});

test('delivery view: no SMTP returns the link to share', () => {
  const url = 'https://yip.example/invite/accept?token=id.secret';
  const v = inviteDeliveryView({ invite: { email: 'a@example.com' }, email_sent: false, accept_url: url });
  assert.equal(v.kind, 'link');
  assert.equal(v.acceptURL, url);
  assert.match(v.message, /no email was sent/);
  assert.equal(inviteDeliveryView({ invite: {}, accept_url: url }, 'resent').title, 'New invite link');
});

test('delivery view: SMTP failure shows the server warning', () => {
  const v = inviteDeliveryView({ invite: { email: 'a@example.com' }, email_sent: false, warning: 'The invite was saved, but the email could not be sent. Try Resend in a few minutes.' });
  assert.equal(v.kind, 'failed');
  assert.match(v.message, /could not be sent/);
  assert.equal(v.acceptURL, '');
});

test('invite status: expired flag, past expiry, last day, days left', () => {
  assert.equal(inviteStatus({ expired: true, expires_at: new Date(NOW + 3 * DAY).toISOString() }, NOW).label, 'Expired');
  assert.equal(inviteStatus({ expires_at: new Date(NOW - 1000).toISOString() }, NOW).expired, true);
  assert.equal(inviteStatus({ expires_at: new Date(NOW + 3600000).toISOString() }, NOW).label, 'Expires within a day');
  assert.equal(inviteStatus({ expires_at: new Date(NOW + 7 * DAY).toISOString() }, NOW).label, 'Expires in 7 days');
  assert.equal(inviteStatus({ expires_at: new Date(NOW + 2.5 * DAY).toISOString() }, NOW).label, 'Expires in 3 days');
});

test('resend rule matches the server: only the owner resends admin invites', () => {
  assert.equal(canResendInvite('owner', { role: 'admin' }), true);
  assert.equal(canResendInvite('admin', { role: 'admin' }), false);
  assert.equal(canResendInvite('admin', { role: 'member' }), true);
  assert.equal(canResendInvite('admin', { role: 'viewer' }), true);
  assert.equal(canResendInvite('member', { role: 'member' }), false);
});

test('Free seat use counts unexpired pending invites, like CreateInvite', () => {
  const live = { expires_at: new Date(NOW + DAY * 2).toISOString() };
  const dead = { expires_at: new Date(NOW - DAY).toISOString(), expired: true };
  assert.deepEqual(freeSeatUse(3, [live, dead], NOW), { used: 4, pending: 1, full: false });
  assert.equal(freeSeatUse(4, [live], NOW).full, true);
  assert.equal(freeSeatUse(5, [], NOW).full, true);
  assert.equal(freeSeatUse(4, [dead], NOW).full, false);
});

test('token is read from the query string and bounded', () => {
  assert.equal(inviteTokenFromSearch('?token=abc.def'), 'abc.def');
  assert.equal(inviteTokenFromSearch(''), '');
  assert.equal(inviteTokenFromSearch('?token=' + 'x'.repeat(300)), '');
});

test('accept form checks match the server bounds', () => {
  assert.equal(checkAcceptForm({ name: '', password: 'short', confirm: 'short' }).field, 'password');
  assert.equal(checkAcceptForm({ name: 'x'.repeat(201), password: 'longenough', confirm: 'longenough' }).field, 'name');
  assert.equal(checkAcceptForm({ name: '', password: 'longenough', confirm: 'different!' }).field, 'confirm');
  // 25 three-byte characters are 75 bytes, over bcrypt's 72.
  const wide = '€'.repeat(25);
  assert.equal(checkAcceptForm({ name: '', password: wide, confirm: wide }).field, 'password');
  assert.equal(checkAcceptForm({ name: 'Ann', password: 'longenough', confirm: 'longenough' }), null);
});

function apiErr(status, body) {
  const e = new Error(body?.error || `HTTP ${status}`);
  e.status = status;
  e.body = body || {};
  return e;
}

test('accept errors map to the page states the backend contract defines', () => {
  const exists = acceptErrorView(apiErr(409, { code: 'account_exists', error: 'an account with this email already exists' }));
  assert.equal(exists.kind, 'fatal');
  assert.equal(exists.action, 'login');

  const invalid = acceptErrorView(apiErr(404, { error: 'this invite link is not valid' }));
  assert.equal(invalid.kind, 'fatal');
  assert.equal(invalid.title, 'Invite link not valid');

  const expired = acceptErrorView(apiErr(410, { error: 'this invite has expired. Ask an admin of the organization to resend it' }));
  assert.equal(expired.kind, 'fatal');
  assert.match(expired.message, /^This invite has expired/);
  assert.equal(expired.action, 'none');

  const used = acceptErrorView(apiErr(410, { error: 'this invite was already used. Sign in instead' }));
  assert.equal(used.title, 'Invite already used');
  assert.equal(used.action, 'login');

  // A lost race on the same token (conditional MarkAccepted) is a 409 with no code.
  assert.equal(acceptErrorView(apiErr(409, { error: 'this invite is no longer valid' })).kind, 'fatal');

  const limit = acceptErrorView(apiErr(403, { error: 'this organization has reached its member limit' }));
  assert.equal(limit.kind, 'form');
  assert.equal(limit.title, 'Member limit reached');

  assert.equal(acceptErrorView(apiErr(400, { error: 'password must be at least 8 characters' })).message, 'Password must be at least 8 characters.');
  assert.equal(acceptErrorView(apiErr(429, {})).kind, 'form');
  assert.equal(acceptErrorView(new Error('Failed to fetch')).kind, 'form');
});

test('role phrase for the accept page', () => {
  assert.equal(roleWithArticle('admin'), 'an admin');
  assert.equal(roleWithArticle('member'), 'a member');
  assert.match(roleWithArticle('viewer'), /^a viewer/);
});

test('seat line shows only for a live paid subscription', () => {
  const pro = { plan: 'pro', status: 'active', seat_price_cents: 800, seat_currency: 'usd' };
  assert.equal(inviteSeatLine(true, pro), 'Adds 1 seat ($8/mo, prorated) when they accept.');
  assert.equal(inviteSeatLine(true, { ...pro, status: 'trialing', plan: 'enterprise' }), 'Adds 1 seat ($8/mo, prorated) when they accept.');
  assert.equal(inviteSeatLine(true, { ...pro, seat_price_cents: 0 }), 'Adds 1 seat when they accept.');
  assert.equal(inviteSeatLine(false, pro), '');
  assert.equal(inviteSeatLine(true, null), '');
  assert.equal(inviteSeatLine(true, { ...pro, plan: 'free' }), '');
  assert.equal(inviteSeatLine(true, { ...pro, plan: 'promo' }), '');
  // Staff-managed paid plan (no billing row) or a canceled subscription bills no seat.
  assert.equal(inviteSeatLine(true, { ...pro, status: 'none' }), '');
  assert.equal(inviteSeatLine(true, { ...pro, status: 'canceled' }), '');
});

test('emailed invite toast names the email', () => {
  assert.equal(inviteSentToast('a@example.com'), 'Invite sent to a@example.com.');
});

test('accept result signs in only with a token and user', () => {
  const user = { id: 'u1', email: 'a@example.com', role: 'member' };
  assert.deepEqual(acceptResult({ status: 'accepted', email: 'a@example.com', signed_in: true, token: 't', user }),
    { signedIn: true, token: 't', user, email: 'a@example.com' });
  assert.deepEqual(acceptResult({ status: 'accepted', email: 'a@example.com', signed_in: false }),
    { signedIn: false, token: '', user: null, email: 'a@example.com' });
  assert.equal(acceptResult({ signed_in: true, email: 'a@example.com' }).signedIn, false);
  assert.equal(acceptResult(null).signedIn, false);
});

test('password rule states the bounds the server enforces', () => {
  assert.match(PASSWORD_RULE, /8 characters/);
  assert.match(PASSWORD_RULE, /72/);
});

test('pending invite rows show the exact expiry as text, not only a tooltip', () => {
  const iso = new Date(NOW + 3 * DAY).toISOString();
  const text = inviteExpiryDate({ expires_at: iso });
  assert.ok(text.length > 0);
  assert.match(text, /2026/);
  assert.equal(inviteExpiryDate({ expires_at: 'not a date' }), '');
  assert.equal(inviteExpiryDate({}), '');
  assert.equal(inviteExpiryDate(null), '');
});

test('role change toast names the person and the new role', () => {
  assert.equal(roleChangedToast('a@example.com', 'Viewer'), 'a@example.com is now a Viewer.');
  assert.equal(roleChangedToast('a@example.com', 'Admin'), 'a@example.com is now an Admin.');
  assert.equal(roleChangedToast('a@example.com', 'Member'), 'a@example.com is now a Member.');
});

test('member row controls follow UserHandler.Update and Delete', () => {
  const owner = { id: 'o', role: 'owner' };
  const admin = { id: 'a', role: 'admin' };
  const member = { id: 'm', role: 'member' };
  const viewer = { id: 'v', role: 'viewer' };
  // Nobody manages the owner or themselves from the table.
  assert.equal(canManageMember('owner', 'o', owner), false);
  assert.equal(canManageMember('admin', 'a', owner), false);
  assert.equal(canManageMember('admin', 'a', admin), false);
  // Owners and admins manage everyone else.
  assert.equal(canManageMember('owner', 'o', admin), true);
  assert.equal(canManageMember('owner', 'o', viewer), true);
  assert.equal(canManageMember('admin', 'a', member), true);
  assert.equal(canManageMember('admin', 'a', viewer), true);
  // Members and viewers never get row controls.
  assert.equal(canManageMember('member', 'm', viewer), false);
  assert.equal(canManageMember('viewer', 'v', member), false);
  assert.equal(canManageMember('owner', 'o', null), false);
});
