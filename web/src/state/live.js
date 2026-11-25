import { useEffect, useRef } from 'preact/hooks';
import { wsMessages, wsStatus } from '../api/ws';

// useLiveRefresh calls refresh in the background when the server says
// something changed. Pages pass a quiet loader (no full-page spinner).
//
// Triggers:
//   - a WebSocket frame (alert.fired, alert.resolved, alert.changed; see
//     internal/api/ws.go). match(msg) narrows which frames count.
//   - the socket coming back after a drop, since frames sent while it was
//     down are lost.
//   - the tab becoming visible again after a frame arrived while hidden.
//
// Bursts are coalesced: the first trigger waits `delay` ms, later triggers
// in that window ride along, and a refresh never overlaps the previous one.
export function useLiveRefresh(refresh, { delay = 1000, match } = {}) {
  const refreshRef = useRef(refresh);
  refreshRef.current = refresh;
  const matchRef = useRef(match);
  matchRef.current = match;

  useEffect(() => {
    let timer = null;
    let disposed = false;
    let running = false;
    let again = false;
    let missedWhileHidden = false;
    let lastSeen = wsMessages.peek()[0];
    let prevStatus = wsStatus.peek();

    const hidden = () => typeof document !== 'undefined' && document.visibilityState === 'hidden';

    async function run() {
      timer = null;
      if (disposed) return;
      if (running) {
        again = true;
        return;
      }
      running = true;
      try {
        await refreshRef.current();
      } catch (_) {
        // A failed background refresh keeps the current view.
      } finally {
        running = false;
        if (again) {
          again = false;
          schedule();
        }
      }
    }

    function schedule() {
      if (disposed) return;
      if (hidden()) {
        missedWhileHidden = true;
        return;
      }
      if (timer) return;
      timer = setTimeout(run, delay);
    }

    const unsubMessages = wsMessages.subscribe((list) => {
      const head = list[0];
      if (!head || head === lastSeen) return;
      const fresh = [];
      for (const m of list) {
        if (m === lastSeen) break;
        fresh.push(m);
      }
      lastSeen = head;
      const fn = matchRef.current;
      if (!fn || fresh.some((m) => {
        try { return fn(m); } catch (_) { return false; }
      })) schedule();
    });

    const unsubStatus = wsStatus.subscribe((s) => {
      if (s === 'live' && prevStatus === 'reconnecting') schedule();
      prevStatus = s;
    });

    function onVisibility() {
      if (!hidden() && missedWhileHidden) {
        missedWhileHidden = false;
        schedule();
      }
    }
    document.addEventListener('visibilitychange', onVisibility);

    return () => {
      disposed = true;
      unsubMessages();
      unsubStatus();
      document.removeEventListener('visibilitychange', onVisibility);
      if (timer) clearTimeout(timer);
      timer = null;
    };
  }, [delay]);
}
