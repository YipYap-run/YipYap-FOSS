import { useState, useEffect, useRef } from 'preact/hooks';
import { get, del } from '../../api/client';
import { ChannelCombobox } from '../../components/combobox.jsx';
import { reloadedMessage } from '../../components/combobox-state.mjs';
import { appMeta } from '../../state/auth';
import {
  statusFromSearch,
  searchWithoutStatus,
  shouldShowSection,
  installationsOf,
  canManageInstalls,
  isDiscordAuthorizeURL,
  serverName,
  connectedLine,
  removePrompt,
  interpretInstallError,
  interpretRemoveError,
  withInstallation,
  withChannel,
  channelOptions,
  interpretChannelsError,
  noServerMessage,
  discordAppChannelHealth,
  channelsUsingInstall,
  installGoneError,
  usageLine,
  UNAVAILABLE,
} from './discord-app-state.mjs';
import { discordInstallsData } from './discord-installs.js';
// Shares the layout of the Slack workspace section.
import './slack-app.css';

/* Discord servers connected through the platform Discord app, shown on the
   Channels tab under the Slack section. Owners and admins connect (OAuth
   bot install) and remove servers; members see the list. Hidden on
   self-hosted builds. API contract: discord-app-state.mjs.
   The install list lives in the discordInstallsData signal, so a server
   the channel form finds removed (and the API revokes) drops out here too.
   onAddChannel(installationId) opens the channel form preset to the
   Discord (YipYap app) type. */
