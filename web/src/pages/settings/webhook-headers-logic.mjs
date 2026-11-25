// Webhook custom headers editor (notif-7). The API returns config.headers
// with the real names and CONFIG_SENTINEL as every value, for every role.
// On save, per header: the sentinel keeps the stored value, a new string
// replaces it, and a header left out is deleted. The checks below mirror
// checkWebhookHeaders in internal/api/handlers/notification_channels_config.go
// so most mistakes show next to the row before the request is sent.

import { CONFIG_SENTINEL, isSavedSecret } from './channel-secrets-logic.mjs';

export const MAX_HEADERS = 32;
export const MAX_HEADER_NAME = 256;
export const MAX_HEADER_VALUE = 4096;

// Set by the HTTP client or YipYap itself (webhookManagedHeaders).
const MANAGED = ['host', 'content-length', 'transfer-encoding', 'connection', 'content-type'];

const TOKEN_RE = /^[!#$%&'*+\-.^_`|~0-9A-Za-z]+$/;

let seq = 0;
function rowKey() {
  seq += 1;
  return 'h' + seq;
}

// A row the editor renders. saved: the server holds a value we cannot see.
// replacing: the user chose to type a new value for a saved header.
export function newHeaderRow() {
  return { key: rowKey(), name: '', value: '', saved: false, replacing: false };
}

// headersToRows turns the API's config.headers into editor rows, sorted by
// name so the order is stable between loads. A legacy string value (from
// before the typed config) is kept as a plain row so the user sees it.
export function headersToRows(headers) {
  if (!headers || typeof headers !== 'object' || Array.isArray(headers)) return [];
  return Object.keys(headers)
    .sort((a, b) => a.toLowerCase().localeCompare(b.toLowerCase()))
    .map(name => {
      const v = headers[name];
      const saved = isSavedSecret(v);
      return { key: rowKey(), name, value: saved ? '' : (typeof v === 'string' ? v : ''), saved, replacing: false };
    });
}

function canonical(name) {
  return name.split('-').map(p => p.charAt(0).toUpperCase() + p.slice(1).toLowerCase()).join('-');
}

function badValueChars(s) {
  for (let i = 0; i < s.length; i++) {
    const c = s.charCodeAt(i);
    if ((c < 0x20 && c !== 9) || c === 0x7f) return true;
  }
  return false;
}

// isBlankRow: a new row with nothing typed. Blank rows are ignored on save
// so an unused "Add header" click does not block the form.
export function isBlankRow(row) {
  return !row.saved && !row.name.trim() && !row.value;
}

// rowsToHeaders builds the headers object for the request body.
// Returns { headers, errors, formError }:
//   headers   the object to send, or undefined when there are no headers
//             (the key is then left out and every stored header is deleted)
//   errors    { [row.key]: { name?, value? } } copy for each bad row
//   formError copy for the whole editor (too many headers), or ''
export function rowsToHeaders(rows) {
  const list = (rows || []).filter(r => !isBlankRow(r));
  const errors = {};
  const seen = new Map();
  const headers = {};
  const fail = (row, part, msg) => {
    errors[row.key] = { ...(errors[row.key] || {}), [part]: msg };
  };
  for (const row of list) {
    const name = row.saved ? row.name : row.name.trim();
    if (!name) fail(row, 'name', 'Enter a header name');
    else if (name.length > MAX_HEADER_NAME) fail(row, 'name', 'Header names can be at most ' + MAX_HEADER_NAME + ' characters');
    else if (!TOKEN_RE.test(name)) fail(row, 'name', 'Use letters, digits and - only (no spaces or colons)');
    else if (MANAGED.includes(name.toLowerCase())) fail(row, 'name', canonical(name) + ' is set by YipYap and cannot be changed');
    else if (seen.has(name.toLowerCase())) fail(row, 'name', 'This header is already in the list');
    if (name) seen.set(name.toLowerCase(), row.key);

    if (row.saved && !row.replacing) {
      headers[name] = CONFIG_SENTINEL;
      continue;
    }
    const value = row.value;
    if (!value) fail(row, 'value', row.saved ? 'Enter the new value, or keep the saved one' : 'Enter a value');
    else if (value.length > MAX_HEADER_VALUE) fail(row, 'value', 'Values can be at most ' + MAX_HEADER_VALUE + ' characters');
    else if (badValueChars(value)) fail(row, 'value', 'Remove line breaks and other control characters');
    else if (isSavedSecret(value)) fail(row, 'value', 'Enter the real value');
    if (name) headers[name] = value;
  }
  const formError = list.length > MAX_HEADERS ? 'At most ' + MAX_HEADERS + ' headers are allowed' : '';
  return { headers: list.length ? headers : undefined, errors, formError };
}

// headerRowForServerField finds the row a server 400 names, for a field
// like "headers.X-Key". Names match exactly first, then ignoring case, as
// the server merges them. Returns the row key or null.
export function headerRowForServerField(rows, field) {
  if (typeof field !== 'string' || !field.startsWith('headers.')) return null;
  const name = field.slice('headers.'.length);
  const list = rows || [];
  const exact = list.find(r => r.name.trim() === name);
  if (exact) return exact.key;
  const loose = list.find(r => r.name.trim().toLowerCase() === name.toLowerCase());
  return loose ? loose.key : null;
}

// headersSummary is a short line for screen readers and the collapsed
// state: "2 headers, 1 saved value".
export function headersSummary(rows) {
  const list = (rows || []).filter(r => !isBlankRow(r));
  if (!list.length) return 'No custom headers';
  const saved = list.filter(r => r.saved && !r.replacing).length;
  let s = list.length === 1 ? '1 header' : list.length + ' headers';
  if (saved) s += ', ' + saved + (saved === 1 ? ' saved value' : ' saved values');
  return s;
}
