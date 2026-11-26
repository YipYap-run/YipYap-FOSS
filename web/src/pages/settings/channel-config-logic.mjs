// Pure helpers for the notification channel form (phone numbers, URL
// fields, server error mapping). Kept free of Preact so they can be checked
// with `npm test` (see channel-config-logic.test.mjs).

import {
  EVENTING_GUARDED_URL_FIELDS, EVENTING_PAID_TYPES, EVENTING_JSON_FIELDS,
  eventingServerErrorField, eventingSubfieldLabel,
} from '../../edition/eventing.mjs';

export const PHONE_TYPES = ['telnyx_sms', 'telnyx_voice', 'twilio_sms', 'twilio_voice'];

// Twilio rows from before the move to Telnyx still deliver and can be
// edited, but the API refuses new ones.
export const RETIRED_TYPES = ['twilio_sms', 'twilio_voice'];

export function isPhoneType(type) {
  return PHONE_TYPES.includes(type);
}

export function isRetiredType(type) {
  return RETIRED_TYPES.includes(type);
}

// Mirrors providers.NormalizeNANPNumber: 10 digits, or 11 starting with 1,
// area code not starting with 0 or 1. Returns { value: '+1XXXXXXXXXX' } or
// { error }. The server is the authority; this catches typos before submit.
export function normalizePhone(raw) {
  const s = typeof raw === 'string' ? raw.trim() : '';
  if (!s) return { error: 'Required' };
  let digits = s.replace(/\D/g, '');
  if (digits.length === 11) {
    if (digits[0] !== '1') return { error: 'Only US and Canada numbers (+1) are supported' };
  } else if (digits.length === 10) {
    digits = '1' + digits;
  } else if (s.startsWith('+') && !s.startsWith('+1')) {
    return { error: 'Only US and Canada numbers (+1) are supported' };
  } else {
    return { error: 'Enter a 10-digit number, for example +1 212-555-4240' };
  }
  if (digits[1] === '0' || digits[1] === '1') return { error: 'Area codes cannot start with 0 or 1' };
  return { value: '+' + digits };
}

// "+12125554240" -> "+1 212-555-4240" for display in the channel list.
export function formatPhone(e164) {
  const m = /^\+1(\d{3})(\d{3})(\d{4})$/.exec(e164 || '');
  return m ? `+1 ${m[1]}-${m[2]}-${m[3]}` : (e164 || '');
}

// The API takes exactly {"to"} for SMS and voice channels. Anything else
// (including keys a legacy row once held) is a 400, so the form sends only
// the number.
export function phoneConfigBody(config) {
  const n = normalizePhone(config?.to);
  return { to: n.value || (typeof config?.to === 'string' ? config.to.trim() : '') };
}

// URL fields the server checks against private and internal addresses.
export const GUARDED_URL_FIELDS = {
  webhook: ['url'],
  ntfy: ['server_url'],
  ...EVENTING_GUARDED_URL_FIELDS,
};

// Help text for outbound URL fields. Delivery does not follow redirects on
// any edition. SaaS also refuses private addresses; FOSS allows them unless
// the operator turns that off, so the copy does not promise either way there.
export function outboundURLHelp(type, key, edition, baseHelp) {
  if (!(GUARDED_URL_FIELDS[type] || []).includes(key)) return baseHelp || '';
  const parts = [];
  if (baseHelp) parts.push(baseHelp);
  parts.push(edition === 'foss'
    ? 'Redirects are not followed, so use the final URL.'
    : 'Must be a public internet address. Redirects are not followed, so use the final URL.');
  return parts.join(' ');
}

const PRIVATE_MSG = 'Private and internal addresses are not allowed. Use a public address.';

// Maps a channel 400 from the API onto a form field. Returns
// { field, message } or null when the message is not about one field (the
// caller shows it as a toast).
export function parseChannelServerError(type, message) {
  if (typeof message !== 'string' || !message) return null;

  if (isPhoneType(type)) {
    if (/^phone number is required/.test(message)) return { field: 'to', message: 'Required' };
    if (/US and Canada/.test(message)) return { field: 'to', message: 'Only US and Canada numbers (+1) are supported' };
    if (/^invalid config/.test(message)) return { field: 'to', message: 'Enter a phone number, for example +1 212-555-4240' };
    return null;
  }

  for (const field of GUARDED_URL_FIELDS[type] || []) {
    if (message === field + ' cannot point to a private or internal address') {
      return { field, message: PRIVATE_MSG };
    }
    const prefix = field + ': ';
    if (message.startsWith(prefix)) {
      let rest = message.slice(prefix.length);
      if (/^unsupported URL scheme/.test(rest)) rest = 'Use an http:// or https:// URL';
      else if (/^missing hostname/.test(rest)) rest = 'Enter a full URL with a host name';
      else if (/^invalid URL/.test(rest)) rest = 'Must be a valid URL';
      else rest = rest.charAt(0).toUpperCase() + rest.slice(1);
      return { field, message: rest };
    }
  }
  // Some edition channel types return a sentinel without a field name.
  const edField = eventingServerErrorField(type, message);
  if (edField) return { field: edField, message: PRIVATE_MSG };
  return null;
}

export const PAID_TYPES = ['telegram', ...PHONE_TYPES, ...EVENTING_PAID_TYPES];

