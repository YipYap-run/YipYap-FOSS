// Pure helpers for the phone number verification step. Kept free of Preact
// so `node --test` can check them against the API contract in
// internal/api/handlers/phone_verify_full.go.

export const PHONE_CHANNEL_TYPES = ['telnyx_sms', 'telnyx_voice', 'twilio_sms', 'twilio_voice'];

export const CODE_LENGTH = 6;

// Cooldowns longer than this are hourly or daily limits. The UI states the
// wait in words instead of running a per-second countdown.
export const LIVE_COUNTDOWN_MAX_SECONDS = 300;

export function isPhoneChannel(type) {
  return PHONE_CHANNEL_TYPES.includes(type);
}

// A phone channel is unverified when the server says so. Channels without a
// verification field (every non-phone type) are never treated as pending.
export function isUnverified(ch) {
  return !!ch && !!ch.verification && ch.verification.status === 'pending';
}

export function defaultMethod(type) {
  return typeof type === 'string' && type.endsWith('_voice') ? 'voice' : 'sms';
}

// Keep digits only, capped at the code length. Handles pasted codes with
// spaces or dashes ("123 456", "123-456").
export function normalizeCode(raw) {
  return String(raw || '').replace(/\D/g, '').slice(0, CODE_LENGTH);
}

// Server messages are lower case and use semicolons
// ("the verification code has expired; request a new code"). Turn them into
// sentences for display.
export function toSentence(msg) {
  const parts = String(msg || '').split(';').map(p => p.trim()).filter(Boolean);
  if (!parts.length) return '';
  const out = parts.map(p => p.charAt(0).toUpperCase() + p.slice(1)).join('. ');
  return /[.!?]$/.test(out) ? out : out + '.';
}

export function formatWait(seconds) {
  const s = Math.max(0, Math.ceil(Number(seconds) || 0));
  if (s < 60) return s === 1 ? '1 second' : s + ' seconds';
  if (s < 3600) {
    const m = Math.ceil(s / 60);
    return m === 1 ? '1 minute' : m + ' minutes';
  }
  const h = Math.ceil(s / 3600);
  return h === 1 ? '1 hour' : h + ' hours';
}

export function formatCountdown(seconds) {
  const s = Math.max(0, Math.ceil(Number(seconds) || 0));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return m + ':' + String(r).padStart(2, '0');
}

function retryAfterOf(err) {
  const n = Number(err && err.body && err.body.retry_after_seconds);
  return Number.isFinite(n) && n > 0 ? Math.ceil(n) : 0;
}

// interpretSendError maps a failed POST .../verify/send to UI state.
//   retryAfter:      seconds until Resend is allowed again (0 = now)
//   unavailable:     platform SMS is not configured, so hide the send buttons
//   alreadyVerified: the number was confirmed elsewhere, refresh the channel
export function interpretSendError(err) {
  const status = err && err.status;
  const serverMsg = err && err.body && err.body.error;
  const res = { message: '', retryAfter: 0, unavailable: false, alreadyVerified: false };
  switch (status) {
    case 429: {
      res.retryAfter = retryAfterOf(err);
      if (!res.retryAfter) {
        res.message = toSentence(serverMsg) || 'Too many codes requested. Try again later.';
        return res;
      }
      // Keep the first clause ("a code was just sent") and replace the
      // vague second one ("try again later") with the actual wait.
      const first = toSentence(String(serverMsg || '').split(';')[0]) || 'Too many codes requested.';
      res.message = first + ' You can request another code in ' + formatWait(res.retryAfter) + '.';
      return res;
    }
    case 409:
      res.alreadyVerified = true;
      res.message = 'This number is already verified.';
      return res;
    case 400:
      res.message = toSentence(serverMsg) || 'This number cannot be verified.';
      return res;
    case 502:
      res.message = 'Could not send the code. Check the number and try again in a moment.';
      return res;
    case 503:
      res.unavailable = true;
      res.message = 'Phone verification is not available right now. Contact support to finish setting up this channel.';
      return res;
    default:
      if (err && err.readonly) {
        res.message = toSentence(err.message);
        return res;
      }
      res.message = 'Could not send the code. Try again.';
      return res;
  }
}

// interpretConfirmError maps a failed POST .../verify/confirm to UI state.
//   attemptsRemaining: wrong guesses left on the current code, or null
//   needNewCode:       the current code is used up (locked, expired, replaced)
export function interpretConfirmError(err) {
  const status = err && err.status;
  const body = (err && err.body) || {};
  const res = { message: '', attemptsRemaining: null, needNewCode: false };
  switch (status) {
    case 422: {
      const left = Number(body.attempts_remaining);
      res.attemptsRemaining = Number.isFinite(left) ? left : null;
      if (res.attemptsRemaining === 0) {
        res.needNewCode = true;
        res.message = 'That code is not correct, and no attempts are left. Request a new code.';
      } else if (res.attemptsRemaining !== null) {
        res.message = 'That code is not correct. ' +
          (res.attemptsRemaining === 1 ? '1 attempt left.' : res.attemptsRemaining + ' attempts left.');
      } else {
        res.message = 'That code is not correct.';
      }
      return res;
    }
    case 429:
      res.attemptsRemaining = 0;
      res.needNewCode = true;
      res.message = 'Too many incorrect codes. Request a new code.';
      return res;
    case 410:
      res.needNewCode = true;
      res.message = 'This code has expired. Request a new code.';
      return res;
    case 409:
      res.needNewCode = true;
      res.message = 'No code is waiting for this number. The number may have changed since the code was sent. Request a new code.';
      return res;
    case 400:
      res.message = 'Enter the 6-digit code.';
      return res;
    default:
      if (err && err.readonly) {
        res.message = toSentence(err.message);
        return res;
      }
      res.message = 'Could not check the code. Try again.';
      return res;
  }
}
