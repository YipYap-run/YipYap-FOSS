import { signal } from '@preact/signals';
import { get } from './client';

export const wsConnected = signal(false);
export const wsMessages = signal([]);

// Live-update state for the UI: 'idle' (signed out), 'connecting', 'live',
// 'reconnecting', or 'stopped' (the server refused this session).
export const wsStatus = signal('idle');
// Why live updates stopped, set with wsStatus 'stopped': 'expired',
// 'disabled', 'password', 'mfa', or 'revoked' (any other refusal).
export const wsStopReason = signal(null);

let ws = null;
let reconnectTimer = null;
let backoff = 1000;
const MAX_BACKOFF = 30000;

// Set by disconnectWS (logout) and cleared by connectWS (login). While set,
// no close event may schedule a reconnect.
let manualClose = false;

// Close code the server sends when it refuses the session (account
// disabled, MFA or password change required, session expired). The client
// checks the session before retrying and stops if it is refused, until the
// next connectWS call after login.
const POLICY_VIOLATION = 1008;

function detach(sock) {
  sock.onopen = null;
  sock.onmessage = null;
  sock.onclose = null;
  sock.onerror = null;
}

// Maps a refused session check to a stop reason. The browser hides the HTTP
// status of a rejected upgrade, so the client asks a gated API route the same
// question /ws asked. Returns null when the session looks usable or the check
// could not run (network or server error), meaning: keep retrying.
export function stopReasonFor(err) {
  if (!err || typeof err.status !== 'number') return null;
  if (err.status === 401) return 'expired';
  if (err.status !== 403) return null;
  const msg = String(err.body?.error || err.message || '').toLowerCase();
  if (msg.includes('disabled')) return 'disabled';
  if (msg.includes('password')) return 'password';
  if (msg.includes('mfa')) return 'mfa';
  // Staff read-only and plan-gate 403s do not block /org reads, so any
  // other 403 here is an access refusal we cannot name.
  return 'revoked';
}

async function checkSession() {
  try {
    await get('/org', { noRedirect: true });
    return null;
  } catch (err) {
    return stopReasonFor(err);
  }
}

function stop(reason) {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  wsStopReason.value = reason;
  wsStatus.value = 'stopped';
}

export function connectWS() {
  manualClose = false;
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  // A retry from the "stopped" state keeps showing the stop reason until the
  // socket actually opens, so the banner does not flicker.
  if (wsStatus.value === 'idle') wsStatus.value = 'connecting';

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  // The HttpOnly session cookie is sent automatically with the WebSocket
  // upgrade request. The server accepts the cookie only.
  const url = `${proto}//${location.host}/ws`;

  const sock = new WebSocket(url);
  ws = sock;
  let opened = false;

  sock.onopen = () => {
    opened = true;
    wsConnected.value = true;
    wsStatus.value = 'live';
    wsStopReason.value = null;
    backoff = 1000;
  };

  sock.onmessage = (evt) => {
    try {
      const msg = JSON.parse(evt.data);
      wsMessages.value = [msg, ...wsMessages.value.slice(0, 99)];
    } catch (_) {
      // ignore non-JSON messages
    }
  };

  sock.onclose = async (evt) => {
    // A socket replaced by a newer one must not touch shared state.
    if (ws !== sock) return;
    ws = null;
    wsConnected.value = false;
    if (manualClose) return;
    const wasStopped = wsStatus.value === 'stopped';
    if (!wasStopped) wsStatus.value = 'reconnecting';
    // A policy close, or a socket that never opened (a refused upgrade looks
    // like 1006 to the browser), may mean the session itself is gone. Ask the
    // API before retrying, so a dead session stops with a reason the UI can
    // show instead of retrying in silence.
    if (evt?.code === POLICY_VIOLATION || !opened) {
      const reason = await checkSession();
      if (manualClose || ws) return;
      if (reason) {
        stop(reason);
        return;
      }
    }
    wsStopReason.value = null;
    wsStatus.value = 'reconnecting';
    scheduleReconnect();
  };

  sock.onerror = () => {
    sock.close();
  };
}

function scheduleReconnect() {
  if (reconnectTimer || manualClose) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    if (manualClose) return;
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
    connectWS();
  }, backoff);
}

export function disconnectWS() {
  manualClose = true;
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (ws) {
    // Detach first so this socket's close event cannot schedule a
    // reconnect under whatever session the browser holds next.
    const sock = ws;
    ws = null;
    detach(sock);
    sock.close();
  }
  wsConnected.value = false;
  wsStatus.value = 'idle';
  wsStopReason.value = null;
  backoff = 1000;
}
