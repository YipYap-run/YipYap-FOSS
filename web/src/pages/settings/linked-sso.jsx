import { useState, useEffect } from 'preact/hooks';
import { get, del } from '../../api/client';
import { Modal } from '../../components/ui';
import { ssoLinksChanged } from '../../state/sso-link';
import { staffReadonly } from '../../edition/mfa';
import '../../styles/sso-link.css';

// describeUnlinkError turns an unlink failure into text a person can act on.
function describeUnlinkError(err, provider) {
  if (err?.body?.code === 'last_sign_in_method') {
    return `${provider} is the only way you can sign in. Set a password first (sign out and use Forgot password on the sign-in page), then unlink it.`;
  }
  if (err?.status === 404) return 'This identity is no longer linked. The list has been refreshed.';
  if (err?.readonly) return err.message;
  return `Could not unlink ${provider}. Try again in a moment.`;
}

// LinkedSSOSection lists the SSO identities linked to the signed-in user and
// lets them unlink one (decision core-auth 6). Linking only happens through
// the sign-in flow: choose Sign in with SSO, then confirm the offer.
export function LinkedSSOSection({ HeadingTag = 'h3' }) {
  const [links, setLinks] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [target, setTarget] = useState(null); // linked identity being removed
  const [busy, setBusy] = useState(false);
  const [unlinkError, setUnlinkError] = useState('');
  const [status, setStatus] = useState('');

  // Reload when the link banner links a new identity while this page is open.
  // A staff impersonation session has no person behind it, and the server
  // refuses the list (auth-16), so skip the request.
  const readonly = staffReadonly.value;
  const linksVersion = ssoLinksChanged.value;
  useEffect(() => {
    if (readonly) { setLoading(false); return; }
    load();
  }, [linksVersion, readonly]);

  async function load() {
    setLoading(true);
    setLoadError('');
    try {
      const data = await get('/auth/oidc/links');
      setLinks(Array.isArray(data) ? data : []);
    } catch (_) {
      setLinks([]);
      setLoadError('Could not load your linked SSO identities.');
    }
    setLoading(false);
  }

  function providerName(l) {
    return l?.provider || 'SSO';
  }

  function openUnlink(l) {
    setTarget(l);
    setUnlinkError('');
    setStatus('');
  }

  function closeUnlink() {
    if (busy) return;
    setTarget(null);
    setUnlinkError('');
  }

  async function confirmUnlink() {
    if (!target || busy) return;
    const name = providerName(target);
    setBusy(true);
    setUnlinkError('');
    try {
      await del(`/auth/oidc/links/${encodeURIComponent(target.id)}`);
      setTarget(null);
      setStatus(`${name} is unlinked.`);
      await load();
      // The Unlink button that opened the dialog is gone; put focus on the
      // section heading instead of <body> (WCAG 2.4.3).
      const heading = document.getElementById('sso-links-heading');
      if (heading) heading.focus();
    } catch (err) {
      setUnlinkError(describeUnlinkError(err, name));
      if (err?.status === 404) load();
    }
    setBusy(false);
  }

  return (
    <section class="card mfa-card-gap" aria-labelledby="sso-links-heading">
      <div class="section-header">
        <HeadingTag id="sso-links-heading" class="mfa-section-title" tabIndex={-1}>Linked SSO identities</HeadingTag>
      </div>
      <p class="mfa-muted mfa-section-body">
        Sign in with these identity providers instead of a password. To link one, sign out,
        choose Sign in with SSO and enter your work email, then confirm the offer after you
        sign in here.
      </p>

      <p class="mfa-sr-only" role="status" aria-live="polite">{status}</p>
      {status && <p class="mfa-success" aria-hidden="true">{status}</p>}

      {readonly ? (
        <p class="mfa-muted">
          Linked identities are not shown in a staff session. They belong to the person who
          signed in.
        </p>
      ) : loading ? (
        <p class="mfa-muted" role="status">Loading...</p>
      ) : loadError ? (
        <div class="mfa-section-body">
          <p class="form-error" role="alert">{loadError}</p>
          <button type="button" class="btn btn-sm btn-outline" onClick={load}>Try again</button>
        </div>
      ) : links.length === 0 ? (
        <p class="mfa-muted">No SSO identities linked.</p>
      ) : (
        <ul class="settings-list sso-links-list">
          {links.map(l => (
            <li class="settings-item" key={l.id}>
              <div class="sso-links-info">
                <strong>{providerName(l)}</strong>
                {!l.enabled && <span class="badge">Connection disabled</span>}
              </div>
              <button type="button" class="btn btn-danger btn-sm"
                aria-label={`Unlink ${providerName(l)}`}
                onClick={() => openUnlink(l)}>
                Unlink
              </button>
            </li>
          ))}
        </ul>
      )}

      <Modal open={!!target} onClose={closeUnlink} title={`Unlink ${providerName(target)}?`}>
        <p class="mfa-muted mfa-modal-intro">
          You will no longer be able to sign in with {providerName(target)}. You can link it
          again later by signing in with it.
        </p>
        {unlinkError && <p class="form-error" role="alert">{unlinkError}</p>}
        <div class="btn-group">
          <button type="button" class="btn btn-danger" onClick={confirmUnlink}
            disabled={busy} aria-busy={busy ? 'true' : 'false'}>
            {busy ? 'Unlinking...' : 'Unlink'}
          </button>
          <button type="button" class="btn btn-outline" onClick={closeUnlink} disabled={busy}>Cancel</button>
        </div>
      </Modal>
    </section>
  );
}
