import { test } from 'node:test';
import assert from 'node:assert/strict';
import { CONFIG_SENTINEL, isSavedSecret, hostLabel, savedURLText, writeOnlyURLSummary } from './channel-secrets-logic.mjs';

// The API contract: the sentinel is eight U+2022 bullets.
test('sentinel matches the API', () => {
  assert.equal(CONFIG_SENTINEL, '••••••••');
  assert.ok(isSavedSecret('••••••••'));
  assert.ok(!isSavedSecret('https://example.com'));
});

test('hidden URL shows only the host', () => {
  assert.equal(hostLabel('https://hooks.slack.com'), 'hooks.slack.com');
  assert.equal(savedURLText('https://hooks.slack.com'), 'Saved URL on hooks.slack.com');
  assert.equal(savedURLText(''), 'Saved URL');
  assert.equal(writeOnlyURLSummary({ url_set: true, url_host: 'https://discord.com' }), 'discord.com');
  assert.equal(writeOnlyURLSummary({ config: { url: 'https://example.com/a' } }), null);
});