export function DiscordServers({ canManage, channels, onAddChannel }) {
  const foss = appMeta.value?.edition === 'foss';
  const data = discordInstallsData.value;
  const [loading, setLoading] = useState(!foss);
  const [loadError, setLoadError] = useState('');
  const [notice, setNotice] = useState(() => statusFromSearch(typeof window !== 'undefined' ? window.location.search : ''));
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState('');
  const [confirmId, setConfirmId] = useState(null);
  const [removingId, setRemovingId] = useState(null);
  const [removeError, setRemoveError] = useState('');
  const [announce, setAnnounce] = useState('');
  const noticeRef = useRef(null);
  const headingRef = useRef(null);

  async function load() {
    setLoadError('');
    try {
      discordInstallsData.value = await get('/integrations/discord/installations');
    } catch (e) {
      if (e?.status === 404) discordInstallsData.value = null;
      else setLoadError('Could not load Discord servers.');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (foss) return;
    load();
  }, [foss]);

  useEffect(() => {
    if (!notice) return;
    try {
      const next = window.location.pathname + searchWithoutStatus(window.location.search) + window.location.hash;
      window.history.replaceState(window.history.state, '', next);
    } catch (_) { /* history blocked: the message still shows */ }
  }, []);

  useEffect(() => {
    if (notice && !loading && noticeRef.current) noticeRef.current.focus();
  }, [loading]);

  if (foss) return null;
  if (loading) {
    return (
      <section class="slack-app" aria-busy="true" aria-label="Discord servers">
        <p class="slack-app-muted">Loading Discord servers{'…'}</p>
      </section>
    );
  }
  if (loadError) {
    return (
      <section class="slack-app" aria-labelledby="discord-app-heading">
        <div class="slack-app-head">
          <h3 id="discord-app-heading">Discord servers</h3>
        </div>
        <div class="slack-app-msg slack-app-msg-error" role="alert">
          <span>{loadError}</span>
          <button type="button" class="btn btn-sm" onClick={() => { setLoading(true); load(); }}>Retry</button>
        </div>
      </section>
    );
  }
  if (!shouldShowSection(data)) {
    return notice
      ? <StatusNotice notice={{ ...notice, action: null }} noticeRef={noticeRef} onDismiss={() => setNotice(null)} />
      : null;
  }

  const list = installationsOf(data);
  // The server's answer wins over the role guess from the parent.
  canManage = canManageInstalls(data, canManage);
  const available = !!data.available;

  async function connect() {
    setConnectError('');
    setConnecting(true);
    try {
      const res = await get('/integrations/discord/install', { headers: { Accept: 'application/json' } });
      if (!res || !isDiscordAuthorizeURL(res.url)) {
        throw new Error('The server returned an unexpected install link. Try again or contact support.');
      }
      window.location.assign(res.url);
    } catch (e) {
      setConnectError(interpretInstallError(e));
      setConnecting(false);
    }
  }

  async function remove(inst) {
    setRemoveError('');
    setRemovingId(inst.id);
    try {
      await del('/integrations/discord/installations/' + encodeURIComponent(inst.id));
      const d = discordInstallsData.value;
      discordInstallsData.value = { ...d, installations: installationsOf(d).filter(i => i.id !== inst.id) };
      setConfirmId(null);
      setAnnounce(serverName(inst) + ' disconnected.');
      if (headingRef.current) headingRef.current.focus();
    } catch (e) {
      setRemoveError(interpretRemoveError(e));
      if (e?.status === 404) {
        setConfirmId(null);
        load();
      }
    } finally {
      setRemovingId(null);
    }
  }

  return (
    <section class="slack-app" aria-labelledby="discord-app-heading">
      <div class="slack-app-head">
        <h3 id="discord-app-heading" ref={headingRef} tabIndex={-1}>Discord servers</h3>
        {canManage && available && (
          <button type="button" class="btn btn-sm btn-primary" onClick={connect}
            disabled={connecting} aria-describedby="discord-app-lead">
            {connecting ? 'Opening Discord…' : list.length ? 'Add another server' : 'Add to Discord'}
          </button>
        )}
      </div>

      <p id="discord-app-lead" class="slack-app-muted">
        {available
          ? 'Post alerts with the YipYap Discord bot, with Acknowledge and Resolve buttons. Discord asks you to pick a server and approve the bot. Then add a channel with the type Discord (YipYap app).'
          : UNAVAILABLE + ' You can still disconnect servers.'}
      </p>

      {notice && (
        <StatusNotice notice={notice} noticeRef={noticeRef} onDismiss={() => setNotice(null)}
          onAction={canManage && onAddChannel && list.length > 0
            ? () => { setNotice(null); onAddChannel(list.length === 1 ? list[0].id : ''); }
            : null} />
      )}
      {connectError && <div class="slack-app-msg slack-app-msg-error" role="alert">{connectError}</div>}
      {removeError && <div class="slack-app-msg slack-app-msg-error" role="alert">{removeError}</div>}
      <p class="sr-only" role="status">{announce}</p>

      {list.length === 0 ? (
        <p class="slack-app-empty">
          {canManage ? 'No servers connected yet.' : 'No servers connected yet. Ask an owner or admin to connect one.'}
        </p>
      ) : (
        <ul class="slack-app-list" aria-label="Connected Discord servers">
          {list.map(inst => {
            const name = serverName(inst);
            const confirming = confirmId === inst.id;
            const usedBy = channelsUsingInstall(channels, inst.id);
            const prompt = confirming ? removePrompt(inst, usedBy.map(c => c.name)) : null;
            const busy = removingId === inst.id;
            return (
              <li key={inst.id} class="slack-app-item">
                <div class="slack-app-row">
                  <div class="slack-app-info">
                    <strong class="slack-app-name">{name}</strong>
                    <span class="slack-app-meta">{connectedLine(inst)}</span>
                    <span class="slack-app-meta">{usageLine(usedBy.length)}</span>
                  </div>
                  {canManage && !confirming && (
                    <div class="slack-app-actions">
                      {available && onAddChannel && (
                        <button type="button" class="btn btn-sm" onClick={() => onAddChannel(inst.id)}
                          aria-label={'Add channel in ' + name}>
                          Add channel
                        </button>
                      )}
                      <button type="button" class="btn btn-sm" onClick={() => { setRemoveError(''); setConfirmId(inst.id); }}
                        aria-label={'Disconnect ' + name}>
                        Disconnect
                      </button>
                    </div>
                  )}
                </div>
                {prompt && (
                  <div class="slack-app-confirm" role="group" aria-labelledby={'discord-rm-' + inst.id}>
                    <p id={'discord-rm-' + inst.id}><strong>{prompt.title}</strong></p>
                    <p>{prompt.body}</p>
                    <div class="slack-app-actions">
                      <button type="button" class="btn btn-sm btn-danger" onClick={() => remove(inst)} disabled={busy}>
                        {busy ? 'Disconnecting…' : prompt.confirm}
                      </button>
                      <button type="button" class="btn btn-sm" onClick={() => setConfirmId(null)} disabled={busy}>
                        Cancel
                      </button>
                    </div>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
      )}

      {!canManage && list.length > 0 && (
        <p class="slack-app-muted">Only owners and admins can add or disconnect servers.</p>
      )}
    </section>
  );
}

function StatusNotice({ notice, noticeRef, onDismiss, onAction }) {
  const isError = notice.tone === 'error';
  return (
    <div ref={noticeRef} tabIndex={-1} role={isError ? 'alert' : 'status'}
      class={'slack-app-msg slack-app-msg-' + notice.tone}>
      <span>{notice.text}</span>
      <span class="slack-app-actions">
        {notice.action === 'add-channel' && onAction && (
          <button type="button" class="btn btn-xs btn-primary" onClick={onAction}>
            Add channel
          </button>
        )}
        <button type="button" class="btn btn-xs" onClick={onDismiss} aria-label="Dismiss Discord message">
          Dismiss
        </button>
      </span>
    </div>
  );
}

/* Server and channel pickers for a "Discord (YipYap app)" channel. The
   parent owns config ({ installation_id, channel_id, channel_name }) and
   errors. */
export function DiscordAppChannelFields({ config, onChange, errors, canManage: roleCanManage }) {
  const [installs, setInstalls] = useState(null);
  const [installsData, setInstallsData] = useState(null);
  const [installsError, setInstallsError] = useState('');
  const [channels, setChannels] = useState([]);
  const [loadingChannels, setLoadingChannels] = useState(false);
  const [channelsError, setChannelsError] = useState('');
  const [announce, setAnnounce] = useState('');
  const installationId = config?.installation_id || '';
  // The server's can_manage wins over the role guess once the install list
  // is in, so per-org channel permissions need no change here.
  const canManage = installsData ? canManageInstalls(installsData, roleCanManage) : !!roleCanManage;

  const live = useRef(true);
  useEffect(() => () => { live.current = false; }, []);

  async function loadInstalls(first) {
    try {
      const d = await get('/integrations/discord/installations');
      if (!live.current) return;
      const list = installationsOf(d);
      setInstallsData(d);
      discordInstallsData.value = d;
      setInstalls(list);
      if (first && !installationId && list.length === 1) onChange(withInstallation(config, list[0].id));
    } catch (e) {
      if (!live.current || !first) return;
      setInstalls([]);
      if (e?.status === 404) setInstallsData({ available: false, installations: [] });
      else setInstallsError('Could not load Discord servers. Close the form and try again.');
    }
  }

  useEffect(() => { loadInstalls(true); }, []);

  async function loadChannels(id, manual) {
    setAnnounce('');
    setChannels([]);
    setChannelsError('');
    if (!id || !canManage) return;
    setLoadingChannels(true);
    try {
      const d = await get('/integrations/discord/channels?installation_id=' + encodeURIComponent(id));
      const list = Array.isArray(d?.channels) ? d.channels : [];
      setChannels(list);
      if (manual) setAnnounce(reloadedMessage(list.length));
    } catch (e) {
      setChannelsError(interpretChannelsError(e));
      // The server is gone (and a 409 revoked it): refresh the list so the
      // picker marks it disconnected and the section above drops it.
      if (installGoneError(e)) loadInstalls(false);
    } finally {
      setLoadingChannels(false);
    }
  }

  useEffect(() => { loadChannels(installationId); }, [installationId, canManage]);

  if (installs === null) {
    return <p class="form-help" aria-busy="true">Loading Discord servers{'…'}</p>;
  }

  const options = channelOptions(channels, config);
  const savedInstallMissing = installationId && !installs.some(i => i.id === installationId);

  return (
    <div class="slack-app-fields">
      {installsError && <div class="field-error" role="alert">{installsError}</div>}

      {installs.length === 0 && !installationId ? (
        !installsError && <p class="form-help" role="status">{noServerMessage(installsData, canManage)}</p>
      ) : (
        <>
          <div class={'form-group' + (errors?.installation_id ? ' has-error' : '')}>
            <label for="discord-app-server" class="required">Discord server</label>
            <select id="discord-app-server" value={installationId}
              aria-required="true"
              aria-invalid={errors?.installation_id ? 'true' : undefined}
              aria-describedby={errors?.installation_id ? 'discord-app-server-err' : undefined}
              onChange={e => onChange(withInstallation(config, e.target.value))}>
              <option value="">Choose a server</option>
              {savedInstallMissing && <option value={installationId}>Disconnected server</option>}
              {installs.map(i => <option key={i.id} value={i.id}>{serverName(i)}</option>)}
            </select>
            {errors?.installation_id && <div id="discord-app-server-err" class="field-error">{errors.installation_id}</div>}
            {savedInstallMissing && (
              <div class="field-error" role="alert">
                This channel's Discord server was disconnected, so it gets no alerts. Pick a connected server and channel.
              </div>
            )}
          </div>

          <div class={'form-group' + (errors?.channel_id ? ' has-error' : '')}>
            <label for="discord-app-channel" id="discord-app-channel-label" class="required">Channel</label>
            <div class="slack-app-channel-row">
              <ChannelCombobox id="discord-app-channel" labelId="discord-app-channel-label"
                options={options}
                value={config?.channel_id || ''}
                onSelect={v => onChange(withChannel(config, channels, v))}
                disabled={!installationId || loadingChannels || !canManage}
                busy={loadingChannels}
                required
                invalid={!!errors?.channel_id}
                describedBy={'discord-app-channel-help' + (errors?.channel_id ? ' discord-app-channel-err' : '')}
                placeholder={loadingChannels ? 'Loading channels…' : 'Search channels'} />
              {canManage && (
                <button type="button" class="btn btn-sm"
                  onClick={() => { if (installationId && !loadingChannels) loadChannels(installationId, true); }}
                  disabled={!installationId}
                  aria-disabled={loadingChannels ? 'true' : undefined}
                  aria-label="Reload the channel list from Discord">
                  {loadingChannels ? 'Reloading…' : 'Reload'}
                </button>
              )}
            </div>
            {errors?.channel_id && <div id="discord-app-channel-err" class="field-error">{errors.channel_id}</div>}
            {channelsError && <div class="field-error" role="alert">{channelsError}</div>}
            <p class="sr-only" role="status">{announce}</p>
            <div id="discord-app-channel-help" class="form-help">
              Text and announcement channels are listed. For a private channel, give the YipYap role access to it in Discord, then press Reload.
              {!canManage && ' Only owners and admins can load the channel list.'}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

/* List tag for a Discord (YipYap app) channel whose server is no longer
   connected. Such a channel gets no alerts, and each skipped send shows as
   a failed delivery on the alert timeline. */
export function DiscordAppDisconnectedTag({ channel }) {
  if (discordAppChannelHealth(channel, discordInstallsData.value) !== 'disconnected') return null;
  return (
    <span class="label-tag tag-slack-disconnected"
      title="This channel's Discord server was disconnected, so it gets no alerts. Open the channel to pick a connected server.">
      Server disconnected
    </span>
  );
}
