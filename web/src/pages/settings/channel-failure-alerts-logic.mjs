// Validation for the channel failure email threshold. The server enforces
// the same rule; this gives the message before a round trip.

export const THRESHOLD_FALLBACK = { min: 1, max: 20, default: 3 };

// parseThreshold returns { value } for a whole number in [min, max], or
// { error } with a message to show next to the field.
export function parseThreshold(raw, bounds = THRESHOLD_FALLBACK) {
  const min = bounds?.min ?? THRESHOLD_FALLBACK.min;
  const max = bounds?.max ?? THRESHOLD_FALLBACK.max;
  const text = String(raw ?? '').trim();
  const msg = `Enter a whole number from ${min} to ${max}.`;
  if (!/^\d+$/.test(text)) return { error: msg };
  const n = Number(text);
  if (n < min || n > max) return { error: msg };
  return { value: n };
}

// thresholdSummary describes the current setting in one sentence.
export function thresholdSummary(n) {
  if (n === 1) return 'Owners are emailed after the first failed delivery.';
  return `Owners are emailed after ${n} failed deliveries in a row.`;
}
