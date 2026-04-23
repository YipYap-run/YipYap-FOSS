import { signal } from '@preact/signals';

export const wsConnected = signal(false);
export const wsMessages = signal([]);

let ws = null;
let reconnectTimer = null;
let backoff = 1000;
const MAX_BACKOFF = 30000;

export function connectWS() {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }

  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
  // The HttpOnly session cookie is sent automatically with the WebSocket
  // upgrade request - no token in the URL needed (and no token to log).
  const url = `${proto}//${location.host}/ws`;

  ws = new WebSocket(url);

  ws.onopen = () => {
    wsConnected.value = true;
    backoff = 1000;
  };

  ws.onmessage = (evt) => {
    try {
      const msg = JSON.parse(evt.data);
      wsMessages.value = [msg, ...wsMessages.value.slice(0, 99)];
    } catch (_) {
      // ignore non-JSON messages
    }
  };

  ws.onclose = () => {
    wsConnected.value = false;
    ws = null;
    scheduleReconnect();
  };

  ws.onerror = () => {
    ws?.close();
  };
}

function scheduleReconnect() {
  if (reconnectTimer) return;
  reconnectTimer = setTimeout(() => {
    reconnectTimer = null;
    backoff = Math.min(backoff * 2, MAX_BACKOFF);
    connectWS();
  }, backoff);
}

export function disconnectWS() {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer);
    reconnectTimer = null;
  }
  if (ws) {
    ws.close();
    ws = null;
  }
  wsConnected.value = false;
}
