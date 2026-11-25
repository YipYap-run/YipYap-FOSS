import { useEffect, useRef, useState } from 'preact/hooks';
import { post } from '../../api/client';
import { currentUser } from '../../state/auth';
import { FormError } from '../../components/ui';
import { describeEmailChangeError } from '../../utils/email-change';
import '../../styles/account-email.css';

// EmailChangeLinkPage redeems one of the two links an email change mails:
// the confirmation link sent to the new address, or the cancel link sent to
// the current one. Neither needs a session, so it works in any browser.
//
// The page waits for a click instead of posting on load. Mail security
// scanners open links and some run scripts; an automatic cancel would undo
// every change for anyone whose old mailbox is scanned.
function EmailChangeLinkPage({ endpoint, pageTitle, intro, action, working, onDone, describeError }) {
  const [token] = useState(() =>
    typeof window === 'undefined' ? '' : new URLSearchParams(window.location.search).get('token') || '');
  const [state, setState] = useState(token ? 'ready' : 'error'); // ready | working | ok | error
  const [result, setResult] = useState(null);
  const [message, setMessage] = useState(token ? '' : 'This link is missing part of its address. Open the link from the email again, or copy the whole link into your browser.');
  const resultRef = useRef(null);

  useEffect(() => { document.title = pageTitle + ' | YipYap'; }, []);

  // Move focus to the result so screen readers announce it.
  useEffect(() => {
    if (state === 'ok' || (state === 'error' && token)) resultRef.current?.focus();
  }, [state]);

  async function submit() {
    setState('working');
    try {
      const res = await post(endpoint, { token });
      setResult(onDone(res));
      setState('ok');
    } catch (err) {
      setMessage(err?.status === 429
        ? 'Too many attempts. Wait a minute and try again.'
        : describeError(err) || describeEmailChangeError(err) || 'This link did not work. Start the change again from Account settings.');
      setState('error');
    }
  }

  const signedIn = !!currentUser.value;
  const home = signedIn ? '/settings/account' : '/login';
  const homeLabel = signedIn ? 'Back to Account settings' : 'Go to sign in';

  return (
    <div class="auth-page">
      <main class="auth-card" aria-labelledby="email-change-title">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <h2 id="email-change-title" class="auth-title">{pageTitle}</h2>
        </div>
        {(state === 'ready' || state === 'working') && (
          <>
            <p style="color:var(--color-text-secondary);margin-bottom:16px">{intro}</p>
            <button type="button" class="btn btn-primary btn-full" onClick={submit}
              disabled={state === 'working'} aria-busy={state === 'working' ? 'true' : undefined}>
              {state === 'working' ? working : action}
            </button>
          </>
        )}
        {state === 'ok' && result && (
          <div ref={resultRef} tabIndex={-1} role="status">
            <p style="margin-bottom:16px">{result.text}</p>
            {result.extra}
            <a href={home} class="btn btn-primary btn-full">{homeLabel}</a>
          </div>
        )}
        {state === 'error' && (
          <div ref={resultRef} tabIndex={-1}>
            <FormError>{message}</FormError>
            <a href={home} class="btn btn-outline btn-full">
              {signedIn ? 'Back to Account settings' : 'Back to sign in'}
            </a>
          </div>
        )}
      </main>
    </div>
  );
}

export function ConfirmEmailChangePage() {
  return (
    <EmailChangeLinkPage
      endpoint="/auth/confirm-email-change"
      pageTitle="Confirm your new email"
      intro="Confirm to make this address the one you sign in with. Your old address stops working for sign-in and password resets."
      action="Confirm new email"
      working="Confirming..."
      describeError={(err) => err?.body?.code === 'email_taken'
        ? 'Another account started using this address after you asked for the change, so it cannot be used. Start the change again with a different address.'
        : ''}
      onDone={(res) => {
        if (currentUser.value && res?.email) {
          currentUser.value = { ...currentUser.value, email: res.email, pending_email: undefined, pending_email_requested_at: undefined };
        }
        return {
          text: res?.email
            ? `Your email is now ${res.email}. Use it to sign in from now on.`
            : 'Your email was changed. Use the new address to sign in from now on.',
        };
      }}
    />
  );
}

export function CancelEmailChangePage() {
  return (
    <EmailChangeLinkPage
      endpoint="/auth/cancel-email-change"
      pageTitle="Cancel email change"
      intro="Someone asked to change the email on your YipYap account. Cancel to keep your current email. If you made the request yourself and still want it, close this page."
      action="Cancel the change"
      working="Cancelling..."
      describeError={() => ''}
      onDone={() => {
        if (currentUser.value) {
          currentUser.value = { ...currentUser.value, pending_email: undefined, pending_email_requested_at: undefined };
        }
        const signedIn = !!currentUser.value;
        return {
          text: 'The change was cancelled and your email stays the same.',
          extra: (
            <p style="margin-bottom:16px;color:var(--color-text-secondary)">
              If you did not ask for it, someone may know your password.{' '}
              {signedIn
                ? <a href="/settings/account">Change your password</a>
                : <a href="/forgot-password">Reset your password</a>}
              {' '}and review your <a href="/settings/sessions">active sessions</a>.
            </p>
          ),
        };
      }}
    />
  );
}
