// Checks the searchable channel picker helpers used by the Slack and
// Discord (YipYap app) channel forms. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  normalizeQuery,
  filterOptions,
  nextActive,
  selectedLabel,
  optionId,
  resultsMessage,
  escapeAction,
  reloadedMessage,
} from '../src/components/combobox-state.mjs';
import { channelOptions as slackOptions } from '../src/pages/settings/slack-app-channel-state.mjs';
import { channelOptions as discordOptions } from '../src/pages/settings/discord-app-state.mjs';

const opts = [
  { value: 'C1', label: '#general' },
  { value: 'C2', label: '#ops-alerts' },
  { value: 'C3', label: '#alerts' },
  { value: 'C4', label: '#Prod-Ops' },
];

test('normalizeQuery drops a leading # and case', () => {
  assert.equal(normalizeQuery('  #Ops '), 'ops');
  assert.equal(normalizeQuery('##x'), 'x');
  assert.equal(normalizeQuery(null), '');
});

test('filterOptions matches anywhere, prefix matches first', () => {
  assert.deepEqual(filterOptions(opts, '').map(o => o.value), ['C1', 'C2', 'C3', 'C4']);
  assert.deepEqual(filterOptions(opts, 'alerts').map(o => o.value), ['C3', 'C2']);
  assert.deepEqual(filterOptions(opts, '#OPS').map(o => o.value), ['C2', 'C4']);
  assert.deepEqual(filterOptions(opts, 'zzz'), []);
  assert.deepEqual(filterOptions(null, 'a'), []);
});

test('filterOptions does not mutate the input', () => {
  const copy = opts.slice();
  filterOptions(opts, 'ops');
  assert.deepEqual(opts, copy);
});

test('nextActive wraps arrows and clamps page keys', () => {
  assert.equal(nextActive(-1, 3, 'ArrowDown'), 0);
  assert.equal(nextActive(2, 3, 'ArrowDown'), 0);
  assert.equal(nextActive(0, 3, 'ArrowUp'), 2);
  assert.equal(nextActive(-1, 3, 'ArrowUp'), 2);
  assert.equal(nextActive(1, 30, 'PageDown'), 11);
  assert.equal(nextActive(25, 30, 'PageDown'), 29);
  assert.equal(nextActive(5, 30, 'PageUp'), 0);
  assert.equal(nextActive(0, 0, 'ArrowDown'), -1);
  assert.equal(nextActive(0, 3, 'Enter'), null);
});

test('selectedLabel finds the chosen option', () => {
  assert.equal(selectedLabel(opts, 'C2'), '#ops-alerts');
  assert.equal(selectedLabel(opts, ''), '');
  assert.equal(selectedLabel(opts, 'nope'), '');
});

test('optionId is a safe DOM id', () => {
  assert.equal(optionId('slack-ch', 'C123', 0), 'slack-ch-opt-0-C123');
  assert.equal(optionId('x', '"><img', 2), 'x-opt-2-___img');
});

test('resultsMessage reads well for screen readers', () => {
  assert.match(resultsMessage(1, 'ops'), /^1 channel match\./);
  assert.match(resultsMessage(3, ''), /^3 channels available\./);
  assert.equal(resultsMessage(0, 'zz'), 'No channels match.');
  assert.equal(resultsMessage(0, ''), 'No channels to choose from.');
});

// The picker filters on the labels both channel forms build.
test('works with Slack and Discord channel options', () => {
  const s = slackOptions([{ id: 'C1', name: 'ops', is_private: false }, { id: 'C2', name: 'eng' }], {});
  assert.deepEqual(filterOptions(s, 'op').map(o => o.value), ['C1']);
  const d = discordOptions([{ id: '1', name: 'alerts' }, { id: '2', name: 'general' }], { channel_id: '9', channel_name: 'old' });
  assert.equal(d[0].value, '9');
  assert.deepEqual(filterOptions(d, 'alert').map(o => o.value), ['1']);
});

test('Escape closes the list first, then clears the search, then lets the dialog close', () => {
  assert.equal(escapeAction(true, 'ops'), 'close');
  assert.equal(escapeAction(true, null), 'close');
  // After the first Escape the typed text is still there to clear.
  assert.equal(escapeAction(false, 'ops'), 'clear');
  assert.equal(escapeAction(false, ''), 'clear');
  // Nothing typed and the list closed: the dialog gets the key.
  assert.equal(escapeAction(false, null), null);
});

test('Reload announces the new channel count', () => {
  assert.equal(reloadedMessage(0), 'Channel list reloaded. No channels found.');
  assert.equal(reloadedMessage(1), 'Channel list reloaded. 1 channel.');
  assert.equal(reloadedMessage(12), 'Channel list reloaded. 12 channels.');
});
