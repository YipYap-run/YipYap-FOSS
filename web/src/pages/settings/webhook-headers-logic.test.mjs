import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CONFIG_SENTINEL } from './channel-secrets-logic.mjs';
import {
  headersToRows, rowsToHeaders, newHeaderRow, headerRowForServerField, headersSummary, isBlankRow, MAX_HEADERS,
} from './webhook-headers-logic.mjs';

// API contract (notification_channels_redact.go): GET returns names with
// the sentinel as every value; PATCH keeps a value sent as the sentinel,
// replaces one sent as a string, and deletes a header left out.

test('saved headers load as rows that never hold the value', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL, Authorization: CONFIG_SENTINEL });
  assert.deepEqual(rows.map(r => r.name), ['Authorization', 'X-Key']);
  assert.ok(rows.every(r => r.saved && !r.replacing && r.value === ''));
  assert.deepEqual(headersToRows(undefined), []);
  assert.deepEqual(headersToRows(['x']), []);
});

test('untouched saved headers go back as the sentinel', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL });
  const r = rowsToHeaders(rows);
  assert.deepEqual(r.errors, {});
  assert.deepEqual(r.headers, { 'X-Key': CONFIG_SENTINEL });
});

test('a replaced header sends the new value and needs one', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL });
  rows[0].replacing = true;
  assert.match(rowsToHeaders(rows).errors[rows[0].key].value, /keep the saved one/);
  rows[0].value = 'new-secret';
  assert.deepEqual(rowsToHeaders(rows).headers, { 'X-Key': 'new-secret' });
});

test('a removed header is left out, and no rows means no headers key', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL, 'X-Other': CONFIG_SENTINEL });
  assert.deepEqual(rowsToHeaders(rows.slice(1)).headers, { 'X-Other': CONFIG_SENTINEL });
  assert.equal(rowsToHeaders([]).headers, undefined);
});

test('a new header needs a valid name and a value', () => {
  const row = { ...newHeaderRow(), name: 'X Api', value: 'v' };
  assert.match(rowsToHeaders([row]).errors[row.key].name, /letters, digits/);
  row.name = 'content-type';
  assert.match(rowsToHeaders([row]).errors[row.key].name, /Content-Type is set by YipYap/);
  row.name = ' X-Api ';
  row.value = '';
  assert.match(rowsToHeaders([row]).errors[row.key].value, /Enter a value/);
  row.value = 'a\r\nInjected: 1';
  assert.match(rowsToHeaders([row]).errors[row.key].value, /line breaks/);
  row.value = CONFIG_SENTINEL;
  assert.match(rowsToHeaders([row]).errors[row.key].value, /real value/);
  row.value = 'ok';
  assert.deepEqual(rowsToHeaders([row]), { headers: { 'X-Api': 'ok' }, errors: {}, formError: '' });
});

test('duplicate names are refused ignoring case', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL });
  const dup = { ...newHeaderRow(), name: 'x-key', value: 'v' };
  assert.match(rowsToHeaders([...rows, dup]).errors[dup.key].name, /already in the list/);
});

test('blank rows are ignored and the header cap is enforced', () => {
  const blank = newHeaderRow();
  assert.ok(isBlankRow(blank));
  assert.deepEqual(rowsToHeaders([blank]), { headers: undefined, errors: {}, formError: '' });
  const many = Array.from({ length: MAX_HEADERS + 1 }, (_, i) => ({ ...newHeaderRow(), name: 'X-' + i, value: 'v' }));
  assert.match(rowsToHeaders(many).formError, /At most 32/);
});

test('a server error on headers.NAME points at that row', () => {
  const rows = headersToRows({ 'X-Key': CONFIG_SENTINEL });
  assert.equal(headerRowForServerField(rows, 'headers.X-Key'), rows[0].key);
  assert.equal(headerRowForServerField(rows, 'headers.x-key'), rows[0].key);
  assert.equal(headerRowForServerField(rows, 'headers.Nope'), null);
  assert.equal(headerRowForServerField(rows, 'url'), null);
});

test('summary counts headers and saved values', () => {
  assert.equal(headersSummary([]), 'No custom headers');
  const rows = headersToRows({ A: CONFIG_SENTINEL, B: CONFIG_SENTINEL });
  rows[1].replacing = true;
  assert.equal(headersSummary(rows), '2 headers, 1 saved value');
});
