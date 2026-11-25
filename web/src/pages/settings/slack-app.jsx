import { useState, useEffect, useRef } from 'preact/hooks';
import { get, del } from '../../api/client';
import { appMeta } from '../../state/auth';
import {
  statusFromSearch,
  searchWithoutStatus,
  shouldShowSection,
  installationsOf,
  canManageInstalls,
  isSlackAuthorizeURL,
  workspaceName,
  connectedLine,
  removePrompt,
  interpretInstallError,
  interpretRemoveError,
} from './slack-app-state.mjs';
import { channelsUsingInstall } from './slack-app-channel-state.mjs';
import { slackInstallsData } from './slack-installs.js';
import './slack-app.css';

/* Slack workspaces connected through the platform Slack app, shown at the
   top of the Channels tab. Owners and admins connect (OAuth install) and
   remove workspaces; members see the list. Self-hosted builds have no
   platform app, so the section stays hidden there. API contract:
   slack-app-state.mjs. */
export function SlackWorkspaces({ canManage, channels, onAddChannel }) {
  const foss = appMeta.value?.edition === 'foss';
  const [data, setData] = useState(null);
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
      const d = await get('/integrations/slack/installations');
      setData(d);
      slackInstallsData.value = d;
    } catch (e) {
      // 404: this build has no platform Slack app. Hide the section.
      if (e?.status === 404) { setData(null); slackInstallsData.value = null; }
      else setLoadError('Could not load Slack workspaces.');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (foss) return;
    load();
  }, [foss]);

  // Drop ?slack= from the address bar so a reload does not repeat the
  // message, and move focus to the message once it is on screen.
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
      <section class="slack-app" aria-busy="true" aria-label="Slack workspaces">
        <p class="slack-app-muted">Loading Slack workspaces{'…'}</p>
      </section>
    );
  }
  if (loadError) {
    return (
      <section class="slack-app" aria-labelledby="slack-app-heading">
        <div class="slack-app-head">
          <h3 id="slack-app-heading">Slack workspaces</h3>
        </div>
        <div class="slack-app-msg slack-app-msg-error" role="alert">
          <span>{loadError}</span>
          <button type="button" class="btn btn-sm" onClick={() => { setLoading(true); load(); }}>Retry</button>
        </div>
      </section>
    );
  }
  // A callback message still shows when the section itself is hidden (for
  // example "not set up on this server"), so the redirect never lands on a
  // page with no explanation.
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
      const res = await get('/integrations/slack/install', { headers: { Accept: 'application/json' } });
      if (!res || !isSlackAuthorizeURL(res.url)) {
        throw new Error('The server returned an unexpected install link. Try again or contact support.');
      }
      // Keep the busy state: the page is about to leave for Slack.
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
      await del('/integrations/slack/installations/' + encodeURIComponent(inst.id));
      setData(d => {
        const next = { ...d, installations: installationsOf(d).filter(i => i.id !== inst.id) };
        slackInstallsData.value = next;
        return next;
      });
      setConfirmId(null);
      setAnnounce(workspaceName(inst) + ' disconnected.');
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
    <section class="slack-app" aria-labelledby="slack-app-heading">
      <div class="slack-app-head">
        <h3 id="slack-app-heading" ref={headingRef} tabIndex={-1}>Slack workspaces</h3>
        {canManage && available && (
          <button type="button" class="btn btn-sm btn-primary" onClick={connect}
            disabled={connecting} aria-describedby="slack-app-lead">
            {connecting ? 'Opening Slack…' : list.length ? 'Add another workspace' : 'Add to Slack'}
          </button>
        )}
      </div>

      <p id="slack-app-lead" class="slack-app-muted">
        {available
          ? 'Post alerts with the YipYap Slack app instead of your own bot token, with Acknowledge and Resolve buttons. Slack asks you to pick a workspace and approve access. Then add a channel with the type Slack (YipYap app).'
          : 'The YipYap Slack app is switched off on this server. You can still disconnect workspaces.'}
      </p>

      {notice && (
        <StatusNotice notice={notice} noticeRef={noticeRef} onDismiss={() => setNotice(null)}
          onAction={canManage && available && onAddChannel && list.length > 0
            ? () => { setNotice(null); onAddChannel(list.length === 1 ? list[0].id : ''); }
            : null} />
      )}
      {connectError && <div class="slack-app-msg slack-app-msg-error" role="alert">{connectError}</div>}
      {removeError && <div class="slack-app-msg slack-app-msg-error" role="alert">{removeError}</div>}
      <p class="sr-only" role="status">{announce}</p>

      {list.length === 0 ? (
        <p class="slack-app-empty">
          {canManage
            ? 'No workspaces connected yet.'
            : 'No workspaces connected yet. Ask an owner or admin to connect one.'}
        </p>
      ) : (
        <ul class="slack-app-list" aria-label="Connected Slack workspaces">
          {list.map(inst => {
            const name = workspaceName(inst);
            const confirming = confirmId === inst.id;
            const prompt = confirming
              ? removePrompt(inst, channelsUsingInstall(channels, inst.id).map(c => c.name))
              : null;
            const busy = removingId === inst.id;
            return (
              <li key={inst.id} class="slack-app-item">
                <div class="slack-app-row">
                  <div class="slack-app-info">
                    <strong class="slack-app-name">{name}</strong>
                    <span class="slack-app-meta">{connectedLine(inst)}</span>
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
                  <div class="slack-app-confirm" role="group" aria-labelledby={'slack-rm-' + inst.id}>
                    <p id={'slack-rm-' + inst.id}><strong>{prompt.title}</strong></p>
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
        <p class="slack-app-muted">Only owners and admins can add or disconnect workspaces.</p>
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
        <button type="button" class="btn btn-xs" onClick={onDismiss} aria-label="Dismiss Slack message">
          Dismiss
        </button>
      </span>
    </div>
  );
}
