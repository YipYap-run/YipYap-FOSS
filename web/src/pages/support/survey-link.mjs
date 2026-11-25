// Helpers for the public survey page that do not touch Preact, so they can be
// unit tested with node --test.

// The survey token lives in the URL fragment (#token=...&rating=N), which the
// browser never sends to the server. Older emails used the query string, so
// fall back to it. Callers strip both from the address bar right after.
export function readLinkParams(loc) {
  const hash = new URLSearchParams(String(loc.hash || '').replace(/^#/, ''));
  const query = new URLSearchParams(String(loc.search || ''));
  const token = hash.get('token') || query.get('token') || '';
  const raw = hash.get('rating') || query.get('rating') || '';
  const rating = /^[1-5]$/.test(raw) ? Number(raw) : 0;
  return { token, rating };
}

// Runs fn once the page has been visible for delayMs in one stretch. A link
// scanner or a background tab that never shows the page does not trigger it.
// If the page is hidden during the wait, the wait starts over when it is shown
// again. Returns a cancel function.
export function runWhenVisible(doc, delayMs, fn, timers = globalThis) {
  let timer = null;
  let done = false;

  function visible() {
    return !doc || doc.visibilityState === undefined || doc.visibilityState === 'visible';
  }
  function arm() {
    if (done || timer !== null || !visible()) return;
    timer = timers.setTimeout(() => {
      timer = null;
      if (done || !visible()) return;
      done = true;
      detach();
      fn();
    }, delayMs);
  }
  function onChange() {
    if (visible()) {
      arm();
    } else if (timer !== null) {
      timers.clearTimeout(timer);
      timer = null;
    }
  }
  function detach() {
    if (doc && doc.removeEventListener) doc.removeEventListener('visibilitychange', onChange);
  }

  if (doc && doc.addEventListener) doc.addEventListener('visibilitychange', onChange);
  arm();

  return function cancel() {
    done = true;
    if (timer !== null) {
      timers.clearTimeout(timer);
      timer = null;
    }
    detach();
  };
}

export function savedRatingNotice(rating) {
  return `Thanks, you rated ${rating}/5. Change it or add a comment below.`;
}

// Turns a survey API failure into a short message and the state the page
// should show. kind is one of: expired, reopened, busy, network, other.
// expired and reopened end the survey; the rest let the person try again.
export function describeSurveyError(err, fallback) {
  // The shared client throws a bare "Unauthorized" error for 401s.
  if (err?.status === 401 || err?.message === 'Unauthorized') {
    return { kind: 'expired', text: 'This survey link has expired or is not valid.' };
  }
  if (err?.status === 409) {
    return { kind: 'reopened', text: 'This ticket was reopened, so the survey is closed. You can rate it again after it is closed.' };
  }
  if (err?.status === 429) {
    return { kind: 'busy', text: `${fallback} Too many tries from this network. Wait a minute and try again.` };
  }
  if (err instanceof TypeError || !err?.status) {
    return { kind: 'network', text: 'Could not reach YipYap. Check your connection and try again.' };
  }
  if (err.status >= 500) {
    return { kind: 'other', text: `${fallback} Try again in a minute.` };
  }
  return { kind: 'other', text: fallback };
}

// True when the error means the survey can no longer be answered with this
// link, so the page should swap the form for an explanation.
export function endsSurvey(info) {
  return info?.kind === 'expired' || info?.kind === 'reopened';
}
