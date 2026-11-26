// Run with: cd web && npm test
// The messages parsed here are the API contract from
// internal/api/handlers/notification_channels.go (validateUserURLs) and
// notification_channels_phone_full.go (validatePhoneConfig).
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizePhone, formatPhone, phoneConfigBody, parseChannelServerError,
  outboundURLHelp, isRetiredType, channelTypeOptions, testErrorField,
  parseJsonField, parseJsonFields, stringifyJsonFields, configFieldError,
  jsonFieldRows,
} from './channel-config-logic.mjs';

test('normalizePhone accepts the formats the API accepts', () => {
  for (const raw of ['+1 212-555-4240', '(212) 555-4240', '2125554240', '1-212-555-4240', '+12125554240']) {
    assert.deepEqual(normalizePhone(raw), { value: '+12125554240' }, raw);
  }
});

test('normalizePhone rejects what the API rejects', () => {
  assert.equal(normalizePhone('').error, 'Required');
  assert.equal(normalizePhone(undefined).error, 'Required');
  assert.match(normalizePhone('+44 20 7183 8750').error, /US and Canada/);
  assert.match(normalizePhone('22125554240').error, /US and Canada/);
  assert.match(normalizePhone('555-4240').error, /10-digit/);
  assert.match(normalizePhone('+1 112-555-4240').error, /Area codes/);
  assert.match(normalizePhone('012-555-4240').error, /Area codes/);
});

test('formatPhone', () => {
  assert.equal(formatPhone('+12125554240'), '+1 212-555-4240');
  assert.equal(formatPhone('odd'), 'odd');
  assert.equal(formatPhone(undefined), '');
});

test('phoneConfigBody sends only the number, even from a legacy config', () => {
  assert.deepEqual(
    phoneConfigBody({ to: '(212) 555-4240', texml_url: 'https://x', from: '+1999', auth_token: '********' }),
    { to: '+12125554240' },
  );
  assert.deepEqual(phoneConfigBody({ to: ' bad ' }), { to: 'bad' });
  assert.deepEqual(phoneConfigBody({}), { to: '' });
});

test('parseChannelServerError maps phone 400s to the number field', () => {
  assert.deepEqual(parseChannelServerError('telnyx_sms', 'phone number is required'), { field: 'to', message: 'Required' });
  assert.equal(parseChannelServerError('telnyx_voice', 'only US and Canada phone numbers (+1) are supported at this time').field, 'to');
  assert.equal(parseChannelServerError('twilio_sms', 'invalid config: expected {"to": "+1XXXXXXXXXX"}').field, 'to');
  assert.equal(parseChannelServerError('twilio_sms', 'Twilio channels are retired. Create an SMS (Telnyx) channel instead.'), null);
});

test('parseChannelServerError maps URL 400s without echoing internals', () => {
  const priv = parseChannelServerError('webhook', 'url cannot point to a private or internal address');
  assert.equal(priv.field, 'url');
  assert.match(priv.message, /Private and internal addresses/);
  assert.equal(parseChannelServerError('ntfy', 'server_url cannot point to a private or internal address').field, 'server_url');
  assert.deepEqual(parseChannelServerError('ntfy', 'server_url: unsupported URL scheme "ftp"'), { field: 'server_url', message: 'Use an http:// or https:// URL' });
  assert.deepEqual(parseChannelServerError('webhook', 'url: missing hostname'), { field: 'url', message: 'Enter a full URL with a host name' });
  // A url error on a type without that field is not mapped.
  assert.equal(parseChannelServerError('slack', 'url cannot point to a private or internal address'), null);
  assert.equal(parseChannelServerError('webhook', ''), null);
});

test('outboundURLHelp', () => {
  assert.match(outboundURLHelp('webhook', 'url', 'saas'), /public internet address/);
  assert.doesNotMatch(outboundURLHelp('webhook', 'url', 'foss'), /public/);
  assert.match(outboundURLHelp('ntfy', 'server_url', 'saas', 'Leave blank for ntfy.sh'), /^Leave blank for ntfy\.sh Must be/);
  assert.equal(outboundURLHelp('ntfy', 'topic', 'saas', 'x'), 'x');
});

