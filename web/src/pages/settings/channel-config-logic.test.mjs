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
  prettyJsonField, jsonFieldRows, addEventTypePattern, acceptedRepliesError, CLOUDEVENT_TYPE_GROUPS,
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
  assert.equal(parseChannelServerError('cloudevent_http', 'control-plane URL cannot target private/internal IP addresses: http://10.0.0.1').field, 'sink_url');
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
    twilio_sms: { legacy: true }, twilio_voice: { legacy: true }, cloudevent_http: {},
  };
  assert.deepEqual(channelTypeOptions(all, { edition: 'saas', paid: true }),
    ['webhook', 'slack', 'telegram', 'telnyx_sms', 'telnyx_voice', 'cloudevent_http']);
  assert.deepEqual(channelTypeOptions(all, { edition: 'saas', paid: false }), ['webhook', 'slack']);
  assert.deepEqual(channelTypeOptions(all, { edition: 'foss', paid: true }),
    ['webhook', 'slack', 'telegram', 'cloudevent_http']);
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
  assert.equal(testErrorField('cloudevent_http', msg).field, 'sink_url');
  assert.equal(testErrorField('slack', msg), null);
  assert.equal(testErrorField('webhook', 'Delivery failed: the destination returned HTTP 5xx.'), null);
  assert.equal(testErrorField('webhook', undefined), null);
});

// Contract: internal/api/handlers/notification_channels_config.go decodes
// headers as {string: string}, event_types as [string], ce_overrides as
// {string: string}, and answers a bad field with { error: "<field>: ...", field }.
test('parseJsonField parses lists and maps and never falls back to empty', () => {
  assert.deepEqual(parseJsonField('cloudevent_http', 'event_types', '["run.yipyap.alert.*"]'), { value: ['run.yipyap.alert.*'] });
  assert.deepEqual(parseJsonField('cloudevent_http', 'event_types', '  '), { value: undefined });
  assert.match(parseJsonField('cloudevent_http', 'event_types', 'run.yipyap.alert.*').error, /^Must be valid JSON: a list like \["run\.yipyap\.alert\.\*"\]/);
  assert.match(parseJsonField('cloudevent_http', 'event_types', '{"a":"b"}').error, /a list like/);
  assert.match(parseJsonField('cloudevent_http', 'event_types', '["a", 3]').error, /strings only/);
  assert.match(parseJsonField('cloudevent_http', 'event_types', '["", "a"]').error, /empty/);
  assert.deepEqual(parseJsonField('cloudevent_http', 'ce_overrides', '{"env": "k"}'), { value: { env: 'k' } });
  assert.match(parseJsonField('cloudevent_http', 'ce_overrides', '{"env": "k"').error, /^Must be valid JSON: an object like/);
  assert.match(parseJsonField('cloudevent_http', 'ce_overrides', '["env"]').error, /an object like/);
  assert.match(parseJsonField('cloudevent_http', 'ce_overrides', '{"retry": 3}').error, /Values must be strings/);
  // Webhook headers are not a JSON box any more (row editor).
  assert.deepEqual(parseJsonField('webhook', 'headers', 'anything'), { value: 'anything' });
  assert.match(parseJsonField('cloudevent_http', 'ce_overrides', 'environment=prod').error, /valid JSON/);
  // A value that is already parsed (accepted as is).
  assert.deepEqual(parseJsonField('cloudevent_http', 'event_types', ['a.*']), { value: ['a.*'] });
});

test('parseJsonFields sends parsed values for every JSON field and drops empty ones', () => {
  const r = parseJsonFields('cloudevent_http', {
    sink_url: 'https://x', event_types: '["a.*"]', ce_overrides: '', mode: 'binary',
  });
  assert.deepEqual(r.errors, {});
  assert.deepEqual(r.config, { sink_url: 'https://x', event_types: ['a.*'], mode: 'binary' });
  const bad = parseJsonFields('cloudevent_http', { sink_url: 'https://x', ce_overrides: '{oops' });
  assert.ok(bad.errors.ce_overrides);
  assert.deepEqual(parseJsonFields('slack', { channel_id: 'C1' }), { config: { channel_id: 'C1' }, errors: {} });
});

test('stringifyJsonFields renders stored objects and lists as indented JSON', () => {
  const out = stringifyJsonFields('cloudevent_http', {
    sink_url: 'https://x', event_types: ['a.*', 'b.*'], ce_overrides: { environment: 'prod' },
    accepted_replies: { types: [] },
  });
  assert.equal(out.event_types, '[\n  "a.*",\n  "b.*"\n]');
  assert.equal(out.ce_overrides, '{\n  "environment": "prod"\n}');
  assert.deepEqual(out.accepted_replies, { types: [] }, 'non-text fields stay objects');
  // A legacy string is shown as is so validation can ask for a fix.
  assert.equal(stringifyJsonFields('cloudevent_http', { event_types: 'a,b' }).event_types, 'a,b');
});

