/* Pure helpers for the Slack workspace section of the Channels tab. Kept
   free of Preact so `npm test` can check them against the API contract in
   internal/api/handlers/slack_app_full.go:

   - GET /integrations/slack/installations returns
     { available, can_manage, installations: [{ id, team_id, team_name, bot_user_id,
       scopes, installed_by, installed_by_email, created_at }] }. Any
     signed-in member may read it. 404 on self-hosted builds, which have no
     platform Slack app.
   - GET /integrations/slack/install with Accept: application/json returns
     { url } and sets the install cookie. 403 for members and API keys,
     503 when the app is not configured.
   - DELETE /integrations/slack/installations/{id} returns 204. 404 when
     the install is gone or belongs to another org, 502 when Slack did not
     confirm the uninstall.
   - The OAuth callback redirects to /settings/channels?slack=<status>. */

export const STATUS_PARAM = 'slack';

/* One message per callback status. tone picks the colors and the live
   region: success and info are polite, error interrupts. */
const STATUS_MESSAGES = {
  installed: {
    tone: 'success',
    text: 'Slack workspace connected. Add a channel with the type Slack (YipYap app) to post alerts there.',
    action: 'add-channel',
  },
  cancelled: {
    tone: 'info',
    text: 'Slack install cancelled. Nothing was connected.',
  },
  expired: {
    tone: 'error',
    text: 'That install link expired or was already used. Start again with Add to Slack, in the same browser.',
  },
  forbidden: {
    tone: 'error',
    text: 'Only owners and admins can connect Slack. Your role may have changed during the install.',
  },
  failed: {
    tone: 'error',
    text: 'Slack did not finish the install. Try again in a minute.',
  },
  unavailable: {
    tone: 'error',
    text: 'The YipYap Slack app is not set up on this server.',
  },
};

/* Reads ?slack= from a query string. Unknown values give null so a crafted
   link cannot put its own words on the page. */
export function statusFromSearch(search) {
  let value = null;
  try {
    value = new URLSearchParams(search || '').get(STATUS_PARAM);
  } catch (_) {
    return null;
  }
  if (!value || !Object.prototype.hasOwnProperty.call(STATUS_MESSAGES, value)) return null;
  return { status: value, ...STATUS_MESSAGES[value] };
}

/* Returns the query string without ?slack=, keeping other parameters such
   as ?channel=. Used with history.replaceState so a reload does not show
   the message again. */
export function searchWithoutStatus(search) {
  const params = new URLSearchParams(search || '');
  params.delete(STATUS_PARAM);
  const rest = params.toString();
  return rest ? '?' + rest : '';
}

/* The section shows when the server has the app configured, or when the
   org still has workspaces connected (so they can be removed even if the
   app was switched off). A null response means the endpoint is missing,
   as on self-hosted builds. */
export function shouldShowSection(data) {
  if (!data) return false;
  return !!data.available || (Array.isArray(data.installations) && data.installations.length > 0);
}

export function installationsOf(data) {
  return data && Array.isArray(data.installations) ? data.installations : [];
}

/* Only send the browser to Slack's own consent page. Guards against a
   misconfigured or tampered response turning the button into an open
   redirect. */
export function isSlackAuthorizeURL(raw) {
  if (typeof raw !== 'string' || raw === '') return false;
  let u;
  try {
    u = new URL(raw);
  } catch (_) {
    return false;
  }
  return u.protocol === 'https:' && u.hostname === 'slack.com' &&
    u.pathname.startsWith('/oauth/v2/authorize') && u.username === '' && u.password === '';
}

/* Name for a workspace row. Slack always sends a name, but fall back to
   the team ID rather than an empty row. */
export function workspaceName(inst) {
  if (!inst) return '';
  return (inst.team_name || '').trim() || inst.team_id || 'Slack workspace';
}

/* "Connected by sam@example.com on Nov 24, 2026", or without the person
   when the installer no longer exists. */
export function connectedLine(inst, locale) {
  if (!inst) return '';
  let when = '';
  const d = inst.created_at ? new Date(inst.created_at) : null;
  if (d && !isNaN(d.getTime())) {
    when = d.toLocaleDateString(locale, { year: 'numeric', month: 'short', day: 'numeric' });
  }
  const who = (inst.installed_by_email || '').trim();
  if (who && when) return 'Connected by ' + who + ' on ' + when;
  if (who) return 'Connected by ' + who;
  if (when) return 'Connected on ' + when;
  return 'Connected';
}

/* Copy for the inline confirmation before removing a workspace. usedBy is
   the names of the channels that post through it: they stop getting alerts
   (the escalation timeline records a failed delivery) until someone picks
   another workspace for them. */
export function removePrompt(inst, usedBy) {
  const name = workspaceName(inst);
  const names = Array.isArray(usedBy) ? usedBy.filter(Boolean) : [];
  let impact = '';
  if (names.length === 1) {
    impact = ' The channel ' + names[0] + ' posts here and will stop getting alerts.';
  } else if (names.length > 1) {
    const shown = names.slice(0, 3).join(', ');
    const more = names.length > 3 ? ' and ' + (names.length - 3) + ' more' : '';
    impact = ' ' + names.length + ' channels post here (' + shown + more + ') and will stop getting alerts.';
  }
  return {
    title: 'Disconnect ' + name + '?',
    body: 'YipYap stops posting to ' + name + ' for this organization.' + impact +
      ' If no other YipYap organization uses this workspace, the app is also removed from Slack. You can connect it again later.',
    confirm: 'Disconnect workspace',
  };
}

function serverMessage(err) {
  const msg = err && err.message;
  return msg && !/^HTTP \d+$/.test(msg) && msg !== 'Unauthorized' ? msg : '';
}

/* Maps an install start failure to a sentence. */
export function interpretInstallError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can connect Slack.';
  if (status === 503) return 'The YipYap Slack app is not set up on this server.';
  if (status === 404) return 'This server does not offer the YipYap Slack app.';
  return serverMessage(err) || 'Could not start the Slack install. Try again.';
}

/* Maps a remove failure to a sentence. */
export function interpretRemoveError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can disconnect a Slack workspace.';
  if (status === 404) return 'This workspace was already disconnected. The list has been refreshed.';
  if (status === 502) return 'Slack did not confirm the disconnect. Try again.';
  return serverMessage(err) || 'Could not disconnect the workspace. Try again.';
}

/* Whether the viewer may connect and remove installs. The server returns
   can_manage, which follows the same role gate as the install, remove and
   channel list routes; older servers without it fall back to the role. */
export function canManageInstalls(data, roleFallback) {
  return data && typeof data.can_manage === 'boolean' ? data.can_manage : !!roleFallback;
}
