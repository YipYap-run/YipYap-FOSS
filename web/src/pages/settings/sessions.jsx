import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, del } from '../../api/client';
import { Modal, formatTime } from '../../components/ui';
import { logout } from '../../state/auth';
import { staffReadonly } from '../../edition/mfa';
import { describeDevice, signInMethodLabel, lastActiveLabel } from '../../utils/session-device';
import '../../styles/sessions.css';

// The session making the request has ended. The API client does not redirect
// on 401s from /auth/* paths, so do it here. Only a revoked session (signed out
// from another device, or by a password or role change) gets the "signed out"
// notice on the sign-in page; a plain expiry goes to the normal sign-in.
function isSessionEnded(err) {
  return err?.status === 401;
}

function endedRedirect(err) {
  window.location.href = err?.body?.code === 'session_revoked'
    ? '/login?reason=signed_out'
    : '/login';
}

function timeValue(ts) {
  const t = new Date(ts).getTime();
  return Number.isNaN(t) ? 0 : t;
}

function deviceName(s) {
  return describeDevice(s.user_agent) || 'Unknown device';
}

// SessionsTab lists the signed-in user's active sessions and lets them sign
// out any of them (decision web-presence 3, auth-12).
export function SessionsTab() {
  const [sessions, setSessions] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [busyId, setBusyId] = useState('');
  const [rowError, setRowError] = useState('');
  const [status, setStatus] = useState('');
  const [confirmOthers, setConfirmOthers] = useState(false);
  const [othersBusy, setOthersBusy] = useState(false);
  const [othersError, setOthersError] = useState('');
  const headingRef = useRef(null);

  // A staff impersonation session has no person behind it, and the server
  // refuses the list (auth-16). Say so plainly instead of showing an error.
  const readonly = staffReadonly.value;
  useEffect(() => {
    if (readonly) { setLoading(false); return; }
    load();
  }, [readonly]);

  async function load() {
    setLoading(true);
    setLoadError('');
    try {
      const data = await get('/auth/sessions');
      setSessions(Array.isArray(data?.sessions) ? data.sessions : []);
    } catch (err) {
      if (isSessionEnded(err)) { endedRedirect(err); return; }
      setSessions([]);
      setLoadError('Could not load your sessions.');
    }
    setLoading(false);
  }

  function focusHeading() {
    if (headingRef.current) headingRef.current.focus();
  }

  async function revoke(s) {
    if (busyId) return;
    const name = deviceName(s);
    setBusyId(s.id);
    setRowError('');
    setStatus('');
    if (s.current) {
      // End this session on the server first. The DELETE clears the cookie,
      // so only leave the page once the server confirms; a failed request
      // would otherwise look like a sign-out while the session stays valid.
      try {
        await del(`/auth/sessions/${encodeURIComponent(s.id)}`);
      } catch (err) {
        if (!isSessionEnded(err) && err?.status !== 404) {
          setRowError(err?.readonly ? err.message : 'Could not sign out of this device. Try again in a moment.');
          setBusyId('');
          return;
        }
      }
      await logout();
      window.location.href = '/login';
      return;
    }
    try {
      await del(`/auth/sessions/${encodeURIComponent(s.id)}`);
      setSessions(list => list.filter(x => x.id !== s.id));
      setStatus(`Signed out ${name}.`);
      // The button that was pressed is gone; keep focus in the section.
      focusHeading();
    } catch (err) {
      if (isSessionEnded(err)) { endedRedirect(err); return; }
      if (err?.status === 404) {
        setStatus('That session had already ended. The list has been refreshed.');
        await load();
        focusHeading();
      } else if (err?.readonly) {
        setRowError(err.message);
      } else {
        setRowError(`Could not sign out ${name}. Try again in a moment.`);
      }
    }
    setBusyId('');
  }

  function openOthers() {
    setOthersError('');
    setStatus('');
    setConfirmOthers(true);
  }

  function closeOthers() {
    if (othersBusy) return;
    setConfirmOthers(false);
    setOthersError('');
  }

  async function revokeOthers() {
    if (othersBusy) return;
    setOthersBusy(true);
    setOthersError('');
    try {
      await post('/auth/sessions/revoke-others', {});
      setConfirmOthers(false);
      setSessions(list => list.filter(x => x.current));
      setStatus('Signed out of all other sessions.');
      focusHeading();
    } catch (err) {
      if (isSessionEnded(err)) { endedRedirect(err); return; }
      setOthersError(err?.readonly ? err.message : 'Could not sign out the other sessions. Try again in a moment.');
    }
    setOthersBusy(false);
  }

  const others = sessions.filter(s => !s.current);
  // This device first, then most recently active.
  const ordered = [...sessions].sort((a, b) => {
    if (a.current !== b.current) return a.current ? -1 : 1;
    return timeValue(b.last_seen_at) - timeValue(a.last_seen_at);
  });
  return (
    <div class="sessions-tab">
      <section class="card" aria-labelledby="sessions-heading">
        <div class="section-header sessions-header">
          <h3 id="sessions-heading" class="sessions-title" tabIndex={-1} ref={headingRef}>Active sessions</h3>
          {!loading && !loadError && others.length > 0 && !readonly && (
            <button type="button" class="btn btn-sm btn-outline" onClick={openOthers}>
              Sign out of all other sessions
            </button>
          )}
        </div>
        <p class="sessions-muted">
          Browsers and devices signed in to your account. If you do not recognize one, sign it
          out and change your password.
        </p>

        <p class="sessions-sr-only" role="status" aria-live="polite">{status}</p>
        {status && <p class="sessions-success" aria-hidden="true">{status}</p>}
        {rowError && <p class="form-error" role="alert">{rowError}</p>}

        {readonly ? (
          <p class="sessions-muted">
            Sessions are not shown in a staff session. They belong to the person who signed
            in, so support staff cannot see or sign them out.
          </p>
        ) : loading ? (
          <p class="sessions-muted" role="status">Loading sessions...</p>
        ) : loadError ? (
          <div>
            <p class="form-error" role="alert">{loadError}</p>
            <button type="button" class="btn btn-sm btn-outline" onClick={load}>Try again</button>
          </div>
        ) : sessions.length === 0 ? (
          <p class="sessions-muted">No active sessions.</p>
        ) : (
          <>
          {others.length === 0 && (
            <p class="sessions-muted sessions-only">
              Only this device is signed in. Sessions on other browsers or devices will show up
              here.
            </p>
          )}
          <div class="sessions-table-wrap">
            {/* Explicit roles keep the table semantics when the narrow-screen CSS
                changes the display of the table elements (Safari drops them). */}
            <table class="sessions-table" role="table">
              <caption class="sessions-caption">
                Active sessions for your account, this device first
              </caption>
              <thead role="rowgroup">
                <tr role="row">
                  <th scope="col" role="columnheader">Device</th>
                  <th scope="col" role="columnheader">IP address</th>
                  <th scope="col" role="columnheader">Sign-in method</th>
                  <th scope="col" role="columnheader">Signed in</th>
                  <th scope="col" role="columnheader">Last active</th>
                  <th scope="col" role="columnheader"><span class="sessions-sr-only">Actions</span></th>
                </tr>
              </thead>
              <tbody role="rowgroup">
                {ordered.map(s => {
                  const name = deviceName(s);
                  const recognized = !!describeDevice(s.user_agent);
                  const active = s.current ? 'Active now' : lastActiveLabel(s.last_seen_at);
                  const activeCell = active === 'Last active time unknown'
                    ? 'Unknown'
                    : active.replace(/^Last active /, '');
                  const busy = busyId === s.id;
                  return (
                    <tr role="row" key={s.id} class={s.current ? 'sessions-row-current' : undefined}>
                      <th scope="row" role="rowheader" class="sessions-device" data-label="Device">
                        <span class="sessions-name">
                          <strong>{name}</strong>
                          {s.current && <span class="badge sessions-badge">This device</span>}
                        </span>
                        {!recognized && s.user_agent && (
                          <span class="sessions-ua">{s.user_agent}</span>
                        )}
                      </th>
                      <td role="cell" data-label="IP address">{s.ip || 'Unknown'}</td>
                      <td role="cell" data-label="Sign-in method">{signInMethodLabel(s.auth_method)}</td>
                      <td role="cell" data-label="Signed in">
                        <time dateTime={s.created_at}>{formatTime(s.created_at)}</time>
                      </td>
                      <td role="cell" data-label="Last active">
                        {s.current || activeCell === 'Unknown'
                          ? activeCell
                          : <time dateTime={s.last_seen_at}>{activeCell}</time>}
                      </td>
                      <td role="cell" class="sessions-action-cell">
                        {!readonly && (
                          <button type="button"
                            class={`btn btn-sm ${s.current ? 'btn-outline' : 'btn-danger'} sessions-action`}
                            aria-label={s.current
                              ? `Sign out of this device, ${name}`
                              : `Sign out session on ${name}, ${active.toLowerCase()}`}
                            onClick={() => revoke(s)}
                            aria-disabled={busyId ? 'true' : undefined}
                            aria-busy={busy ? 'true' : 'false'}>
                            {busy ? 'Signing out...' : 'Sign out'}
                          </button>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
          </>
        )}
      </section>

      <Modal open={confirmOthers} onClose={closeOthers} title="Sign out of all other sessions?">
        <p class="sessions-muted">
          {others.length === 1
            ? 'The other session will be signed out.'
            : `The other ${others.length} sessions will be signed out.`}{' '}
          This device stays signed in. Anyone using those sessions will need to sign in again.
        </p>
        {othersError && <p class="form-error" role="alert">{othersError}</p>}
        <div class="btn-group">
          <button type="button" class="btn btn-danger" onClick={revokeOthers}
            disabled={othersBusy} aria-busy={othersBusy ? 'true' : 'false'}>
            {othersBusy ? 'Signing out...' : 'Sign out other sessions'}
          </button>
          <button type="button" class="btn btn-outline" onClick={closeOthers} disabled={othersBusy}>Cancel</button>
        </div>
      </Modal>
    </div>
  );
}
