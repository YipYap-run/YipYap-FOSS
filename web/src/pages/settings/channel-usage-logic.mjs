// Which escalation policies send through a channel. Contract:
// internal/domain/escalation.go ChannelPolicyRef, returned as `used_by` on
// GET, list, POST and PATCH of /notification-channels (always an array):
//   [{ policy_id, policy_name, steps: [1-based step numbers] }]
// DELETE /notification-channels/{id} answers 409 with
//   { error, used_by } while the list is non-empty, and 204 once the
// channel is removed from every policy. Disabling is allowed.

export function usedByList(channel) {
  return Array.isArray(channel?.used_by) ? channel.used_by : [];
}

// usedByLabel is the short text on a channel row, '' when unused.
export function usedByLabel(usedBy) {
  const n = Array.isArray(usedBy) ? usedBy.length : 0;
  if (n === 0) return '';
  return `Used by ${n} ${n === 1 ? 'policy' : 'policies'}`;
}

// policyHref opens a policy's steps on the escalation tab.
export function policyHref(policyId) {
  return '/settings/escalation?policy=' + encodeURIComponent(policyId);
}

// stepsText is "step 2" or "steps 1 and 3", '' when unknown.
export function stepsText(steps) {
  const s = Array.isArray(steps) ? steps.filter(n => Number.isInteger(n) && n > 0) : [];
  if (s.length === 0) return '';
  if (s.length === 1) return `step ${s[0]}`;
  return `steps ${s.slice(0, -1).join(', ')} and ${s[s.length - 1]}`;
}

// policyNamesText is "Backup", "Backup and Primary" or
// "Backup, On call and Primary".
export function policyNamesText(usedBy) {
  const names = (Array.isArray(usedBy) ? usedBy : []).map(p => p?.policy_name || 'Unnamed policy');
  if (names.length <= 1) return names.join('');
  return names.slice(0, -1).join(', ') + ' and ' + names[names.length - 1];
}

// unusedLabel is the row note for a channel no policy sends through, so
// it is clear the channel only fires on a Test. '' when the API did not
// say (an older server without used_by) or the channel is in use.
export function unusedLabel(channel) {
  if (!channel || !Array.isArray(channel.used_by) || channel.used_by.length) return '';
  return 'Not in any escalation policy, so alerts do not reach it';
}

// needsDisableConfirm is true when a save turns off a channel that
// policies send through. Disabling is allowed; the form asks first.
export function needsDisableConfirm(wasEnabled, nowEnabled, usedBy) {
  return !!wasEnabled && !nowEnabled && Array.isArray(usedBy) && usedBy.length > 0;
}

// disableConfirmText is the confirmation shown before saving a channel
// that goes from enabled to disabled while policies use it; '' when no
// confirmation is needed.
export function disableConfirmText(wasEnabled, nowEnabled, usedBy) {
  if (!needsDisableConfirm(wasEnabled, nowEnabled, usedBy)) return '';
  const n = usedBy.length;
  return `${n === 1 ? 'This escalation policy sends' : `These ${n} escalation policies send`} alerts through this channel and will skip it while it is disabled: ${policyNamesText(usedBy)}.`;
}

// deleteConfirmText names the channel in the delete confirmation.
export function deleteConfirmText(name) {
  const n = typeof name === 'string' && name.trim() ? `"${name.trim()}"` : 'this channel';
  return `Delete ${n}? This cannot be undone.`;
}

// formSnapshot is a stable string of the channel form, so the form can
// tell whether anything changed since it opened. Keys starting with "_"
// are form bookkeeping and do not count. Object keys are sorted so the
// order fields were typed in does not matter.
export function formSnapshot(form) {
  const norm = v => {
    if (Array.isArray(v)) return v.map(norm);
    if (v && typeof v === 'object') {
      const out = {};
      for (const k of Object.keys(v).sort()) {
        if (k.startsWith('_')) continue;
        if (v[k] === undefined) continue;
        out[k] = norm(v[k]);
      }
      return out;
    }
    return v;
  };
  return JSON.stringify(norm(form));
}

// channelOptionLabel is a channel's entry in the escalation step picker.
// It says when the channel will not deliver, so a step is not built on
// one by mistake.
export function channelOptionLabel(ch, typeLabel) {
  if (!ch) return '';
  const base = `${ch.name || 'Unnamed channel'} (${typeLabel || ch.type})`;
  if (ch.enabled === false) return base + ', disabled';
  if (ch.plan_status === 'disabled') return base + ', paused by plan';
  return base;
}

// withoutPolicyParam drops ?policy= from a query string once the deep link
// has opened the policy, so a reload or Back does not reopen it.
export function withoutPolicyParam(search) {
  const q = new URLSearchParams(search || '');
  if (!q.has('policy')) return null;
  q.delete('policy');
  const rest = q.toString();
  return rest ? '?' + rest : '';
}

// deleteBlockedBy returns the policies that block a delete, from the
// channel row before the request or from a 409 response after it, or null
// when the delete may go ahead (or failed for another reason).
export function deleteBlockedBy(channel, err) {
  if (err) {
    if (err.status === 409 && Array.isArray(err.body?.used_by)) return err.body.used_by;
    return null;
  }
  const list = usedByList(channel);
  return list.length ? list : null;
}

// stepsWithoutActiveChannel returns the 1-based numbers of steps none of
// whose targets has a channel that will deliver: the channel exists, is
// enabled, and is not paused by the plan (plan_status 'disabled'). A step
// with no targets counts too. channels is the GET /notification-channels
// list; while it has not loaded (null) nothing is flagged.
export function stepsWithoutActiveChannel(steps, channels) {
  if (!Array.isArray(steps) || !Array.isArray(channels)) return [];
  const active = new Set(channels
    .filter(c => c && c.enabled !== false && c.plan_status !== 'disabled')
    .map(c => c.id));
  const out = [];
  steps.forEach((step, i) => {
    const targets = Array.isArray(step?.targets) ? step.targets : [];
    if (!targets.some(t => t && active.has(t.channel_id))) out.push(i + 1);
  });
  return out;
}

// stepChannelWarning is the text under a step that will page nobody.
export function stepChannelWarning(step, channels) {
  const targets = Array.isArray(step?.targets) ? step.targets : [];
  if (targets.length === 0 || targets.every(t => !t?.channel_id)) {
    return 'This step has no notification channel, so it will not notify anyone.';
  }
  const known = new Map((channels || []).map(c => [c.id, c]));
  const reasons = new Set();
  for (const t of targets) {
    const c = known.get(t?.channel_id);
    if (!c) reasons.add('missing');
    else if (c.enabled === false) reasons.add('disabled');
    else if (c.plan_status === 'disabled') reasons.add('paused');
  }
  if (reasons.size === 1 && reasons.has('disabled')) {
    return 'Every channel on this step is disabled, so it will not notify anyone. Enable a channel or pick another.';
  }
  if (reasons.size === 1 && reasons.has('paused')) {
    return 'Every channel on this step is paused by your plan, so it will not notify anyone. Pick another channel or upgrade.';
  }
  return 'No channel on this step can deliver (disabled, paused or deleted), so it will not notify anyone.';
}
