// Monitor limits the SPA shows before the server is asked. The server is
// always the one that enforces them; these only shape the form and header.
//
// PLAN_LIMITS mirrors internal/billing/limits.go (MaxMonitors and
// MinCheckInterval). TestSPAPlanLimitsMatchBilling in internal/billing reads
// this object and fails when the two drift, so keep it plain JSON-like data.
// 0 means no cap.
export const PLAN_LIMITS = {
  free: { max_monitors: 5, min_check_interval_seconds: 300 },
  pro: { max_monitors: 500, min_check_interval_seconds: 30 },
  enterprise: { max_monitors: 0, min_check_interval_seconds: 10 },
  promo: { max_monitors: 0, min_check_interval_seconds: 10 },
};

// FOSS builds report operator caps in /api/v1/meta under limits. The floor
// default matches handlers.DefaultMinCheckIntervalSeconds.
const FOSS_DEFAULT_MIN_INTERVAL = 10;

// monitorLimits returns { maxMonitors, minInterval, source } for the
// current edition. source is 'server' for FOSS operator caps and 'plan' for
// SaaS plan limits. When the plan is not known yet (the org failed to load)
// plan is null and nothing is capped here; the server still enforces.
export function monitorLimits(meta, org) {
  if (meta?.edition === 'foss') {
    const l = meta.limits || {};
    return {
      source: 'server',
      maxMonitors: l.max_monitors_per_org > 0 ? l.max_monitors_per_org : 0,
      minInterval: l.min_check_interval_seconds > 0 ? l.min_check_interval_seconds : FOSS_DEFAULT_MIN_INTERVAL,
    };
  }
  const plan = org?.plan;
  const p = PLAN_LIMITS[plan];
  if (!p) {
    return { source: 'plan', plan: null, maxMonitors: 0, minInterval: PLAN_LIMITS.enterprise.min_check_interval_seconds };
  }
  return {
    source: 'plan',
    plan,
    maxMonitors: p.max_monitors,
    minInterval: p.min_check_interval_seconds,
  };
}

// Only links to the hosted product are turned into anchors in server
// messages. Other URLs in an error can echo user input, so they stay text.
const LINKABLE = /https:\/\/yipyap\.run\/[A-Za-z0-9/_-]*/g;

// serverMessageParts splits an API error message into text and link parts
// for safe rendering (no innerHTML). It also capitalizes the first letter,
// since Go error strings start lowercase.
export function serverMessageParts(message) {
  const msg = String(message || '').trim();
  if (!msg) return [];
  const text = msg.charAt(0).toUpperCase() + msg.slice(1);
  const parts = [];
  let last = 0;
  for (const m of text.matchAll(LINKABLE)) {
    if (m.index > last) parts.push({ text: text.slice(last, m.index) });
    parts.push({ text: m[0], href: m[0] });
    last = m.index + m[0].length;
  }
  if (last < text.length) parts.push({ text: text.slice(last) });
  return parts;
}
