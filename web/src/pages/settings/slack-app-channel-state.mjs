/* Pure helpers for the "Slack (YipYap app)" channel form. Kept free of
   Preact so `npm test` can check them against the API contract:

   - A slack_app channel config is { installation_id, channel_id,
     channel_name }. The server rejects other fields, an install that is not
     this org's or is revoked, and a channel ID that is not a Slack ID
     (notification_channels_slackapp_full.go).
   - GET /integrations/slack/channels?installation_id=... returns
     { channels: [{ id, name, is_private, is_member }], truncated }, sorted
     by name. Owners and admins only. 404 when the install is gone or not
     this org's, 409 when Slack no longer accepts the install, 502 when
     Slack did not answer, 503 when the app is not configured
     (slack_app_channels_full.go). */

export const SLACK_APP_TYPE = 'slack_app';

const CHANNEL_ID = /^[CGD][A-Z0-9]{2,20}$/;

/* The config sent to the API: only the three known fields, trimmed. */
export function slackAppConfig(config) {
  const c = config || {};
  return {
    installation_id: String(c.installation_id || '').trim(),
    channel_id: String(c.channel_id || '').trim(),
    channel_name: String(c.channel_name || '').trim().replace(/^#/, ''),
  };
}

/* Picking a workspace clears the channel, which belongs to the old one. */
export function withInstallation(config, installationId) {
  const prev = slackAppConfig(config);
  if (prev.installation_id === installationId) return prev;
  return { installation_id: installationId || '', channel_id: '', channel_name: '' };
}

export function withChannel(config, channels, channelId) {
  const prev = slackAppConfig(config);
  const ch = (channels || []).find(c => c.id === channelId);
  return { ...prev, channel_id: channelId || '', channel_name: ch ? ch.name : (channelId === prev.channel_id ? prev.channel_name : '') };
}

/* Field errors keyed like the form fields. */
export function validateSlackAppConfig(config) {
  const c = slackAppConfig(config);
  const errs = {};
  if (!c.installation_id) errs.installation_id = 'Choose a Slack workspace';
  if (!c.channel_id) errs.channel_id = 'Choose a channel';
  else if (!CHANNEL_ID.test(c.channel_id)) errs.channel_id = 'Choose a channel from the list';
  return errs;
}

export function channelLabel(ch) {
  if (!ch) return '';
  const name = (ch.name || '').trim() || ch.id;
  return '#' + name + (ch.is_private ? ' (private)' : '');
}

/* Options for the channel select. When editing, the saved channel may be
   missing from the list (archived, or the bot left a private channel), so
   it is kept as an option rather than silently dropped. */
export function channelOptions(channels, config) {
  const list = Array.isArray(channels) ? channels.slice() : [];
  const c = slackAppConfig(config);
  if (c.channel_id && !list.some(ch => ch.id === c.channel_id)) {
    list.unshift({ id: c.channel_id, name: c.channel_name || c.channel_id, missing: true });
  }
  return list.map(ch => ({
    value: ch.id,
    label: ch.missing ? channelLabel(ch) + ' (not found in Slack)' : channelLabel(ch),
  }));
}

/* One line for the channel list. */
export function slackAppSummary(config, installations) {
  const c = slackAppConfig(config);
  if (!c.channel_id) return '';
  const where = c.channel_name ? '#' + c.channel_name : c.channel_id;
  const inst = (installations || []).find(i => i.id === c.installation_id);
  const team = inst && (inst.team_name || inst.team_id);
  return team ? where + ' in ' + team : where;
}

function serverMessage(err) {
  const msg = err && err.message;
  return msg && !/^HTTP \d+$/.test(msg) && msg !== 'Unauthorized' ? msg : '';
}

/* Maps a channel list failure to a sentence. */
export function interpretChannelsError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can load the Slack channel list.';
  if (status === 404) return 'This Slack workspace is no longer connected. Pick another one or connect it again above.';
  if (status === 409) return serverMessage(err) || 'Slack no longer accepts this workspace. Connect it again above.';
  if (status === 503) return 'The YipYap Slack app is not set up on this server.';
  return serverMessage(err) || 'Could not load channels from Slack. Try again.';
}

/* Whether a slack_app channel's workspace is still connected. data is the
   GET /integrations/slack/installations response (null while loading or on
   self-hosted builds). Returns 'unknown' until the list is in, so the list
   never flashes a false warning. */
export function slackAppChannelHealth(channel, data) {
  if (!channel || channel.type !== SLACK_APP_TYPE) return 'ok';
  if (!data || !Array.isArray(data.installations)) return 'unknown';
  const id = slackAppConfig(channel.config).installation_id;
  if (!id) return 'disconnected';
  return data.installations.some(i => i.id === id) ? 'ok' : 'disconnected';
}

/* Channels that post through one workspace, for the remove prompt. */
export function channelsUsingInstall(channels, installationId) {
  if (!installationId) return [];
  return (channels || []).filter(ch =>
    ch && ch.type === SLACK_APP_TYPE && slackAppConfig(ch.config).installation_id === installationId);
}

/* The Slack (YipYap app) type is offered for a new channel only when the
   server has the app set up or the org already has a workspace connected.
   While the list loads (data null) it stays offered; the form explains if
   nothing is connected. Editing an existing channel always keeps it. */
export function offerSlackAppType(data, editing) {
  if (editing || !data) return true;
  return !!data.available || (Array.isArray(data.installations) && data.installations.length > 0);
}

/* The sentence shown in the channel form when there is no workspace to pick. */
export function noWorkspaceMessage(data, canManage) {
  if (data && data.available === false) {
    return 'The YipYap Slack app is not set up on this server. Choose the Slack type and use your own bot instead.';
  }
  return canManage
    ? 'No Slack workspace is connected yet. Close this form, use Add to Slack at the top of the Channels page, then add the channel.'
    : 'No Slack workspace is connected yet. Ask an owner or admin to connect one.';
}
