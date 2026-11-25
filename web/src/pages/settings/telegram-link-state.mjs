// Pure helpers for linking a Telegram chat with a one-time code. Kept free
// of Preact so `node --test` can check them against the API contract in
// internal/api/handlers/telegram_link.go:
//
//   POST /telegram/link-codes {name} or {channel_id}
//     201 {id, status: "pending", expires_at, code, command, bot_username,
//          group_link, private_link}
//     400 bad input, 403 plan or channel limit, 404 unknown channel,
//     429 too many codes this hour, 503 linking not configured.
//   GET /telegram/link-codes/{id}
//     200 {id, status: "pending"|"linked"|"expired"|"failed", expires_at,
//          channel_id, chat_title (when linked)}
//
// Telegram channels hold chat_id and chat_title in their config. Both are
// set by the bot when a chat sends the code; the API refuses to change them.

export const POLL_INTERVAL_MS = 3000;

export function isTelegramChannel(ch) {
  return !!ch && ch.type === 'telegram';
}

function chatID(config) {
  if (!config || typeof config !== 'object') return '';
  const v = config.chat_id;
  return v === undefined || v === null ? '' : String(v).trim();
}

// An own-bot channel (API only) carries a bot token; the YipYap bot cannot
// link it.
export function usesOwnBot(config) {
  return !!config && typeof config === 'object' && !!config.bot_token;
}

export function isTelegramUnlinked(ch) {
  return isTelegramChannel(ch) && !chatID(ch.config);
}

// What the channel list and edit form call the linked chat. Older channels
// linked by typing an ID have no title.
export function telegramChatLabel(config) {
  const title = config && typeof config.chat_title === 'string' ? config.chat_title.trim() : '';
  if (title) return title;
  const id = chatID(config);
  return id ? 'Chat ' + id : '';
}

export function telegramSummary(config) {
  const label = telegramChatLabel(config);
  return label ? 'Chat: ' + label : 'No chat linked';
}

export function secondsLeft(expiresAt, nowMs = Date.now()) {
  const t = Date.parse(expiresAt || '');
  if (Number.isNaN(t)) return 0;
  return Math.max(0, Math.ceil((t - nowMs) / 1000));
}

export function formatMinutesLeft(seconds) {
  const s = Math.max(0, Math.ceil(Number(seconds) || 0));
  if (s === 0) return 'expired';
  if (s < 60) return 'less than a minute left';
  const m = Math.ceil(s / 60);
  return m === 1 ? '1 minute left' : m + ' minutes left';
}

// The visible countdown, m:ss. Screen readers get formatMinutesLeft
// instead, so they do not hear "4 colon 59".
export function formatCountdown(seconds) {
  const s = Math.max(0, Math.ceil(Number(seconds) || 0));
  const m = Math.floor(s / 60);
  const r = s % 60;
  return m + ':' + (r < 10 ? '0' : '') + r;
}

// Under this many seconds the status line warns once that the code is
// about to expire.
export const EXPIRY_WARNING_SECONDS = 60;

export function expiresSoon(seconds) {
  const s = Number(seconds) || 0;
  return s > 0 && s <= EXPIRY_WARNING_SECONDS;
}

// A code is done once it is linked, expired or failed; polling stops then.
export function isFinalStatus(status) {
  return status === 'linked' || status === 'expired' || status === 'failed';
}

// soon is true in the last minute of a pending code. The status line is a
// live region, so the text changes once when the minute starts, not on
// every tick.
export function statusMessage(status, chatTitle, soon = false) {
  switch (status) {
    case 'linked':
      return chatTitle ? 'Linked to ' + chatTitle + '.' : 'Chat linked.';
    case 'expired':
      return 'This code expired. Get a new code to try again.';
    case 'failed':
      return 'The bot could not link the chat. Check the reply in Telegram, then get a new code.';
    default:
      return soon
        ? 'Waiting for the code in Telegram. It expires in less than a minute.'
        : 'Waiting for the code in Telegram.';
  }
}

// The body for POST /telegram/link-codes: an existing channel is linked by
// ID, a new one is named and created when the chat sends the code.
export function linkCodeRequest({ channelId, name }) {
  if (channelId) return { purpose: 'channel', channel_id: channelId };
  return { purpose: 'channel', name: String(name || '').trim() };
}

export function interpretLinkCodeError(err) {
  const status = err && err.status;
  const raw = (err && err.body && err.body.error) || (err && err.message) || '';
  switch (status) {
    case 503:
      return { message: 'Telegram linking is not set up on this server. Ask your YipYap operator to configure the Telegram bot.', unavailable: true };
    case 429:
      return { message: 'Too many link codes this hour. Try again later.', unavailable: false };
    case 404:
      return { message: 'This channel no longer exists. Close this window and reload.', unavailable: true };
    case 403:
      return { message: raw ? sentence(raw) : 'You cannot add more channels on this plan.', unavailable: true };
    default:
      return { message: raw ? sentence(raw) : 'Could not create a link code. Try again.', unavailable: false };
  }
}

function sentence(msg) {
  const s = String(msg).trim();
  if (!s) return s;
  const out = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(out) ? out : out + '.';
}

// Matches telegramLinkMaxNameLen on the server.
export const NAME_MAX = 100;

// After this many failed polls in a row the panel says it cannot reach
// YipYap (it keeps trying).
export const POLL_FAILURES_BEFORE_NOTICE = 3;

// The t.me links come from the API. Only render plain https links to
// t.me, so a bad value can never become a javascript: or other link.
export function safeTelegramLink(href) {
  if (typeof href !== 'string' || !href) return '';
  let u;
  try { u = new URL(href); } catch (_) { return ''; }
  if (u.protocol !== 'https:' || u.hostname !== 't.me' || u.username || u.password || u.port) return '';
  return u.href;
}

// What a failed status poll means for the panel. A 404 means the code is
// gone (deleted channel or another org), so polling stops. Other errors
// are network or server blips: keep polling, and say so after a few.
export function pollFailure(err, failuresInARow) {
  if (err && err.status === 404) {
    return { stop: true, message: 'This code is no longer valid. Get a new code to try again.' };
  }
  if (failuresInARow >= POLL_FAILURES_BEFORE_NOTICE) {
    return { stop: false, message: 'Cannot reach YipYap to check the code. Still trying.' };
  }
  return { stop: false, message: '' };
}

// The name sent with the code is the one the channel gets. If the name
// field changed after the code was made, the panel says so.
export function nameChangedSinceCode(codeName, currentName) {
  const a = String(codeName || '').trim();
  const b = String(currentName || '').trim();
  return a !== '' && a !== b;
}

// What the page-level live region says once a chat is linked. The form
// closes on link, so its own status line is gone before a screen reader
// can read it; the page keeps a region that outlives the modal.
export function linkedAnnouncement(res) {
  const title = res && typeof res.chat_title === 'string' ? res.chat_title.trim() : '';
  return statusMessage('linked', title);
}
