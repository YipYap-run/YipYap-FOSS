// Pure helpers for the email channel form. Kept free of Preact so they can
// be checked with `npm test` (see email-channel-logic.test.mjs).

export const EMAIL_MAX_RECIPIENTS_DEFAULT = 10;

// Order matters: it is the order the fields appear in the form, so the first
// error in this list is the one that gets focus.
export const EMAIL_FIELDS = ['to', 'mode', 'host', 'port', 'tls_mode', 'username', 'password', 'from'];

const SERVER_KEYS = ['host', 'port', 'tls_mode', 'username', 'password', 'from'];

// Same separators the API accepts: comma, semicolon and newlines.
export function splitRecipients(value) {
  if (Array.isArray(value)) value = value.join(',');
  if (typeof value !== 'string') return [];
  return value.split(/[,;\r\n]+/).map(s => s.trim()).filter(Boolean);
}

export function joinRecipients(list) {
  return list.join(', ');
}

// Bare address from "a@b.c" or "Name <a@b.c>". The server parses with
// net/mail and is the authority; this check only catches typos early.
export function bareAddress(entry) {
  const m = /<([^<>]*)>\s*$/.exec(entry);
  return (m ? m[1] : entry).trim();
}

export function isLikelyEmail(entry) {
  return /^[^\s@<>()[\]\\,;:"]+@[^\s@<>()[\]\\,;:"]+\.[^\s@<>()[\]\\,;:".]{2,}$/.test(bareAddress(entry));
}

// Adds entries to a list, dropping case-insensitive duplicates.
export function addRecipients(list, entries) {
  const seen = new Set(list.map(e => bareAddress(e).toLowerCase()));
  const out = [...list];
  for (const e of entries) {
    const key = bareAddress(e).toLowerCase();
    if (!key || seen.has(key)) continue;
    seen.add(key);
    out.push(e);
  }
  return out;
}

// Client-side checks that mirror the API rules for the chosen mode. Returns
// { field: message }.
export function validateEmailConfig(config, { maxRecipients = EMAIL_MAX_RECIPIENTS_DEFAULT } = {}) {
  const errs = {};
  const to = splitRecipients(config.to);
  if (to.length === 0) errs.to = 'Add at least one recipient';
  else if (to.length > maxRecipients) errs.to = `Up to ${maxRecipients} recipients`;
  else {
    const bad = to.find(e => !isLikelyEmail(e));
    if (bad) errs.to = `"${bad}" is not a valid email address`;
  }
  if (config.mode !== 'smtp') return errs;

  const str = k => (config[k] == null ? '' : String(config[k])).trim();
  if (!str('host')) errs.host = 'Required';
  else if (/[\s/:]/.test(str('host'))) errs.host = 'Enter a host name only, without a scheme or port';

  const port = str('port');
  if (!port) errs.port = 'Required';
  else if (!/^\d+$/.test(port) || +port < 1 || +port > 65535) errs.port = 'Must be a number from 1 to 65535';

  if (!['starttls', 'tls'].includes(str('tls_mode'))) errs.tls_mode = 'Required';
  if (!str('username')) errs.username = 'Required';
  // Not trimmed on the server either: spaces may be part of a password.
  if (!config.password) errs.password = 'Required';
  if (!str('from')) errs.from = 'Required';
  else if (!isLikelyEmail(str('from'))) errs.from = 'Must be a valid email address';
  return errs;
}

// The body the API expects for each mode. Platform mode must not carry server
// keys at all (the API rejects them), so they are dropped, not blanked.
export function emailConfigBody(config) {
  const to = joinRecipients(splitRecipients(config.to));
  if (config.mode !== 'smtp') return { mode: 'platform', to };
  const body = { mode: 'smtp', to };
  for (const k of SERVER_KEYS) body[k] = config[k];
  body.port = Number(config.port);
  return body;
}

const API_PREFIX = 'invalid email channel config: ';

// The API reports email config problems as
//   "invalid email channel config: <field> <message>"
// with the field key first. Returns { field, message } (message in sentence
// case, ready to show under the input) or null for any other error.
export function parseEmailConfigError(message) {
  if (typeof message !== 'string' || !message.startsWith(API_PREFIX)) return null;
  const rest = message.slice(API_PREFIX.length);
  const sp = rest.indexOf(' ');
  const field = sp < 0 ? rest : rest.slice(0, sp);
  let msg = sp < 0 ? '' : rest.slice(sp + 1).trim();
  if (!EMAIL_FIELDS.includes(field)) return { field: null, message: rest };
  // The server has no platform mailer (FOSS without SMTP settings). The raw
  // text names the internal mode value, so say what to do instead.
  if (field === 'mode' && msg.includes('not available')) {
    return {
      field,
      message: 'This server has no mail service set up. Enter your own SMTP server below.',
      platformUnavailable: true,
    };
  }
  // "is required" reads as "Required"; "is not a valid email address" as
  // "Not a valid email address"; everything else is capitalized as is.
  if (msg.startsWith('is ')) msg = msg.slice(3);
  if (msg === 'required') msg = 'Required';
  msg = msg.charAt(0).toUpperCase() + msg.slice(1);
  return { field, message: msg || 'Check this field' };
}
