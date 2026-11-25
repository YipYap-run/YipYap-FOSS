// Which escalation policies use a channel: the row disclosure, the
// in-form delete and disable confirmations, the refused delete notice with
// links, the escalation step warning and the ?policy= deep link. Policy
// names are rendered as text, never HTML.
import { useEffect, useRef } from 'preact/hooks';
import { usedByList, usedByLabel, unusedLabel, policyHref, stepsText, stepChannelWarning, stepsWithoutActiveChannel, withoutPolicyParam } from './channel-usage-logic.mjs';
import './channel-usage.css';

function PolicyLinks({ usedBy }) {
  return (
    <ul class="channel-usage-list">
      {usedBy.map(p => (
        <li key={p.policy_id}>
          <a href={policyHref(p.policy_id)}>{p.policy_name || 'Unnamed policy'}</a>
          {stepsText(p.steps) && <span class="channel-usage-steps"> ({stepsText(p.steps)})</span>}
        </li>
      ))}
    </ul>
  );
}

// ChannelUsageLine sits on the channel row. In use: a disclosure that
// lists the policies with links. Not in use: a short note, since such a
// channel only ever receives Tests. The row ignores clicks inside it
// (fromUsageDisclosure) rather than this stopping them, so the router
// still handles the policy links without a full page load.
export function ChannelUsageLine({ channel }) {
  const usedBy = usedByList(channel);
  const label = usedByLabel(usedBy);
  if (!label) {
    const note = unusedLabel(channel);
    return note ? <span class="channel-usage-line channel-usage-unused">{note}</span> : null;
  }
  return (
    <details class="channel-usage-line channel-usage-details">
      <summary>{label}</summary>
      <PolicyLinks usedBy={usedBy} />
    </details>
  );
}

// fromUsageDisclosure is true for a click on the row's usage disclosure,
// which must not also open the edit form.
export function fromUsageDisclosure(e) {
  return !!e?.target?.closest?.('.channel-usage-details');
}

// InlineConfirm asks inside the open form instead of a native confirm(),
// so the question, the affected policies and the answer buttons are all
// in the page and the form keeps its edits whatever the answer. Focus
// moves to the question when it appears; the caller moves it back on
// cancel. Escape cancels the question and keeps the dialog open.
export function InlineConfirm({ id, title, children, confirmLabel, danger, busy, onConfirm, onCancel }) {
  const ref = useRef(null);
  useEffect(() => { ref.current?.focus(); }, []);
  return (
    <div class={'channel-confirm' + (danger ? ' channel-confirm-danger' : '')} role="group"
      aria-labelledby={id + '-title'} aria-describedby={children ? id + '-body' : undefined}
      onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); if (!busy) onCancel(); } }}>
      <p class="channel-confirm-title" id={id + '-title'} tabIndex={-1} ref={ref}>{title}</p>
      {children && <div id={id + '-body'}>{children}</div>}
      <div class="channel-confirm-actions">
        <button type="button" class={'btn btn-sm ' + (danger ? 'btn-danger' : 'btn-primary')}
          onClick={onConfirm} disabled={busy} aria-busy={busy ? 'true' : undefined}>
          {busy ? 'Working…' : confirmLabel}
        </button>
        <button type="button" class="btn btn-sm" onClick={onCancel} disabled={busy}>Cancel</button>
      </div>
    </div>
  );
}

// ChannelDeleteBlocked replaces the delete with an explanation and a link
// to each policy. role="alert" so it is announced when it appears after
// the Delete click; focus moves to the heading. dirty adds a line that a
// policy link leaves this form, because following it discards the edits.
export function ChannelDeleteBlocked({ usedBy, dirty, onDismiss }) {
  const ref = useRef(null);
  useEffect(() => { ref.current?.focus(); }, []);
  const n = usedBy.length;
  return (
    <div class="channel-delete-blocked" role="alert"
      onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); e.stopPropagation(); onDismiss(); } }}>
      <p class="channel-delete-blocked-title" tabIndex={-1} ref={ref}>
        This channel cannot be deleted yet
      </p>
      <p>
        {n === 1 ? 'An escalation policy sends' : `${n} escalation policies send`} alerts through it.
        Remove the channel from {n === 1 ? 'that policy' : 'each policy'}, then delete it.
        To stop alerts going to it now, clear Enabled and save instead.
      </p>
      <PolicyLinks usedBy={usedBy} />
      {dirty && (
        <p class="channel-delete-blocked-dirty">
          Opening a policy closes this form. Save first to keep your changes to this channel.
        </p>
      )}
      <button type="button" class="btn btn-xs" onClick={onDismiss}>Dismiss</button>
    </div>
  );
}

// StepChannelWarning shows under a step none of whose channels can
// deliver. channels null means the list has not loaded: no warning.
export function StepChannelWarning({ step, channels }) {
  if (!channels || stepsWithoutActiveChannel([step], channels).length === 0) return null;
  return <p class="step-channel-warning" role="status">{stepChannelWarning(step, channels)}</p>;
}

// usePolicyDeepLink opens the steps of the policy named by ?policy= once
// the policy list has loaded, then drops the parameter so a reload does
// not open it again. onMissing runs when no policy has that id (deleted
// since the link was shown, or not visible to this user).
export function usePolicyDeepLink(policies, loading, open, onMissing) {
  const done = useRef(false);
  useEffect(() => {
    if (loading || done.current || typeof window === 'undefined') return;
    done.current = true;
    const id = new URLSearchParams(window.location.search).get('policy');
    if (!id) return;
    const rest = withoutPolicyParam(window.location.search);
    if (rest !== null) {
      try { window.history.replaceState(window.history.state, '', window.location.pathname + rest + window.location.hash); } catch (_) {}
    }
    const p = (policies || []).find(x => x.id === id);
    if (p) open(p);
    else if (onMissing) onMissing();
  }, [loading, policies]);
}