test('configFieldError maps the typed 400 onto the form field', () => {
  const keys = ['sink_url', 'mode', 'event_types', 'ce_overrides', 'accepted_replies'];
  assert.deepEqual(
    configFieldError('cloudevent_http', { error: 'event_types: must be a JSON list of strings, like ["run.yipyap.alert.*"]', field: 'event_types' }, keys),
    { field: 'event_types', message: 'Must be a JSON list of strings, like ["run.yipyap.alert.*"]' },
  );
  assert.deepEqual(
    configFieldError('cloudevent_http', { error: 'accepted_replies.rate_limit: must be a whole number, 0 or more', field: 'accepted_replies.rate_limit' }, keys),
    { field: 'accepted_replies', message: 'Rate limit must be a whole number, 0 or more' },
  );
  assert.equal(configFieldError('cloudevent_http', { error: 'auth.oidc.client_id: is required', field: 'auth.oidc.client_id' }, keys), null);
  assert.deepEqual(
    configFieldError('webhook', { error: 'headers.X-Api_Key: has no saved value to keep; enter a value', field: 'headers.X-Api_Key' }, ['url', 'headers']),
    { field: 'headers', message: 'X-Api_Key has no saved value to keep; enter a value' },
  );
  assert.equal(configFieldError('webhook', { error: 'boom' }, ['url']), null);
  assert.equal(configFieldError('webhook', undefined, ['url']), null);
});

test('prettyJsonField indents valid values and leaves broken text alone', () => {
  assert.equal(prettyJsonField('cloudevent_http', 'ce_overrides', '{"X-A":"1"}'), '{\n  "X-A": "1"\n}');
  assert.equal(prettyJsonField('cloudevent_http', 'event_types', '["a.*"]'), '[\n  "a.*"\n]');
  assert.equal(prettyJsonField('cloudevent_http', 'ce_overrides', '{"X-A":'), '{"X-A":');
  // Wrong shape stays as typed so the inline error still points at it.
  assert.equal(prettyJsonField('cloudevent_http', 'event_types', '"a.*"'), '"a.*"');
  assert.equal(prettyJsonField('cloudevent_http', 'ce_overrides', ''), '');
});

test('jsonFieldRows grows between 3 and 12 lines', () => {
  assert.equal(jsonFieldRows(''), 3);
  assert.equal(jsonFieldRows(undefined), 3);
  assert.equal(jsonFieldRows('a\nb\nc\nd\ne'), 5);
  assert.equal(jsonFieldRows('x\n'.repeat(40)), 12);
});

test('addEventTypePattern appends to a valid list and refuses to overwrite broken text', () => {
  assert.deepEqual(JSON.parse(addEventTypePattern('', 'run.yipyap.alert.*')), ['run.yipyap.alert.*']);
  assert.deepEqual(JSON.parse(addEventTypePattern('["run.yipyap.monitor.*"]', 'run.yipyap.alert.*')),
    ['run.yipyap.monitor.*', 'run.yipyap.alert.*']);
  assert.deepEqual(JSON.parse(addEventTypePattern('["run.yipyap.alert.*"]', 'run.yipyap.alert.*')), ['run.yipyap.alert.*']);
  assert.equal(addEventTypePattern('run.yipyap.alert.*', 'run.yipyap.monitor.*'), null);
  assert.equal(addEventTypePattern('{"a":"b"}', 'run.yipyap.monitor.*'), null);
});

test('CLOUDEVENT_TYPE_GROUPS patterns are lists the API accepts', () => {
  for (const g of CLOUDEVENT_TYPE_GROUPS) {
    const r = parseJsonField('cloudevent_http', 'event_types', JSON.stringify([g.pattern]));
    assert.equal(r.error, undefined);
    for (const t of g.types) assert.ok(t.startsWith(g.pattern.slice(0, -1)), t);
  }
});

test('acceptedRepliesError mirrors the server whole-number rule', () => {
  assert.equal(acceptedRepliesError(undefined), null);
  assert.equal(acceptedRepliesError({ rate_limit: 0, max_suppress_seconds: 3600 }), null);
  assert.equal(acceptedRepliesError({ rate_limit: 1.5 }), 'Rate limit must be a whole number');
  assert.equal(acceptedRepliesError({ max_route_duration_seconds: -1 }), 'Route cap must be 0 or more');
  assert.equal(acceptedRepliesError({ max_remediation_seconds: '10' }), 'Remediation started cap must be a whole number');
});

test('configFieldError names reply caps by their label', () => {
  const r = configFieldError('cloudevent_http',
    { error: 'accepted_replies.max_suppress_seconds: must be a whole number, 0 or more', field: 'accepted_replies.max_suppress_seconds' },
    ['sink_url', 'accepted_replies']);
  assert.deepEqual(r, { field: 'accepted_replies', message: 'Suppressed cap must be a whole number, 0 or more' });
});
