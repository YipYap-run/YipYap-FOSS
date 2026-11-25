import { useEffect } from 'preact/hooks';
import { wsStatus, wsStopReason, connectWS } from '../api/ws';
import { logout } from '../state/auth';
import { currentPath } from '../state/route';

const DOT_LABEL = {
  idle: 'Live updates off',
  connecting: 'Connecting live updates',
  live: 'Live updates on',
  reconnecting: 'Live updates reconnecting',
  stopped: 'Live updates stopped',
};

// Visible text for every state but "live", so the state is not shown by
// color alone (WCAG 1.4.1).
const DOT_TEXT = {
  connecting: 'Connecting',
  reconnecting: 'Reconnecting',
  stopped: 'Paused',
};

// Sidebar indicator for the live-update socket.
export function LiveStatusDot() {
  const status = wsStatus.value;
  const live = status === 'live';
  return (
    <span class="ws-status-wrap" title={DOT_LABEL[status]}>
      <span class={`ws-status ${live ? 'connected' : 'disconnected'}`} aria-hidden="true" />
      {DOT_TEXT[status]
        ? <span class="ws-status-text">{DOT_TEXT[status]}</span>
        : <span class="sr-only">{DOT_LABEL[status]}</span>}
    </span>
  );
}

const STOP_COPY = {
  expired: {
    text: 'Your session has ended, so live updates stopped.',
    action: 'signin',
  },
  disabled: {
    text: 'Your account is disabled, so live updates stopped. Ask an org admin to turn it back on.',
    action: 'signout',
  },
  password: {
    text: 'Live updates are paused until you change your password.',
    link: { href: '/settings/account', label: 'Change password' },
  },
  mfa: {
    text: 'Live updates are paused until you set up two-factor authentication.',
    link: { href: '/settings/security', label: 'Set up two-factor authentication' },
  },
  revoked: {
    text: 'Live updates stopped because this session no longer has access.',
    action: 'retry',
  },
};

function signOutTo(path) {
  logout().finally(() => { window.location.href = path; });
}

// Banner shown when the server refused the live-update session. Pages still
// load, but alerts and monitor changes no longer arrive on their own, so the
// user needs to know and needs a way to fix it.
export function LiveStatusBanner() {
  const status = wsStatus.value;
  const reason = wsStopReason.value;
  const path = currentPath.value;

  // After a password change or MFA setup the user moves on to another page.
  // Try again on navigation and when the tab regains focus; a still-refused
  // session stops again with the same reason.
  const retryable = status === 'stopped' && reason !== 'expired';
  useEffect(() => {
    if (!retryable) return undefined;
    const onVisible = () => {
      if (document.visibilityState === 'visible') connectWS();
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [retryable]);

  useEffect(() => {
    if (retryable) connectWS();
    // Only on navigation, not on the transition into "stopped".
  }, [path]);

  if (status !== 'stopped') return null;
  const copy = STOP_COPY[reason] || STOP_COPY.revoked;
  return (
    <div class="live-banner" role="alert">
      <span>{copy.text}</span>
      {copy.link && <a href={copy.link.href}>{copy.link.label}</a>}
      {copy.action === 'signin' && (
        <button type="button" class="live-banner-action" onClick={() => signOutTo('/login')}>
          Sign in again
        </button>
      )}
      {copy.action === 'signout' && (
        <button type="button" class="live-banner-action" onClick={() => signOutTo('/login')}>
          Sign out
        </button>
      )}
      {copy.action === 'retry' && (
        <button type="button" class="live-banner-action" onClick={() => connectWS()}>
          Try again
        </button>
      )}
    </div>
  );
}
