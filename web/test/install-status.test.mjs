// Checks how the Slack and Discord install sections read can_manage from
// the install status endpoints. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';

// can_manage from GET /integrations/{slack,discord}/installations decides
// whether Connect and Remove show; the role is only a fallback.
import { canManageInstalls as slackCanManage } from '../src/pages/settings/slack-app-state.mjs';
import { canManageInstalls as discordCanManage } from '../src/pages/settings/discord-app-state.mjs';

test('canManageInstalls prefers the server answer', () => {
  for (const fn of [slackCanManage, discordCanManage]) {
    assert.equal(fn({ available: true, can_manage: false, installations: [] }, true), false);
    assert.equal(fn({ available: true, can_manage: true, installations: [] }, false), true);
    assert.equal(fn({ available: true, installations: [] }, true), true);
    assert.equal(fn(null, false), false);
  }
});
