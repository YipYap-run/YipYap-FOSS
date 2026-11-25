// Pure helpers for the Billing tab's post-checkout activation flow (bill-1,
// ux-4). Kept free of Preact so node --test can cover them.

// Stripe sends the browser back before its webhook has necessarily reached
// us, and the webhook handler now returns 500/409 until the event applies,
// so Stripe retries it. Poll quickly for about 90 seconds, then keep a slow
// background poll going so the page settles on its own once a retry lands.
export const FAST_POLL_MS = [2000, 3000, 5000, 5000, 10000, 10000, 15000, 15000, 15000, 15000];
export const SLOW_POLL_MS = 30000;
// Stop the background poll after about 15 minutes, which matches the support
// link threshold in the copy.
export const SLOW_POLL_ATTEMPTS = 26;

// Delay before the given poll attempt (0-based), or null when polling stops.
// The second value says whether the attempt belongs to the slow phase, where
// the UI shows the "still activating" notice.
export function pollDelay(attempt) {
  if (attempt < FAST_POLL_MS.length) return { ms: FAST_POLL_MS[attempt], slow: false };
  if (attempt < FAST_POLL_MS.length + SLOW_POLL_ATTEMPTS) return { ms: SLOW_POLL_MS, slow: true };
  return null;
}

// A plan bought through Stripe Checkout. Free and promo orgs have not
// finished a checkout yet. Matches the GET /billing contract pinned in
// internal/api/handlers/billing_get_test.go.
export function isStripePlanActive(b) {
  return !!b && typeof b.plan === 'string' && b.plan !== '' && b.plan !== 'free' && b.plan !== 'promo';
}

// Only follow redirect URLs that point at a secure origin. Plain http is
// allowed for localhost so a local Stripe mock still works.
export function safeRedirectURL(raw) {
  if (typeof raw !== 'string' || raw === '') return null;
  let u;
  try {
    u = new URL(raw);
  } catch (_) {
    return null;
  }
  if (u.protocol === 'https:') return u.href;
  if (u.protocol === 'http:' && (u.hostname === 'localhost' || u.hostname === '127.0.0.1')) return u.href;
  return null;
}

// Returns the query string without the checkout marker, plus whether the
// marker was present. The session id itself is never sent anywhere.
export function stripCheckoutMarker(search) {
  const params = new URLSearchParams(search || '');
  if (!params.has('session_id')) return { returned: false, search: search || '' };
  params.delete('session_id');
  const qs = params.toString();
  return { returned: true, search: qs ? `?${qs}` : '' };
}

// Text announced after a manual "Check again" that found the plan still
// inactive, so the click has visible and audible feedback.
// backgroundPolling says whether the page is still polling on its own.
export function checkAgainMessage(at, backgroundPolling) {
  const time = at.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  const tail = backgroundPolling
    ? 'This page keeps checking in the background.'
    : 'Check again later, or contact support if it has been more than 15 minutes.';
  return `Checked at ${time}. Not active yet. ${tail}`;
}
