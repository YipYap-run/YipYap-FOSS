// Copy and state helpers for the self-service email change on the Account
// tab. Kept free of Preact so `npm test` can pin them against the API
// contract of PUT /auth/email and POST /auth/resend-verification.

// emailChangeError turns a PUT /auth/email failure into copy a person can act on.
export function emailChangeError(err) {
  switch (err?.status) {
    case 409:
      return 'Another account already uses that email address.';
    case 400:
      return err.message === 'invalid email address' || err.message === 'email address too long'
        ? 'Enter a valid email address, like name@example.com.'
        : (err.message || 'Check the form and try again.');
    case 401:
      return 'Your current password is not correct.';
    case 403:
      return err.message === 'MFA verification failed'
        ? 'The MFA code is not correct.'
        : (err.message || 'You cannot change your email address right now.');
    default:
      return err?.message || 'Failed to update email. Try again.';
  }
}

// emailChangeSuccess is the confirmation shown after PUT /auth/email.
export function emailChangeSuccess(res, fallbackEmail) {
  const addr = res?.user?.email || fallbackEmail;
  return res?.verification_required
    ? `Email updated. We sent a verification link to ${addr}. Open it before you next sign in.`
    : `Email updated. Sign in with ${addr} from now on.`;
}

// needsEmailVerification reports whether the signed-in user still has to
// confirm their address. Synthetic sessions (read-only shares, staff
// read-only views) carry no real address and never show the notice.
export function needsEmailVerification(user) {
  return !!(user && user.id && user.id !== 'session' && typeof user.email === 'string' &&
    user.email.includes('@') && !user.email_verified_at);
}

// resendOutcome maps a POST /auth/resend-verification result (or error) to
// {kind, message, cooldown}. kind is 'ok', 'verified', 'wait', 'unavailable'
// or 'error'. cooldown is seconds until the button may be used again.
export const RESEND_COOLDOWN_SECS = 30;

export function resendOutcome(res, err) {
  if (err) {
    if (err.status === 429) {
      const wait = Number(err.body?.retry_after);
      return {
        kind: 'wait',
        message: 'Too many requests. Wait a moment, then try again.',
        cooldown: Number.isFinite(wait) && wait > 0 ? Math.ceil(wait) : RESEND_COOLDOWN_SECS,
      };
    }
    if (err.status === 404) {
      return {
        kind: 'unavailable',
        message: 'This server is not set up to send email. Ask your administrator to verify your address.',
        cooldown: 0,
      };
    }
    return { kind: 'error', message: err.message || 'Could not send the email. Try again.', cooldown: 0 };
  }
  if (res?.status === 'already_verified') {
    return { kind: 'verified', message: 'Your email address is verified.', cooldown: 0 };
  }
  return {
    kind: 'ok',
    message: 'Verification link sent. Check your inbox and spam folder.',
    cooldown: RESEND_COOLDOWN_SECS,
  };
}
