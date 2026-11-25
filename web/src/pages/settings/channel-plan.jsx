// Paid channel status and refused Test notes on the notification channel
// list. All text is plain text from channel-plan-logic.mjs or the API's
// error field; nothing is rendered as HTML.
import { useState, useEffect, useCallback } from 'preact/hooks';
import { currentOrg, currentUser, appMeta } from '../../state/auth';
import { channelPlanView, paidChannelsBanner } from './channel-plan-logic.mjs';
import './channel-plan.css';

// Billing changes need owner or admin (routes_full.go), and the billing
// tab only exists when billing is configured.
function upgradeAccess() {
  return {
    canUpgrade: ['owner', 'admin'].includes(currentUser.value?.role),
    billingEnabled: !!appMeta.value?.billing_enabled,
  };
}

// statusFor prefers the server's plan_status and grace_ends_at on the
// channel row and falls back to the org's plan fields.
function statusFor(channel) {
  return channelPlanView(channel, currentOrg.value, { edition: appMeta.value?.edition });
}

// ChannelPlanBadge marks a paid channel that is in its grace period or has
// stopped. The word carries the meaning; the hidden prefix gives screen
// readers the context the layout gives sighted users.
export function ChannelPlanBadge({ channel }) {
  const v = statusFor(channel);
  if (!v) return null;
  return (
    <span class={`channel-plan-badge channel-plan-${v.status}`}>
      <span class="channel-health-sr">Plan status: </span>{v.badge}
    </span>
  );
}

export function ChannelPlanLine({ channel }) {
  const v = statusFor(channel);
  if (!v) return null;
  const { canUpgrade, billingEnabled } = upgradeAccess();
  return (
    <span class={`channel-plan-line channel-plan-line-${v.status}`}>
      {v.detail}
      {billingEnabled && (
        <>
          {' '}
          <a href="/settings/billing" onClick={e => e.stopPropagation()}>
            {canUpgrade ? 'Upgrade' : 'View plan'}
            <span class="channel-health-sr">{canUpgrade ? ` to Pro for ${channel.name}` : ` for ${channel.name}`}</span>
          </a>
        </>
      )}
    </span>
  );
}

// PaidChannelsBanner is the channel page banner after the org left its
// paid plan: the grace end date while paid channels still deliver, then
// that they are disabled, naming them. role="status" so it is announced
// politely; the leading label word carries the state, the tint and side
// bar only repeat it. People who cannot change the plan are told who can.
export function PaidChannelsBanner({ channels }) {
  const opts = { edition: appMeta.value?.edition, ...upgradeAccess() };
  const b = paidChannelsBanner(channels, currentOrg.value, opts);
  if (!b) return null;
  return (
    <div class={`channel-plan-banner channel-plan-banner-${b.state}`} role="status">
      <p class="channel-plan-banner-text">
        <strong class="channel-plan-banner-label">{b.label}<span class="channel-health-sr">:</span></strong>{' '}
        {b.text}
      </p>
      {b.names.length > 0 && (
        <p class="channel-plan-banner-names">
          Not sending: {b.names.join(', ')}.
        </p>
      )}
      {b.alsoGrace && <p class="channel-plan-banner-text">{b.alsoGrace}</p>}
      {(b.action || b.note) && (
        <p class="channel-plan-banner-actions">
          {b.note && <span>{b.note}</span>}
          {b.action && <a href={b.action.href} class="channel-plan-banner-link">{b.action.text}</a>}
        </p>
      )}
    </div>
  );
}

export function channelTestNoteId(id) {
  return 'channel-test-note-' + id;
}

// ChannelTestNote explains a Test the server refused (rate limit, plan,
// quota, or a check that could not run). The Test button points at it with
// aria-describedby, and the list's live region announces it once.
// A note with dismissible set (a failed Test) shows a Dismiss button and
// stays until it is used or the next Test runs.
export function ChannelTestNote({ channel, note, onDismiss }) {
  if (!note) return null;
  return (
    <span class={`channel-test-note channel-test-note-${note.kind}`} id={channelTestNoteId(channel.id)}>
      {note.message}
      {note.dismissible && onDismiss && (
        <>
          {' '}
          <button type="button" class="channel-test-note-dismiss" aria-label={'Dismiss test result for ' + channel.name}
            onClick={e => { e.stopPropagation(); onDismiss(); }}>Dismiss</button>
        </>
      )}
      {note.upgradeHref && (
        <>
          {' '}
          <a href={note.upgradeHref} onClick={e => e.stopPropagation()}>
            {note.kind === 'quota' ? 'View plan usage' : 'Upgrade to Pro'}
          </a>
        </>
      )}
    </span>
  );
}

// useTestCooldowns tracks Test cooldowns from 429 answers, keyed by limit
// scope ('org' covers every channel, 'phone' SMS and voice). When the
// last one ends the buttons re-enable and the rate notes are cleared.
// announcement is the text for one polite live region on the list.
export function useTestCooldowns(setTestNotes) {
  const [cooldowns, setCooldowns] = useState({});
  const [announcement, setAnnouncement] = useState('');

  const startCooldown = useCallback(({ scope, until }) => {
    setCooldowns(c => ({ ...c, [scope]: Math.max(c[scope] || 0, until) }));
  }, []);

  // Clear first so the same text twice is still announced.
  const announce = useCallback(text => {
    setAnnouncement('');
    setTimeout(() => setAnnouncement(text), 50);
  }, []);

  useEffect(() => {
    const now = Date.now();
    const pending = Object.values(cooldowns).filter(t => t > now);
    if (!pending.length) return undefined;
    const next = Math.min(...pending);
    const timer = setTimeout(() => {
      const t = Date.now();
      // The effect reruns on every change, so this closure has the
      // current cooldowns.
      const kept = {};
      for (const [k, v] of Object.entries(cooldowns)) if (v > t) kept[k] = v;
      setCooldowns(kept);
      setTestNotes(n => {
        const kept = {};
        for (const [id, note] of Object.entries(n)) {
          if (note.kind !== 'rate' || (note.cooldown && note.cooldown.until > t)) kept[id] = note;
        }
        return kept;
      });
      const ended = Object.keys(cooldowns).filter(k => !(k in kept));
      if (ended.includes('org')) setAnnouncement('Channel tests are available again.');
      else if (ended.includes('phone')) setAnnouncement('SMS and voice tests are available again.');
    }, Math.max(0, next - now) + 50);
    return () => clearTimeout(timer);
  }, [cooldowns]);

  return { cooldowns, startCooldown, announce, announcement };
}
