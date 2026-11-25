// Promo banner copy, driven by the promo fields of /api/v1/meta
// (promo_phase, promo_expires_at, promo_days_remaining). Kept free of JSX so
// node --test can check it against the API contract in
// internal/api/meta_full_test.go.

// Format an RFC3339 timestamp as a long date in the viewer's locale.
export function formatPromoDate(iso, locale) {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(locale, { year: 'numeric', month: 'long', day: 'numeric' });
}

function plural(n, word) {
  return `${n} ${word}${n === 1 ? '' : 's'}`;
}

// promoBanner returns { lead, action, actionHref, tail } for the layout
// banner, or null when no banner should show. lead is always shown; admins
// get a link (action, actionHref) followed by tail; other members get tail
// only, which tells them who can act.
export function promoBanner(meta, isAdmin, locale) {
  if (!meta || meta.plan !== 'promo' || !meta.promo_expires_at) return null;
  const date = formatPromoDate(meta.promo_expires_at, locale);
  if (!date) return null;
  const days = Number(meta.promo_days_remaining) || 0;

  if (meta.promo_phase === 'warning') {
    return isAdmin
      ? { lead: `Your promo ends on ${date}.`, action: 'Add a payment method', actionHref: '/settings/billing#promo', tail: ' to keep Pro features.' }
      : { lead: `Your promo ends on ${date}.`, tail: ' Ask an owner or admin to add a payment method to keep Pro features.' };
  }
  if (meta.promo_phase === 'grace') {
    const lead = days > 0
      ? `Your promo ended on ${date}. Features switch to Free in ${plural(days, 'day')}.`
      : `Your promo ended on ${date}. Features switch to Free today.`;
    return isAdmin
      ? { lead, action: 'Add a payment method', actionHref: '/settings/billing#promo', tail: ' to keep them.' }
      : { lead, tail: ' Ask an owner or admin to add a payment method to keep them.' };
  }
  return null;
}

// promoCountdown returns "in N days", "in 1 day" or "today" for the
// promo_days_remaining of the warning and grace phases, or '' when there is
// no count to show.
export function promoCountdown(meta) {
  if (!meta || meta.promo_days_remaining == null) return '';
  if (meta.promo_phase !== 'warning' && meta.promo_phase !== 'grace') return '';
  const days = Number(meta.promo_days_remaining);
  if (!Number.isFinite(days) || days < 0) return '';
  return days === 0 ? 'today' : `in ${plural(days, 'day')}`;
}

// promoCardCopy returns the copy for the promo card on the Billing tab:
// the date labels, which date the countdown belongs to, and the line above
// the upgrade buttons. The grace countdown counts to the switch to Free;
// the warning countdown counts to the promo end, matching
// promo_days_remaining in /api/v1/meta.
export function promoCardCopy(meta) {
  const phase = meta?.promo_phase;
  const countdown = promoCountdown(meta);
  if (phase === 'grace') {
    return {
      endLabel: 'Promo ended',
      endCountdown: '',
      freeCountdown: countdown,
      cta: 'Add a payment method before the switch to Free to keep Pro features.',
    };
  }
  if (phase === 'warning') {
    return {
      endLabel: 'Promo ends',
      endCountdown: countdown,
      freeCountdown: '',
      cta: 'Add a payment method before the promo ends to keep Pro features.',
    };
  }
  return {
    endLabel: 'Promo ends',
    endCountdown: '',
    freeCountdown: '',
    cta: 'Ready to upgrade to a paid plan?',
  };
}

// metaIsStale reports whether /api/v1/meta was loaded more than maxAgeMs ago
// (or never). The promo phase and day count change daily, so a tab left open
// reloads meta when it becomes visible again.
export function metaIsStale(loadedAt, now, maxAgeMs) {
  if (!loadedAt) return true;
  return now - loadedAt > maxAgeMs;
}

// promoUpgradeCopy returns the line under "Upgrade to Pro" on the promo
// card. checkoutTrialEnd is checkout_trial_end from GET /api/v1/billing
// (internal/api/handlers/billing_promo_checkout_test.go): when set, the
// rest of the promo becomes a free trial and the first charge is on that
// date; when absent, checkout charges right away.
export function promoUpgradeCopy(checkoutTrialEnd, locale) {
  const addOn = 'Enterprise can be added on this page once the Pro subscription has started.';
  const date = checkoutTrialEnd ? formatPromoDate(checkoutTrialEnd, locale) : '';
  if (date) {
    return `Upgrade now and keep the rest of your promo as a free trial. You will not be charged until ${date}. ${addOn}`;
  }
  return `Billing starts when you complete checkout. ${addOn}`;
}
