import { useState } from 'preact/hooks';
import { currentUser } from '../state/auth';
import {
  ssoLinkOffer,
  currentSSOLinkOffer,
  acceptSSOLinkOffer,
  clearSSOLinkOffer,
  describeSSOLinkError,
} from '../state/sso-link';
import '../styles/sso-link.css';

// Removing the banner removes the focused button, so move focus to the page
// content instead of dropping it on <body> (WCAG 2.4.3).
function focusMain() {
  const main = document.querySelector('main.main-content');
  if (main) {
    if (!main.hasAttribute('tabindex')) main.setAttribute('tabindex', '-1');
    main.focus({ preventScroll: true });
  }
}

// SSOLinkBanner asks a signed-in user whether to link the SSO identity that
// matched their email (decision core-auth 6). Linking only happens on an
// explicit click; the server checks the offer belongs to this account.
export function SSOLinkBanner() {
  // Read the signal so the banner re-renders when the offer changes.
  ssoLinkOffer.value;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [linked, setLinked] = useState('');

  if (linked) {
    return (
      <div class="sso-link-banner sso-link-banner-ok" role="status">
        <span>
          {linked} is linked. You can now sign in with {linked}. Manage it under{' '}
          <a href="/settings/security">Settings, Security</a>.
        </span>
        <button type="button" class="sso-link-btn sso-link-btn-quiet"
          onClick={() => { setLinked(''); focusMain(); }}>
          Dismiss
        </button>
      </div>
    );
  }

  const offer = currentSSOLinkOffer();
  if (!currentUser.value) return null;
  // A failure that ends the offer (expired, already linked elsewhere) still
  // needs its message shown, without the Link button.
  if (!offer) {
    if (!error) return null;
    return (
      <div class="sso-link-banner" role="alert">
        <span class="sso-link-error-text">{error}</span>
        <button type="button" class="sso-link-btn sso-link-btn-quiet"
          onClick={() => { setError(''); focusMain(); }}>
          Dismiss
        </button>
      </div>
    );
  }
  const provider = offer.provider;

  async function onLink() {
    if (busy) return;
    setBusy(true);
    setError('');
    try {
      const res = await acceptSSOLinkOffer();
      setLinked(res?.provider || provider);
    } catch (err) {
      setError(describeSSOLinkError(err, provider));
    }
    setBusy(false);
  }

  function onDecline() {
    clearSSOLinkOffer();
    setError('');
    focusMain();
  }

  return (
    <section class="sso-link-banner" aria-labelledby="sso-link-title">
      <div class="sso-link-text">
        <h2 id="sso-link-title" class="sso-link-title">
          We found an existing account for this email. Link it to {provider} sign-in?
        </h2>
        <p>
          Someone tried to sign in with {provider} using the email on this account. Link it
          only if that was you.
        </p>
        {error && <p class="sso-link-error" role="alert">{error}</p>}
      </div>
      <div class="sso-link-actions">
        <button type="button" class="btn btn-primary btn-sm" onClick={onLink}
          disabled={busy} aria-busy={busy ? 'true' : 'false'}>
          {busy ? 'Linking...' : 'Link'}
        </button>
        <button type="button" class="btn btn-outline btn-sm" onClick={onDecline} disabled={busy}>
          {error ? 'Dismiss' : 'Not now'}
        </button>
      </div>
    </section>
  );
}
