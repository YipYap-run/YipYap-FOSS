// Promo state as reported by GET /api/v1/meta (see promoMeta in
// internal/api/handlers/meta_full.go). Pure helpers so the banner and the
// billing page describe a promo the same way.

const PLAN_NAMES = { free: 'Free', pro: 'Pro', enterprise: 'Enterprise', promo: 'Promo' };

// Show the banner this many days before the promo ends.
export const PROMO_NOTICE_DAYS = 14;

export function planName(plan) {
  return PLAN_NAMES[plan] || 'Free';
}

export function formatPromoDate(iso) {
  if (!iso) return '';
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}

export function daysLeftText(days) {
  if (days == null) return '';
  if (days <= 0) return 'today';
  if (days === 1) return 'in 1 day';
  return `in ${days} days`;
}

// promoState normalizes the meta fields. Returns null when the org is not on
// a promo.
export function promoState(meta) {
  if (!meta || meta.plan !== 'promo') return null;
  return {
    expiresAt: meta.promo_expires_at || '',
    endsAt: meta.promo_ends_at || '',
    daysLeft: typeof meta.promo_days_remaining === 'number' ? meta.promo_days_remaining : null,
    inGrace: !!meta.promo_expiring,
    planAfter: meta.promo_plan_after || 'free',
    subscribed: !!meta.promo_subscribed,
    billingStartsAt: meta.promo_billing_starts_at || '',
  };
}

// promoNotice returns the banner content for the app layout, or null when no
// banner is due. canManage is true for owners and admins, who can act on it.
export function promoNotice(meta, canManage) {
  const p = promoState(meta);
  if (!p || !p.endsAt || p.daysLeft == null) return null;
  if (!p.inGrace && p.daysLeft > PROMO_NOTICE_DAYS) return null;
  // An Enterprise subscription keeps the limits the promo grants.
  if (p.subscribed && p.planAfter === 'enterprise') return null;

  const when = daysLeftText(p.daysLeft);
  const date = formatPromoDate(p.endsAt);
  const lead = p.inGrace
    ? `Your promotional plan has expired. Its limits stay in place until ${date} (${when}).`
    : `Your promotional plan ends ${when}, on ${date}.`;

  let next;
  let action = null;
  if (p.subscribed) {
    next = `After that your organization moves to your ${planName(p.planAfter)} subscription and Pro plan limits apply.`;
    if (canManage) action = 'Review billing';
  } else {
    next = 'After that your organization moves to the Free plan.';
    action = canManage ? 'Subscribe to keep paid features' : null;
    if (!canManage) next += ' Ask an owner or admin to subscribe.';
  }
  return { text: `${lead} ${next}`, action, urgent: p.inGrace, key: p.endsAt };
}
