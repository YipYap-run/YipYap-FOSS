import { useState, useEffect, useRef } from 'preact/hooks';
import { get, put } from '../../api/client';
import { currentOrg } from '../../state/auth';
import { Modal, FormError } from '../../components/ui';

// Enforce SSO (decision web-presence 3). GET/PUT /org/sso-enforcement.
// Turning it on refuses password sign-in, passkey sign-in and password
// reset for everyone except the org owner, and signs out their non-SSO
// sessions. The server checks every rule; this section explains them,
// says up front what is missing, and asks for confirmation.

function errorText(err, fallback) {
  if (err?.readonly) return err.message;
  const msg = err?.body?.error;
  if (!msg) return fallback;
  return msg.charAt(0).toUpperCase() + msg.slice(1) + (/[.!?]$/.test(msg) ? '' : '.');
}

// setupVersion changes whenever a connection or domain changes on the SSO
// tab, so the Ready check follows a domain verified in the same view.
export function SSOEnforcement({ setupVersion = 0 }) {
  const [state, setState] = useState(null); // { enforced, ready }
  const [session, setSession] = useState(null); // { role, amr }
  const [loadError, setLoadError] = useState('');
  const [confirming, setConfirming] = useState(null); // 'on' | 'off' | null
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState('');
  const [status, setStatus] = useState('');
  const headingRef = useRef(null);

  async function load() {
    setLoadError('');
    try {
      setState(await get('/org/sso-enforcement'));
    } catch (err) {
      setLoadError(errorText(err, 'Could not load the Enforce SSO setting.'));
    }
  }

  useEffect(() => { load(); }, [setupVersion]);

  // How this admin signed in decides whether the server lets them turn it
  // on (an admin must be on an SSO session; the owner need not be). Ask
  // once so the page can say so before the click, not after.
  useEffect(() => {
    let live = true;
    get('/auth/session', { noRedirect: true })
      .then(info => { if (live) setSession({ role: info?.role || '', amr: info?.amr || '' }); })
      .catch(() => {});
    return () => { live = false; };
  }, []);

  async function apply(enforced) {
    if (saving) return;
    setSaving(true);
    setError('');
    try {
      const res = await put('/org/sso-enforcement', { enforced });
      setState({ enforced: res.enforced, ready: res.ready });
      if (currentOrg.value) currentOrg.value = { ...currentOrg.value, sso_enforced: res.enforced };
      if (enforced) {
        const n = res.revoked_sessions || 0;
        setStatus(n > 0
          ? `SSO is now enforced. ${n} password or passkey ${n === 1 ? 'session was' : 'sessions were'} signed out.`
          : 'SSO is now enforced. No one needed to be signed out.');
      } else {
        setStatus('SSO is no longer enforced. Members can sign in with a password or passkey again.');
      }
      setConfirming(null);
      // The button that opened the dialog is replaced, so put focus on the
      // section heading instead of letting it fall to the page body.
      setTimeout(() => { if (headingRef.current) headingRef.current.focus(); }, 0);
    } catch (err) {
      setError(errorText(err, 'Could not change the setting. Try again in a moment.'));
      if (err?.body?.code === 'sso_not_ready') load();
    }
    setSaving(false);
  }

  function open(which) {
    setError('');
    setStatus('');
    setConfirming(which);
  }

  function close() {
    if (saving) return;
    setConfirming(null);
    setError('');
  }

  if (loadError) {
    return (
      <section class="sso-enforcement" aria-labelledby="sso-enforce-heading">
        <h3 id="sso-enforce-heading">Enforce SSO</h3>
        <FormError>{loadError}</FormError>
        <button type="button" class="btn btn-sm btn-outline" onClick={load}>Try again</button>
      </section>
    );
  }
  if (!state) {
    return (
      <section class="sso-enforcement" aria-labelledby="sso-enforce-heading" aria-busy="true">
        <h3 id="sso-enforce-heading">Enforce SSO</h3>
        <p class="text-muted">Loading...</p>
      </section>
    );
  }

  const isOwner = session?.role === 'owner';
  // Only block on what we know. Without session info, the server decides.
  const needsSSOSession = !!session && !isOwner && session.amr !== 'sso';
  const canEnable = state.ready && !needsSSOSession;
  const hintId = !state.ready ? 'sso-enforce-not-ready' : needsSSOSession ? 'sso-enforce-session-hint' : undefined;

  return (
    <section class="sso-enforcement" aria-labelledby="sso-enforce-heading">
      <div class="section-header">
        <h3 id="sso-enforce-heading" ref={headingRef} tabIndex={-1}>Enforce SSO</h3>
        <span class={'label-tag sso-enforce-state' + (state.enforced ? ' is-on' : '')}>
          {state.enforced ? 'Enforced' : 'Not enforced'}
        </span>
      </div>
      <p class="text-muted">
        When SSO is enforced, everyone in your organization except the owner must sign in with
        SSO. Password sign-in, passkey sign-in and password reset stop working for them. API keys
        keep working.
      </p>
      <p class="text-muted">
        The owner keeps password and passkey sign-in as a way in if your identity provider is
        down.{isOwner ? ' That is you.' : ''}
      </p>

      {!state.enforced && !state.ready && (
        <p class="sso-enforce-hint" id="sso-enforce-not-ready">
          To turn this on, enable a connection above and verify at least one email domain on it.
        </p>
      )}
      {!state.enforced && state.ready && needsSSOSession && (
        <p class="sso-enforce-hint" id="sso-enforce-session-hint">
          You signed in with a password or passkey. Sign out, then sign in with SSO to turn this
          on. That shows SSO works for your account before password sign-in goes away.
        </p>
      )}

      <p class="sso-sr-only" role="status" aria-live="polite">{status}</p>
      {status && <p class="sso-status" aria-hidden="true">{status}</p>}

      {state.enforced ? (
        <button type="button" class="btn btn-sm btn-outline" onClick={() => open('off')}>
          Stop enforcing SSO
        </button>
      ) : (
        <button type="button" class="btn btn-sm btn-primary" onClick={() => open('on')}
                disabled={!canEnable} aria-describedby={hintId}>
          Enforce SSO
        </button>
      )}

      <Modal open={confirming === 'on'} onClose={close} title="Enforce SSO?">
        <ul class="sso-modal-list">
          <li>Everyone except the owner must sign in with SSO from now on.</li>
          <li>Anyone signed in with a password or passkey is signed out now.</li>
          <li>Password reset emails stop for everyone except the owner.</li>
          <li>
            People whose account is not linked to SSO yet can link it the next time they sign in
            with SSO, by confirming with their password once.
          </li>
          <li>API keys keep working.</li>
        </ul>
        <FormError id="sso-enforce-error">{error}</FormError>
        <div class="btn-group">
          <button type="button" class="btn btn-danger" onClick={() => apply(true)}
                  disabled={saving} aria-busy={saving ? 'true' : 'false'}>
            {saving ? 'Turning on...' : 'Enforce SSO'}
          </button>
          <button type="button" class="btn btn-outline" onClick={close} disabled={saving}>Cancel</button>
        </div>
      </Modal>

      <Modal open={confirming === 'off'} onClose={close} title="Stop enforcing SSO?">
        <p class="sso-modal-intro">
          Members can sign in with a password or passkey again, and reset a forgotten password.
          SSO sign-in keeps working.
        </p>
        <FormError id="sso-enforce-off-error">{error}</FormError>
        <div class="btn-group">
          <button type="button" class="btn btn-primary" onClick={() => apply(false)}
                  disabled={saving} aria-busy={saving ? 'true' : 'false'}>
            {saving ? 'Turning off...' : 'Stop enforcing'}
          </button>
          <button type="button" class="btn btn-outline" onClick={close} disabled={saving}>Cancel</button>
        </div>
      </Modal>
    </section>
  );
}
