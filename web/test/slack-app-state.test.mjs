// Checks the Slack workspace UI helpers against the API contract in
// internal/api/handlers/slack_app_full.go. Run with `npm test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import {
  STATUS_PARAM,
  statusFromSearch,
  searchWithoutStatus,
  shouldShowSection,
  installationsOf,
  isSlackAuthorizeURL,
  workspaceName,
  connectedLine,
  removePrompt,
  interpretInstallError,
  interpretRemoveError,
} from '../src/pages/settings/slack-app-state.mjs';

function apiErr(status, body = {}) {
  const e = new Error(body.error || 'HTTP ' + status);
  e.status = status;
  e.body = body;
  return e;
}

// Every status word the OAuth callback can redirect with (finish() calls in
// slack_app_full.go).
const CALLBACK_STATUSES = ['installed', 'cancelled', 'expired', 'forbidden', 'failed', 'unavailable'];

test('every callback status has a message, success and cancel are not errors', () => {
  assert.equal(STATUS_PARAM, 'slack');
  for (const s of CALLBACK_STATUSES) {
    const n = statusFromSearch('?slack=' + s);
    assert.ok(n, s);
    assert.equal(n.status, s);
    assert.ok(n.text.length > 0);
    assert.ok(!/\u2014/.test(n.text), 'no em-dash in copy');
  }
  assert.equal(statusFromSearch('?slack=installed').tone, 'success');
  assert.equal(statusFromSearch('?slack=cancelled').tone, 'info');
  assert.equal(statusFromSearch('?slack=expired').tone, 'error');
});

test('unknown or missing status shows nothing, so links cannot inject copy', () => {
  assert.equal(statusFromSearch(''), null);
  assert.equal(statusFromSearch('?channel=abc'), null);
  assert.equal(statusFromSearch('?slack=<script>alert(1)</script>'), null);
  assert.equal(statusFromSearch('?slack=toString'), null);
  assert.equal(statusFromSearch('?slack=__proto__'), null);
  assert.equal(statusFromSearch(undefined), null);
});

test('searchWithoutStatus drops only the slack parameter', () => {
  assert.equal(searchWithoutStatus('?slack=installed'), '');
  assert.equal(searchWithoutStatus('?slack=failed&channel=c1'), '?channel=c1');
  assert.equal(searchWithoutStatus('?channel=c1'), '?channel=c1');
  assert.equal(searchWithoutStatus(''), '');
});

test('section shows when the app is available or workspaces remain', () => {
  assert.equal(shouldShowSection(null), false); // 404 on self-hosted builds
  assert.equal(shouldShowSection({ available: false, installations: [] }), false);
  assert.equal(shouldShowSection({ available: true, installations: [] }), true);
  assert.equal(shouldShowSection({ available: false, installations: [{ id: 'i1' }] }), true);
  assert.deepEqual(installationsOf({ installations: [{ id: 'i1' }] }), [{ id: 'i1' }]);
  assert.deepEqual(installationsOf({}), []);
  assert.deepEqual(installationsOf(null), []);
});

test('only Slack consent URLs are followed', () => {
  assert.equal(isSlackAuthorizeURL('https://slack.com/oauth/v2/authorize?client_id=1&state=x'), true);
  assert.equal(isSlackAuthorizeURL('http://slack.com/oauth/v2/authorize?state=x'), false);
  assert.equal(isSlackAuthorizeURL('https://evil.example/oauth/v2/authorize'), false);
  assert.equal(isSlackAuthorizeURL('https://slack.com.evil.example/oauth/v2/authorize'), false);
  assert.equal(isSlackAuthorizeURL('https://user:pw@slack.com/oauth/v2/authorize'), false);
  assert.equal(isSlackAuthorizeURL('https://slack.com/api/auth.test'), false);
  assert.equal(isSlackAuthorizeURL('javascript:alert(1)'), false);
  assert.equal(isSlackAuthorizeURL('/oauth/v2/authorize'), false);
  assert.equal(isSlackAuthorizeURL(''), false);
  assert.equal(isSlackAuthorizeURL(undefined), false);
});

test('workspace rows read well with partial data', () => {
  const full = { id: 'i1', team_id: 'T1', team_name: 'Acme', installed_by_email: 'sam@example.com', created_at: '2026-11-24T15:30:47Z' };
  assert.equal(workspaceName(full), 'Acme');
  assert.equal(workspaceName({ team_id: 'T1', team_name: '  ' }), 'T1');
  assert.equal(workspaceName({}), 'Slack workspace');
  assert.equal(connectedLine(full, 'en-US'), 'Connected by sam@example.com on Nov 24, 2026');
  assert.equal(connectedLine({ ...full, installed_by_email: '' }, 'en-US'), 'Connected on Nov 24, 2026');
  assert.equal(connectedLine({ installed_by_email: 'sam@example.com', created_at: 'garbage' }), 'Connected by sam@example.com');
  assert.equal(connectedLine({}), 'Connected');
  // The token never reaches the list response, and the row never shows IDs
  // of users it cannot name.
  assert.ok(!connectedLine({ installed_by: 'u-123' }).includes('u-123'));
});

test('remove prompt names the workspace and explains shared workspaces', () => {
  const p = removePrompt({ team_name: 'Acme' });
  assert.equal(p.title, 'Disconnect Acme?');
  assert.match(p.body, /other YipYap organization/);
  assert.equal(p.confirm, 'Disconnect workspace');
});

test('remove prompt names the channels that will stop getting alerts', () => {
  assert.doesNotMatch(removePrompt({ team_name: 'Acme' }, []).body, /stop getting alerts/);
  assert.match(removePrompt({ team_name: 'Acme' }, ['ops']).body, /The channel ops posts here and will stop getting alerts/);
  const many = removePrompt({ team_name: 'Acme' }, ['a', 'b', 'c', 'd', 'e']).body;
  assert.match(many, /5 channels post here \(a, b, c and 2 more\)/);
});

test('install and remove errors map to plain sentences', () => {
  assert.match(interpretInstallError(apiErr(403)), /owners and admins/);
  assert.match(interpretInstallError(apiErr(503, { error: 'The Slack app is not configured on this server.' })), /not set up/);
  assert.match(interpretInstallError(apiErr(404)), /does not offer/);
  assert.equal(interpretInstallError(apiErr(500)), 'Could not start the Slack install. Try again.');
  assert.equal(interpretInstallError(new Error('Bad link')), 'Bad link');

  assert.match(interpretRemoveError(apiErr(404)), /already disconnected/);
  assert.match(interpretRemoveError(apiErr(502, { error: 'Slack did not confirm the uninstall. Try again.' })), /Slack did not confirm/);
  assert.match(interpretRemoveError(apiErr(403)), /owners and admins/);
  assert.equal(interpretRemoveError(apiErr(500)), 'Could not disconnect the workspace. Try again.');
});

test('the installed notice offers Add channel, error notices do not', () => {
  const n = statusFromSearch('?slack=installed');
  assert.equal(n.action, 'add-channel');
  assert.match(n.text, /Slack \(YipYap app\)/);
  for (const st of ['cancelled', 'expired', 'forbidden', 'failed', 'unavailable']) {
    assert.equal(statusFromSearch('?slack=' + st).action, undefined);
  }
});