// Which types the Type select offers. New channels get the types this edition
// and plan allow, minus retired ones. On edit the type is fixed, so the
// channel's own type is always listed (a downgraded org or a retired Twilio
// row would otherwise show the wrong label).
export function channelTypeOptions(allTypes, { edition, paid, editType } = {}) {
  return Object.keys(allTypes).filter(k => {
    if (editType) return k === editType;
    if (allTypes[k]?.legacy || isRetiredType(k)) return false;
    if (edition === 'foss' && isPhoneType(k)) return false;
    if (!paid && PAID_TYPES.includes(k)) return false;
    return true;
  });
}

// Maps a failed Test onto a stored URL the server now refuses (for example a
// webhook saved before private addresses were blocked), so the form can open
// on that field. Returns { field, message } or null.
export function testErrorField(type, message) {
  if (typeof message !== 'string') return null;
  const fields = GUARDED_URL_FIELDS[type];
  if (!fields || !/destination address is not allowed/.test(message)) return null;
  return { field: fields[0], message: PRIVATE_MSG };
}

// JSON fields the form edits as text, with the shape the API decodes them
// into (internal/api/handlers/notification_channels_config.go). 'list' is a
// JSON array of strings, 'map' a JSON object of string values. Webhook
// headers have their own row editor (webhook-headers-logic.mjs) because
// their values are write-only.
export const JSON_FIELDS = {
  ...EVENTING_JSON_FIELDS,
};

// Parses one JSON textarea. Returns { value } (undefined when the box is
// empty, so the key is left out) or { error } with copy for the field. It
// never falls back to an empty value: a typo must not erase headers.
export function parseJsonField(type, key, text) {
  const spec = JSON_FIELDS[type]?.[key];
  if (!spec) return { value: text };
  if (text !== null && typeof text === 'object') text = JSON.stringify(text);
  const s = typeof text === 'string' ? text.trim() : '';
  if (!s) return { value: undefined };
  const want = spec.shape === 'list' ? 'a list like ' + spec.example : 'an object like ' + spec.example;
  let v;
  try { v = JSON.parse(s); } catch (_) { return { error: 'Must be valid JSON: ' + want }; }
  if (spec.shape === 'list') {
    if (!Array.isArray(v) || v.some(x => typeof x !== 'string')) return { error: 'Must be ' + want + ' (strings only)' };
    if (v.some(x => !x.trim())) return { error: 'Entries cannot be empty' };
  } else {
    if (v === null || typeof v !== 'object' || Array.isArray(v)) return { error: 'Must be ' + want };
    if (Object.values(v).some(x => typeof x !== 'string')) return { error: 'Values must be strings, like ' + spec.example };
  }
  return { value: v };
}

// Parses every JSON field of a config for save. Returns { config, errors }:
// config has parsed values (empty boxes removed), errors maps field to copy.
export function parseJsonFields(type, config) {
  const out = { ...config };
  const errors = {};
  for (const key of Object.keys(JSON_FIELDS[type] || {})) {
    if (!(key in out)) continue;
    const r = parseJsonField(type, key, out[key]);
    if (r.error) errors[key] = r.error;
    else if (r.value === undefined) delete out[key];
    else out[key] = r.value;
  }
  return { config: out, errors };
}

// Turns stored object and list values into text for the edit form. A
// legacy row that stored a string keeps it, so the form shows what is
// there and validation asks for a fix.
export function stringifyJsonFields(type, config) {
  const out = { ...config };
  for (const key of Object.keys(JSON_FIELDS[type] || {})) {
    if (out[key] !== null && typeof out[key] === 'object') out[key] = JSON.stringify(out[key], null, 2);
  }
  return out;
}

// Maps a typed config 400 ({ error: "<field>: <message>", field }) onto a
// form field. Nested fields ("auth.oidc.client_id") land on their
// top-level input. Returns
// { field, message } or null.
export function configFieldError(type, body, formKeys) {
  const field = body && typeof body.field === 'string' ? body.field : '';
  if (!field || typeof body.error !== 'string') return null;
  const top = field.split('.')[0];
  if (!formKeys.includes(top)) return null;
  let msg = body.error.startsWith(field + ': ') ? body.error.slice(field.length + 2) : body.error;
  if (top !== field) {
    const sub = field.slice(top.length + 1);
    // Header names are shown as typed: "X-Api_Key has no saved value".
    if (top === 'headers') return { field: top, message: sub + ' ' + msg };
    const label = eventingSubfieldLabel(top, sub);
    msg = (label || sub.replace(/[._]/g, ' ')) + ' ' + msg;
  }
  return { field: top, message: msg.charAt(0).toUpperCase() + msg.slice(1) };
}

// Re-indents a JSON box on blur so a one-line paste reads as the stored
// value will. Text that does not parse, or has the wrong shape, is left as
// typed so the user can see and fix it.
export function prettyJsonField(type, key, text) {
  if (typeof text !== 'string' || !text.trim()) return text;
  const r = parseJsonField(type, key, text);
  if (r.error || r.value === undefined) return text;
  return JSON.stringify(r.value, null, 2);
}

// Rows for a JSON textarea: grows with the content, from 3 up to 12 lines
// (longer values scroll).
export function jsonFieldRows(text) {
  const lines = typeof text === 'string' ? text.split('\n').length : 1;
  return Math.min(12, Math.max(3, lines));
}
