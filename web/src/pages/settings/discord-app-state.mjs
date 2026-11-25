/* Pure helpers for the Discord server section and the "Discord (YipYap
   app)" channel form. Kept free of Preact so `npm test` can check them
   against the API contract in internal/api/handlers/discord_app_full.go:

   - GET /integrations/discord/installations returns
     { available, can_manage, installations: [{ id, guild_id, guild_name, installed_by,
       installed_by_email, created_at }] }. Any signed-in member may read
     it. 404 on self-hosted builds.
   - GET /integrations/discord/install with Accept: application/json
     returns { url } and sets the install cookie. 403 for members and API
     keys, 503 when the app is not configured.
   - DELETE /integrations/discord/installations/{id} returns 204. 404 when
     the install is gone or belongs to another org, 502 when Discord did
     not confirm the bot left.
   - GET /integrations/discord/channels?installation_id=... returns
     { channels: [{ id, name, parent_id }] } sorted by name. Owners and
     admins only. 404 when the install is gone, 409 when the bot was
     removed from the server (the install is then revoked), 502 when
     Discord did not answer, 503 when the app is not configured.
   - A discord_app channel config is { installation_id, channel_id,
     channel_name }. The server rejects other fields, an install that is
     not this org's, and a channel outside the install's server, and takes
     channel_name from Discord (notification_channels_discordapp_full.go).
   - The OAuth callback redirects to /settings/channels?discord=<status>. */

export const STATUS_PARAM = 'discord';
export const DISCORD_APP_TYPE = 'discord_app';

/* "Server" in this copy always means a Discord server, never the YipYap
   host, so availability messages avoid the word. */
export const UNAVAILABLE = 'The YipYap Discord app is not available right now.';

const STATUS_MESSAGES = {
  installed: {
    tone: 'success',
    text: 'Discord server connected. Add a channel with the type Discord (YipYap app) to post alerts there.',
    action: 'add-channel',
  },
  cancelled: { tone: 'info', text: 'Discord install cancelled. Nothing was connected.' },
  expired: {
    tone: 'error',
    text: 'That install link expired or was already used. Start again with Add to Discord, in the same browser.',
  },
  forbidden: {
    tone: 'error',
    text: 'Only owners and admins can connect Discord. Your role may have changed during the install.',
  },
  failed: { tone: 'error', text: 'Discord did not finish the install. Try again in a minute.' },
  unavailable: { tone: 'error', text: UNAVAILABLE },
};

/* Reads ?discord= from a query string. Unknown values give null so a
   crafted link cannot put its own words on the page. */
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

/* The query string without ?discord=, keeping other parameters. */
export function searchWithoutStatus(search) {
  const params = new URLSearchParams(search || '');
  params.delete(STATUS_PARAM);
  const rest = params.toString();
  return rest ? '?' + rest : '';
}

export function installationsOf(data) {
  return data && Array.isArray(data.installations) ? data.installations : [];
}

/* Shown when the app is configured, or when servers are still connected
   so they can be removed after the app was switched off. */
export function shouldShowSection(data) {
  if (!data) return false;
  return !!data.available || installationsOf(data).length > 0;
}

/* Only send the browser to Discord's own consent page, never an open
   redirect from a tampered response. */
export function isDiscordAuthorizeURL(raw) {
  if (typeof raw !== 'string' || raw === '') return false;
  let u;
  try {
    u = new URL(raw);
  } catch (_) {
    return false;
  }
  return u.protocol === 'https:' && u.hostname === 'discord.com' &&
    (u.pathname === '/oauth2/authorize' || u.pathname === '/api/oauth2/authorize') &&
    u.username === '' && u.password === '';
}

export function serverName(inst) {
  if (!inst) return '';
  return (inst.guild_name || '').trim() || inst.guild_id || 'Discord server';
}

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

/* Copy for the inline confirmation before removing a server. usedBy is the
   names of the channels that post there. */
export function removePrompt(inst, usedBy) {
  const name = serverName(inst);
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
      ' If no other YipYap organization uses this server, the YipYap bot also leaves it. You can connect it again later.',
    confirm: 'Disconnect server',
  };
}

function serverMessage(err) {
  const msg = err && err.message;
  return msg && !/^HTTP \d+$/.test(msg) && msg !== 'Unauthorized' ? msg : '';
}

export function interpretInstallError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can connect Discord.';
  if (status === 503) return UNAVAILABLE;
  if (status === 404) return 'This edition of YipYap does not include the Discord app.';
  return serverMessage(err) || 'Could not start the Discord install. Try again.';
}

export function interpretRemoveError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can disconnect a Discord server.';
  if (status === 404) return 'This server was already disconnected. The list has been refreshed.';
  if (status === 502) return 'Discord did not confirm the disconnect. Try again.';
  return serverMessage(err) || 'Could not disconnect the server. Try again.';
}

/* ---- channel form ---- */

const SNOWFLAKE = /^[0-9]{15,20}$/;

