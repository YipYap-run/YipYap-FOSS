// Fetch helpers for the Reply Activity page that do not touch Preact, so they
// can be unit tested with node --test.

// How long the free-text filters wait after the last keystroke before they
// trigger a request.
export const FILTER_DEBOUNCE_MS = 300;

// buildReplyQuery returns the query string for GET /admin/cloudevents/replies.
// Free-text filters are trimmed so a stray space does not filter out every row.
export function buildReplyQuery({ since, channel, alert, replyType, limit }) {
  const params = new URLSearchParams();
  params.set('since', since);
  const ch = String(channel || '').trim();
  const al = String(alert || '').trim();
  if (ch) params.set('channel', ch);
  if (al) params.set('alert', al);
  if (replyType) params.set('type', replyType);
  params.set('limit', String(limit));
  return params.toString();
}

// createLatestRequest tracks one in-flight request at a time. start() aborts
// the previous request and returns a handle for the new one. Only the newest
// handle reports isCurrent(), so a slow response for an old filter can never
// overwrite the list for the current one, even if the server ignored the abort.
export function createLatestRequest(makeController = () => new AbortController()) {
  let seq = 0;
  let controller = null;
  return {
    start() {
      if (controller) controller.abort();
      const mine = ++seq;
      const ctrl = makeController();
      controller = ctrl;
      return {
        signal: ctrl.signal,
        isCurrent: () => mine === seq && !ctrl.signal.aborted,
      };
    },
    cancel() {
      seq++;
      if (controller) controller.abort();
      controller = null;
    },
  };
}

// isAbortError reports whether err came from an aborted fetch.
export function isAbortError(err) {
  return !!err && (err.name === 'AbortError' || err.code === 20);
}

// debounce returns a function that calls fn with the latest arguments once
// waitMs has passed with no further calls, plus cancel().
export function debounce(fn, waitMs, timers = globalThis) {
  let timer = null;
  function debounced(...args) {
    if (timer !== null) timers.clearTimeout(timer);
    timer = timers.setTimeout(() => {
      timer = null;
      fn(...args);
    }, waitMs);
  }
  debounced.cancel = () => {
    if (timer !== null) timers.clearTimeout(timer);
    timer = null;
  };
  return debounced;
}

// statusText is the screen reader summary for the polite live region above
// the list, so a filter change is announced once its results arrive.
export function statusText({ loading, error, count, hasMore }) {
  if (loading) return 'Loading replies…';
  if (error) return '';
  if (count === 0) return 'No replies found.';
  const noun = count === 1 ? 'reply' : 'replies';
  return hasMore ? `Showing the latest ${count} ${noun}. More are available.` : `Showing ${count} ${noun}.`;
}

// outcomeClass maps a raw audit outcome to its badge class in
// reply-activity.css. Unknown outcomes get the neutral style.
export function outcomeClass(outcome) {
  const o = outcome || '';
  if (o === 'accepted') return 'ra-accepted';
  if (o.startsWith('handler_error:')) return 'ra-failed';
  if (o.startsWith('rejected:')) return 'ra-rejected';
  return 'ra-other';
}
