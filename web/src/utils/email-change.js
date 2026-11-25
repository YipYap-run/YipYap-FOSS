// Email change timings. Mirror auth.EmailChangeTTL and emailChangeCooldown
// in the Go handlers; the server enforces both.
export const EMAIL_CHANGE_TTL_HOURS = 24;
export const EMAIL_CHANGE_COOLDOWN_SECONDS = 60;

// pendingEmailStatus describes the user's pending email change at `now`:
// null when nothing is pending, otherwise the address, when the link
// expires, whether it already has, and how many seconds remain before a new
// link can be sent.
export function pendingEmailStatus(user, now = Date.now()) {
  if (!user?.pending_email) return null;
  const requested = Date.parse(user.pending_email_requested_at || '');
  if (Number.isNaN(requested)) {
    return { email: user.pending_email, expiresAt: null, expired: false, resendIn: 0 };
  }
  const expiresAt = requested + EMAIL_CHANGE_TTL_HOURS * 3600 * 1000;
  const resendAt = requested + EMAIL_CHANGE_COOLDOWN_SECONDS * 1000;
  return {
    email: user.pending_email,
    expiresAt,
    expired: now >= expiresAt,
    resendIn: Math.max(0, Math.ceil((resendAt - now) / 1000)),
  };
}

// describeEmailChangeError maps the email change error codes to copy, or
// returns '' so the caller can fall back to its general handling.
export function describeEmailChangeError(err) {
  const code = err?.body?.code;
  switch (code) {
    case 'email_taken':
      return 'Another account already uses that email address. Use a different address.';
    case 'invalid_email':
      return 'Enter a valid email address, like name@company.com.';
    case 'email_unchanged':
      return 'That is already your email address.';
    case 'email_change_cooldown':
      return cooldownMessage(err?.body?.retry_after);
    case 'email_change_link_invalid':
      return 'This link has expired, was already used, or was replaced by a newer request. Start the change again from Account settings.';
    default:
      if (err?.status === 429) return cooldownMessage(err?.body?.retry_after);
      return '';
  }
}

function cooldownMessage(seconds) {
  const n = Number(seconds);
  if (n > 0) return `We just sent a link. Wait ${n} second${n === 1 ? '' : 's'} before sending another.`;
  return 'We just sent a link. Wait a minute before sending another.';
}