/* The config sent to the API: only the three known fields, trimmed. */
export function discordAppConfig(config) {
  const c = config || {};
  return {
    installation_id: String(c.installation_id || '').trim(),
    channel_id: String(c.channel_id || '').trim(),
    channel_name: String(c.channel_name || '').trim().replace(/^#/, ''),
  };
}

/* Picking a server clears the channel, which belongs to the old one. */
export function withInstallation(config, installationId) {
  const prev = discordAppConfig(config);
  if (prev.installation_id === installationId) return prev;
  return { installation_id: installationId || '', channel_id: '', channel_name: '' };
}

export function withChannel(config, channels, channelId) {
  const prev = discordAppConfig(config);
  const ch = (channels || []).find(c => c.id === channelId);
  return { ...prev, channel_id: channelId || '', channel_name: ch ? ch.name : (channelId === prev.channel_id ? prev.channel_name : '') };
}

export function validateDiscordAppConfig(config) {
  const c = discordAppConfig(config);
  const errs = {};
  if (!c.installation_id) errs.installation_id = 'Choose a Discord server';
  if (!c.channel_id) errs.channel_id = 'Choose a channel';
  else if (!SNOWFLAKE.test(c.channel_id)) errs.channel_id = 'Choose a channel from the list';
  return errs;
}

/* Options for the channel select. A saved channel missing from the list
   (deleted, or hidden from the bot) stays as an option, marked. */
export function channelOptions(channels, config) {
  const list = Array.isArray(channels) ? channels.slice() : [];
  const c = discordAppConfig(config);
  if (c.channel_id && !list.some(ch => ch.id === c.channel_id)) {
    list.unshift({ id: c.channel_id, name: c.channel_name || c.channel_id, missing: true });
  }
  return list.map(ch => ({
    value: ch.id,
    label: '#' + ((ch.name || '').trim() || ch.id) + (ch.missing ? ' (not found in Discord)' : ''),
  }));
}

export function discordAppSummary(config, installations) {
  const c = discordAppConfig(config);
  if (!c.channel_id) return '';
  const where = c.channel_name ? '#' + c.channel_name : c.channel_id;
  const inst = (installations || []).find(i => i.id === c.installation_id);
  const server = inst && (inst.guild_name || inst.guild_id);
  return server ? where + ' in ' + server : where;
}

export function interpretChannelsError(err) {
  const status = err && err.status;
  if (status === 403) return 'Only owners and admins can load the Discord channel list.';
  if (status === 404) return 'This Discord server is no longer connected. Pick another one or connect it again above.';
  if (status === 409) return serverMessage(err) || 'The YipYap bot is no longer in this Discord server. Connect it again above.';
  if (status === 503) return UNAVAILABLE;
  return serverMessage(err) || 'Could not load channels from Discord. Try again.';
}

/* 'ok', 'disconnected', or 'unknown' while the install list loads. */
export function discordAppChannelHealth(channel, data) {
  if (!channel || channel.type !== DISCORD_APP_TYPE) return 'ok';
  if (!data || !Array.isArray(data.installations)) return 'unknown';
  const id = discordAppConfig(channel.config).installation_id;
  if (!id) return 'disconnected';
  return data.installations.some(i => i.id === id) ? 'ok' : 'disconnected';
}

export function channelsUsingInstall(channels, installationId) {
  if (!installationId) return [];
  return (channels || []).filter(ch =>
    ch && ch.type === DISCORD_APP_TYPE && discordAppConfig(ch.config).installation_id === installationId);
}

/* Offered for a new channel when the app is set up or a server is
   connected. Stays offered while loading and when editing. */
export function offerDiscordAppType(data, editing) {
  if (editing || !data) return true;
  return !!data.available || installationsOf(data).length > 0;
}

export function noServerMessage(data, canManage) {
  if (data && data.available === false) {
    return UNAVAILABLE + ' Choose the Discord type and use your own bot instead.';
  }
  return canManage
    ? 'No Discord server is connected yet. Close this form, use Add to Discord at the top of the Channels page, then add the channel.'
    : 'No Discord server is connected yet. Ask an owner or admin to connect one.';
}

/* Errors from the channel list that mean the install is gone: 404 (removed
   elsewhere) and 409 (the bot was kicked, and the API revoked the install).
   The form then reloads the server list so the section and list tags
   catch up. */
export function installGoneError(err) {
  const status = err && err.status;
  return status === 404 || status === 409;
}

/* Second line under a server in the list: how many channels post there. */
export function usageLine(count) {
  const n = Number(count) || 0;
  if (n <= 0) return 'No channel posts here yet';
  return n === 1 ? 'Used by 1 channel' : 'Used by ' + n + ' channels';
}

/* Starting config for a new channel opened from a server row or the
   "connected" notice. A known install is preselected. */
export function newChannelConfig(installationId) {
  return { installation_id: String(installationId || '').trim(), channel_id: '', channel_name: '' };
}

/* Whether the viewer may connect and remove installs. The server returns
   can_manage, which follows the same role gate as the install, remove and
   channel list routes; older servers without it fall back to the role. */
export function canManageInstalls(data, roleFallback) {
  return data && typeof data.can_manage === 'boolean' ? data.can_manage : !!roleFallback;
}
