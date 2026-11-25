// Pure helpers for the SMS and voice usage block in Billing (bill-6). Kept
// free of JSX so `npm test` can run them under node:test.

export const WARN_RATIO = 0.8;

function count(used) {
  return Math.max(0, Number(used) || 0);
}

// usageLevel classifies a counter: 'unlimited' (limit missing or -1), 'ok',
// 'warn' (80% or more) or 'full' (quota reached).
export function usageLevel(used, limit) {
  if (limit == null || limit < 0) return 'unlimited';
  const n = count(used);
  if (n >= limit) return 'full';
  if (n >= limit * WARN_RATIO) return 'warn';
  return 'ok';
}

// usageText is the visible count: '80 of 100', or '12 sent, unlimited'.
export function usageText(used, limit) {
  const n = count(used);
  if (limit == null || limit < 0) return `${n} sent, unlimited`;
  return `${n} of ${limit}`;
}

// usagePercent is the meter fill, clamped to 0..100. Unlimited is 0.
export function usagePercent(used, limit) {
  if (limit == null || limit <= 0) return 0;
  return Math.min(100, Math.round((count(used) / limit) * 100));
}

// usageValueText is the spoken value of the meter, so a screen reader hears
// the same thing the count shows plus the state.
export function usageValueText(noun, used, limit) {
  const level = usageLevel(used, limit);
  const base = `${usageText(used, limit)} ${noun} used`;
  if (level === 'unlimited') return `${count(used)} ${noun} sent, unlimited`;
  if (level === 'full') return `${base}, quota reached`;
  if (level === 'warn') return `${base}, ${usagePercent(used, limit)} percent`;
  return base;
}

// usageFlag is the pill next to the count: the real percent once the warning
// level is reached (rounded down, so 99.6% never reads as 100%), 'Quota
// reached' when full, otherwise empty.
export function usageFlag(used, limit) {
  const level = usageLevel(used, limit);
  if (level === 'full') return 'Quota reached';
  if (level !== 'warn') return '';
  return `${Math.floor((count(used) / limit) * 100)}% used`;
}

// hasMeter is false for unlimited and zero limits, where a bar has no range.
export function hasMeter(limit) {
  return limit != null && limit > 0;
}