test('isRetiredType', () => {
  assert.ok(isRetiredType('twilio_voice'));
  assert.ok(!isRetiredType('telnyx_voice'));
});

test('channelTypeOptions hides retired, paid and SaaS-only types on create', () => {
  const all = {
    webhook: {}, slack: {}, telegram: {}, telnyx_sms: {}, telnyx_voice: {},
    twilio_sms: { legacy: true }, twilio_voice: { legacy: true },
  };
  assert.deepEqual(channelTypeOptions(all, { edition: 'saas', paid: true }),
    ['webhook', 'slack', 'telegram', 'telnyx_sms', 'telnyx_voice']);
  assert.deepEqual(channelTypeOptions(all, { edition: 'saas', paid: false }), ['webhook', 'slack']);
  assert.deepEqual(channelTypeOptions(all, { edition: 'foss', paid: true }),
    ['webhook', 'slack', 'telegram']);
});

test('channelTypeOptions keeps the channel type on edit, even when create would hide it', () => {
  const all = { webhook: {}, telnyx_sms: {}, twilio_voice: { legacy: true } };
  assert.deepEqual(channelTypeOptions(all, { paid: false, editType: 'telnyx_sms' }), ['telnyx_sms']);
  assert.deepEqual(channelTypeOptions(all, { paid: true, editType: 'twilio_voice' }), ['twilio_voice']);
});

test('testErrorField points a refused destination at the URL field', () => {
  // notify.PublicDeliveryError for checker.ErrSSRFBlocked.
  const msg = 'Delivery failed: the destination address is not allowed.';
  assert.equal(testErrorField('webhook', msg).field, 'url');
  assert.equal(testErrorField('ntfy', msg).field, 'server_url');
  assert.equal(testErrorField('slack', msg), null);
  assert.equal(testErrorField('webhook', 'Delivery failed: the destination returned HTTP 5xx.'), null);
  assert.equal(testErrorField('webhook', undefined), null);
});

// A type with no JSON fields passes the text through untouched. Edition
// JSON fields are covered in their own tests.
test('parseJsonField and friends leave plain fields alone', () => {
  assert.deepEqual(parseJsonField('webhook', 'headers', 'anything'), { value: 'anything' });
  assert.deepEqual(parseJsonFields('slack', { channel_id: 'C1' }), { config: { channel_id: 'C1' }, errors: {} });
  assert.deepEqual(stringifyJsonFields('slack', { channel_id: 'C1' }), { channel_id: 'C1' });
});

test('configFieldError maps the typed 400 onto the form field', () => {
  assert.deepEqual(
    configFieldError('ntfy', { error: 'server_url: must be a valid URL', field: 'server_url' }, ['server_url', 'topic']),
    { field: 'server_url', message: 'Must be a valid URL' },
  );
  assert.equal(configFieldError('ntfy', { error: 'auth.token: is required', field: 'auth.token' }, ['server_url']), null);
  assert.deepEqual(
    configFieldError('webhook', { error: 'headers.X-Api_Key: has no saved value to keep; enter a value', field: 'headers.X-Api_Key' }, ['url', 'headers']),
    { field: 'headers', message: 'X-Api_Key has no saved value to keep; enter a value' },
  );
  assert.equal(configFieldError('webhook', { error: 'boom' }, ['url']), null);
  assert.equal(configFieldError('webhook', undefined, ['url']), null);
});

test('jsonFieldRows grows between 3 and 12 lines', () => {
  assert.equal(jsonFieldRows(''), 3);
  assert.equal(jsonFieldRows(undefined), 3);
  assert.equal(jsonFieldRows('a\nb\nc\nd\ne'), 5);
  assert.equal(jsonFieldRows('x\n'.repeat(40)), 12);
});
