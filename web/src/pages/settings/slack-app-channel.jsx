import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { ChannelCombobox } from '../../components/combobox.jsx';
import { reloadedMessage } from '../../components/combobox-state.mjs';
import { installationsOf, workspaceName, canManageInstalls } from './slack-app-state.mjs';
import {
  withInstallation,
  withChannel,
  channelOptions,
  interpretChannelsError,
  noWorkspaceMessage,
  slackAppChannelHealth,
} from './slack-app-channel-state.mjs';
import { slackInstallsData } from './slack-installs.js';
import './slack-app.css';

/* Workspace and channel pickers for a "Slack (YipYap app)" channel. The
   parent owns config ({ installation_id, channel_id, channel_name }) and
   errors; API contract: slack-app-channel-state.mjs. */
export function SlackAppChannelFields({ config, onChange, errors, canManage: roleCanManage }) {
  const [installs, setInstalls] = useState(null);
  const [installsData, setInstallsData] = useState(null);
  const [installsError, setInstallsError] = useState('');
  const [channels, setChannels] = useState([]);
  const [truncated, setTruncated] = useState(false);
  const [loadingChannels, setLoadingChannels] = useState(false);
  const [channelsError, setChannelsError] = useState('');
  const [announce, setAnnounce] = useState('');
  const installationId = config?.installation_id || '';
  // The server's can_manage wins over the role guess once the install list
  // is in, so per-org channel permissions need no change here.
  const canManage = installsData ? canManageInstalls(installsData, roleCanManage) : !!roleCanManage;

  useEffect(() => {
    let live = true;
    get('/integrations/slack/installations')
      .then(d => {
        if (!live) return;
        const list = installationsOf(d);
        setInstallsData(d);
        slackInstallsData.value = d;
        setInstalls(list);
        // One workspace: pick it so the channel list loads right away.
        if (!installationId && list.length === 1) onChange(withInstallation(config, list[0].id));
      })
      .catch(e => {
        if (!live) return;
        setInstalls([]);
        // 404: no platform Slack app on this build.
        if (e?.status === 404) setInstallsData({ available: false, installations: [] });
        else setInstallsError('Could not load Slack workspaces. Close the form and try again.');
      });
    return () => { live = false; };
  }, []);

  async function loadChannels(id, manual) {
    setAnnounce('');
    setChannels([]);
    setTruncated(false);
    setChannelsError('');
    if (!id || !canManage) return;
    setLoadingChannels(true);
    try {
      const d = await get('/integrations/slack/channels?installation_id=' + encodeURIComponent(id));
      const list = Array.isArray(d?.channels) ? d.channels : [];
      setChannels(list);
      if (manual) setAnnounce(reloadedMessage(list.length));
      setTruncated(!!d?.truncated);
    } catch (e) {
      setChannelsError(interpretChannelsError(e));
    } finally {
      setLoadingChannels(false);
    }
  }

  useEffect(() => { loadChannels(installationId); }, [installationId, canManage]);

  if (installs === null) {
    return <p class="form-help" aria-busy="true">Loading Slack workspaces{'…'}</p>;
  }

  const options = channelOptions(channels, config);
  const savedInstallMissing = installationId && !installs.some(i => i.id === installationId);

  return (
    <div class="slack-app-fields">
      {installsError && <div class="field-error" role="alert">{installsError}</div>}

      {installs.length === 0 && !installationId ? (
        !installsError && <p class="form-help" role="status">{noWorkspaceMessage(installsData, canManage)}</p>
      ) : (
        <>
          <div class={'form-group' + (errors?.installation_id ? ' has-error' : '')}>
            <label for="slack-app-workspace" class="required">Slack workspace</label>
            <select id="slack-app-workspace" value={installationId}
              aria-required="true"
              aria-invalid={errors?.installation_id ? 'true' : undefined}
              aria-describedby={errors?.installation_id ? 'slack-app-workspace-err' : undefined}
              onChange={e => onChange(withInstallation(config, e.target.value))}>
              <option value="">Choose a workspace</option>
              {savedInstallMissing && <option value={installationId}>Disconnected workspace</option>}
              {installs.map(i => <option key={i.id} value={i.id}>{workspaceName(i)}</option>)}
            </select>
            {errors?.installation_id && <div id="slack-app-workspace-err" class="field-error">{errors.installation_id}</div>}
            {savedInstallMissing && (
              <div class="field-error" role="alert">
                This channel's workspace was disconnected, so it gets no alerts. Pick a connected workspace and channel.
              </div>
            )}
          </div>

          <div class={'form-group' + (errors?.channel_id ? ' has-error' : '')}>
            <label for="slack-app-channel" id="slack-app-channel-label" class="required">Channel</label>
            <div class="slack-app-channel-row">
              <ChannelCombobox id="slack-app-channel" labelId="slack-app-channel-label"
                options={options}
                value={config?.channel_id || ''}
                onSelect={v => onChange(withChannel(config, channels, v))}
                disabled={!installationId || loadingChannels || !canManage}
                busy={loadingChannels}
                required
                invalid={!!errors?.channel_id}
                describedBy={'slack-app-channel-help' + (errors?.channel_id ? ' slack-app-channel-err' : '')}
                placeholder={loadingChannels ? 'Loading channels…' : 'Search channels'} />
              {canManage && (
                <button type="button" class="btn btn-sm"
                  onClick={() => { if (installationId && !loadingChannels) loadChannels(installationId, true); }}
                  disabled={!installationId}
                  aria-disabled={loadingChannels ? 'true' : undefined}
                  aria-label="Reload the channel list from Slack">
                  {loadingChannels ? 'Reloading…' : 'Reload'}
                </button>
              )}
            </div>
            {errors?.channel_id && <div id="slack-app-channel-err" class="field-error">{errors.channel_id}</div>}
            {channelsError && <div class="field-error" role="alert">{channelsError}</div>}
            <p class="sr-only" role="status">{announce}</p>
            <div id="slack-app-channel-help" class="form-help">
              Public channels work right away. For a private channel, invite @YipYap to it in Slack, then press Reload.
              {truncated && ' This workspace has more channels than the list can show, so some may be missing.'}
              {!canManage && ' Only owners and admins can load the channel list.'}
            </div>
          </div>
        </>
      )}
    </div>
  );
}

/* List tag for a Slack (YipYap app) channel whose workspace is no longer
   connected. Such a channel gets no alerts, and each skipped send shows as
   a failed delivery on the alert timeline. */
export function SlackAppDisconnectedTag({ channel }) {
  if (slackAppChannelHealth(channel, slackInstallsData.value) !== 'disconnected') return null;
  return (
    <span class="label-tag tag-slack-disconnected"
      title="This channel's Slack workspace was disconnected, so it gets no alerts. Open the channel to pick a connected workspace.">
      Workspace disconnected
    </span>
  );
}
