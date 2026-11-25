// SMS and voice usage for the Billing tab (bill-6).
//
// GET /billing returns sms_used, sms_limit, voice_used, voice_limit and
// usage_resets_at. A limit of -1 means unlimited. When a quota is reached the
// escalation engine skips that channel and moves on to the step's next
// channel, so the copy here says what happens instead of implying pages are
// lost.

import { useEffect, useRef } from 'preact/hooks';
import { usageLevel, usageText, usagePercent, usageValueText, usageFlag, hasMeter } from './usage-level.mjs';

export { usageLevel, usageText };

function formatResetDate(ts) {
  if (!ts || String(ts).startsWith('0001')) return '';
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function UsageRow({ id, label, noun, spoken, used, limit, resetText }) {
  const level = usageLevel(used, limit);
  const pct = usagePercent(used, limit);
  const until = resetText ? ` until ${resetText}` : ' until the usage period resets';
  const labelId = `${id}-label`;
  const countId = `${id}-count`;
  const flag = usageFlag(used, limit);

  return (
    <li class={`usage-row usage-${level}`}>
      <div class="usage-row-head">
        <span class="usage-label" id={labelId}>{label}</span>
        <span class="usage-count" id={countId}>
          {usageText(used, limit)}
          {flag && <span class="usage-flag">{flag}</span>}
        </span>
      </div>
      {hasMeter(limit) && (
        <div
          class="usage-bar"
          role="meter"
          aria-labelledby={labelId}
          aria-valuemin={0}
          aria-valuemax={limit}
          aria-valuenow={Math.min(Math.max(0, Number(used) || 0), limit)}
          aria-valuetext={usageValueText(spoken, used, limit)}
        >
          <span class="usage-bar-fill" style={{ width: `${pct}%` }} />
        </div>
      )}
      {level === 'warn' && (
        <p class="usage-note usage-note-warn">
          {noun} is close to the quota. Once it is reached, {noun.toLowerCase()} pages
          go to the next channel in each escalation step{until}. Make sure each step has
          another channel, such as email or chat.
        </p>
      )}
      {level === 'full' && (
        <p class="usage-note usage-note-full">
          {noun} pages are skipped{until}. Escalation moves on to the next channel in each step,
          and the alert timeline notes each skip. A step with no other channel sends nothing,
          so check your <a href="/settings/escalation">escalation steps</a>.
        </p>
      )}
    </li>
  );
}

export function SMSVoiceUsage({ billing, isOwnerOrAdmin }) {
  const headingRef = useRef(null);
  const visible = !!billing && !(billing.plan === 'free' && !billing.sms_used && !billing.voice_used) &&
    !(billing.sms_limit == null && billing.voice_limit == null);

  // The alert timeline links here as /settings/billing#usage. The block renders
  // after the billing fetch, so the browser cannot jump to it on its own.
  useEffect(() => {
    if (!visible || window.location.hash !== '#usage' || !headingRef.current) return;
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    headingRef.current.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' });
    headingRef.current.focus({ preventScroll: true });
  }, [visible]);

  if (!billing) return null;
  const smsLimit = billing.sms_limit;
  const voiceLimit = billing.voice_limit;
  const smsUsed = billing.sms_used || 0;
  const voiceUsed = billing.voice_used || 0;

  // Free orgs cannot add SMS or voice channels. Only show usage there when
  // channels kept from an earlier plan have sent something.
  if (billing.plan === 'free' && smsUsed === 0 && voiceUsed === 0) return null;
  // Older servers without limits: show nothing rather than a wrong number.
  if (smsLimit == null && voiceLimit == null) return null;

  const resetText = formatResetDate(billing.usage_resets_at);
  const unlimited = usageLevel(smsUsed, smsLimit) === 'unlimited' && usageLevel(voiceUsed, voiceLimit) === 'unlimited';
  const anyFull = usageLevel(smsUsed, smsLimit) === 'full' || usageLevel(voiceUsed, voiceLimit) === 'full';

  return (
    <section class="usage-section" id="usage" aria-labelledby="usage-heading">
      <h4 id="usage-heading" ref={headingRef} tabIndex={-1}>SMS and voice this period</h4>
      <ul class="usage-list">
        <UsageRow id="usage-sms" label="SMS" noun="SMS" spoken="SMS" used={smsUsed} limit={smsLimit} resetText={resetText} />
        <UsageRow id="usage-voice" label="Voice" noun="Voice" spoken="voice calls" used={voiceUsed} limit={voiceLimit} resetText={resetText} />
      </ul>
      {unlimited ? (
        <p class="form-help">Enterprise includes unlimited SMS and voice.</p>
      ) : (
        <p class="form-help">
          Pages over the quota are not dropped. Escalation sends them to the next channel in
          the step and notes the skip on the alert timeline.
          Owners and admins get an email at 80% and 100% of each quota.
          {resetText && ` Counters reset ${resetText}.`}
        </p>
      )}
      {anyFull && !unlimited && isOwnerOrAdmin && billing.plan !== 'free' && (
        <p class="form-help">Add Enterprise for unlimited SMS and voice.</p>
      )}
    </section>
  );
}
