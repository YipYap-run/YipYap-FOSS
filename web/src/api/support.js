import { get } from './client';

// The impact options come from the server so the SPA never hardcodes the
// list or its mapping to priority. One request per page load is enough; a
// failed request is not cached so the next caller retries.
let impactsPromise = null;

export function loadSupportImpacts() {
  if (!impactsPromise) {
    impactsPromise = get('/support/impacts')
      .then(data => (Array.isArray(data?.impacts) ? data.impacts : []))
      .catch(err => {
        impactsPromise = null;
        throw err;
      });
  }
  return impactsPromise;
}

// Label for a stored impact value, or a readable fallback when the option
// list is not loaded or no longer has that value.
export function impactLabel(impacts, value) {
  if (!value) return '';
  const hit = (impacts || []).find(o => o.value === value);
  if (hit) return hit.label;
  const text = value.replace(/_/g, ' ');
  return text.charAt(0).toUpperCase() + text.slice(1);
}

function windowText(seconds) {
  if (!seconds || seconds <= 0) return 'a while';
  if (seconds >= 86400) return seconds === 86400 ? 'a day' : `${Math.round(seconds / 86400)} days`;
  if (seconds >= 3600) return seconds === 3600 ? 'an hour' : `${Math.round(seconds / 3600)} hours`;
  const m = Math.max(1, Math.round(seconds / 60));
  return m === 1 ? 'a minute' : `${m} minutes`;
}

// Text for a 429 from the ticket or message endpoints. The server sends the
// limit window as retry_after_seconds; the real wait can be shorter.
export function rateLimitText(err, kind) {
  const wait = windowText(err?.body?.retry_after_seconds);
  if (kind === 'ticket' && err?.body?.limit_scope === 'organization') {
    return `Your organization has opened a lot of tickets today, so new ones are paused for now. ` +
      `Add details to an open ticket instead, or try again within ${wait}.`;
  }
  if (kind === 'ticket') {
    return `You have opened a lot of tickets in a short time, so new ones are paused for now. ` +
      `Add details to one of your open tickets instead, or try again within ${wait}.`;
  }
  return `You have sent a lot of messages in a short time, so replies are paused for now. ` +
    `Your text is still here. Try again within ${wait}.`;
}

// Field errors come back as 400 {"error":"<field>: <message>","field":"<field>"}.
// Returns { field, message } with the field prefix removed, or null.
export function fieldErrorOf(err) {
  const field = err?.status === 400 ? err?.body?.field : '';
  if (!field) return null;
  let message = err.body.error || '';
  if (message.startsWith(`${field}: `)) message = message.slice(field.length + 2);
  return { field, message };
}

// Always-visible character counter for a capped text box. The server caps
// are the authority; maxLength on the box keeps the count from passing max.
export function charCount(value, max) {
  return `${(value || '').length} of ${max} characters`;
}
