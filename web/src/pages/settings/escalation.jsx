import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch, del, put } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, Spinner, useToast, ToastContainer } from '../../components/ui';
import { appMeta, currentUser } from '../../state/auth';
import { canWrite } from '../../state/permissions.mjs';
import { coverage } from './contact-methods.jsx';
import './escalation.css';

/*
 * Escalation policies: policy list, policy modal, and the steps editor.
 *
 * API contract (PUT /escalation-policies/{id}/steps):
 *  - Delays travel as wait_seconds and repeat_interval_seconds. The editor
 *    shows minutes and converts on load and save.
 *  - Step and target ids are sent back so open alerts keep their place.
 *  - on_call_primary / on_call_secondary targets carry a schedule id in
 *    target_id; user and team targets carry a user or team id.
 *  - A 400 carries {error, field, fields}, where each key in fields is a path
 *    like "steps[0].targets[1].target_id".
 *  - channel_id is required only for "channel" targets. For user, on-call and
 *    team targets it is an optional extra destination: people are paged
 *    through their own contact methods and notification rules, and the
 *    channel gets one message per page of the step.
 *  - Targets form groups in order. The first target starts a group; a target
 *    with simultaneous=true joins the group before it, false starts a new
 *    group. Groups page wait_seconds apart. An omitted simultaneous is saved
 *    as true.
 *  - After the last group, the step repeats repeat_count more times, spaced
 *    by repeat_interval_seconds (0 means use wait_seconds). wait_seconds
 *    after its last page, the next step pages. is_terminal stops escalation
 *    at that point instead, even when the policy loops.
 */

const ON_CALL_TYPES = new Set(['on_call_primary', 'on_call_secondary']);

// The alert detail page links to a policy as /settings/escalation#ep-<id>.
export const POLICY_ANCHOR_PREFIX = 'ep-';

export function policyIdFromHash(hash) {
  const h = (hash || '').replace(/^#/, '');
  if (!h.startsWith(POLICY_ANCHOR_PREFIX)) return '';
  try {
    return decodeURIComponent(h.slice(POLICY_ANCHOR_PREFIX.length));
  } catch {
    return '';
  }
}

const TARGET_TYPES = [
  { value: 'on_call_primary', label: 'On-call primary' },
  { value: 'on_call_secondary', label: 'On-call secondary' },
  { value: 'user', label: 'User' },
  { value: 'team', label: 'Team' },
  { value: 'channel', label: 'Channel only' },
];

// Teams and schedules do not exist in the FOSS edition, so those target
// types would always fail to page.
const SAAS_ONLY_TYPES = new Set(['on_call_primary', 'on_call_secondary', 'team']);

function isFoss() {
  return appMeta.value?.edition === 'foss';
}

// Target types offered for a target. A saved target keeps its own type in
// the list so the select never shows a blank value.
function targetTypeOptions(current) {
  if (!isFoss()) return TARGET_TYPES;
  return TARGET_TYPES.filter(o => !SAAS_ONLY_TYPES.has(o.value) || o.value === current);
}

function secondsToMinutes(s) {
  const n = Number(s) || 0;
  return Math.round((n / 60) * 100) / 100;
}

function minutesToSeconds(m) {
  const n = Number(m);
  if (!Number.isFinite(n)) return 0;
  return Math.round(n * 60);
}

// formatSpan turns seconds into short text such as "5 minutes",
// "1 hour 30 minutes" or "45 seconds".
export function formatSpan(seconds) {
  const s = Math.max(0, Math.round(Number(seconds) || 0));
  if (s === 0) return 'no wait';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  // Seconds only matter for spans under an hour.
  const sec = h ? 0 : s % 60;
  const parts = [];
  if (h) parts.push(`${h} hour${h === 1 ? '' : 's'}`);
  if (m) parts.push(`${m} minute${m === 1 ? '' : 's'}`);
  if (sec) parts.push(`${sec} second${sec === 1 ? '' : 's'}`);
  return parts.join(' ');
}

// targetGroups counts the groups a step pages in order. The first target
// starts a group, and each target that is not simultaneous starts another.
export function targetGroups(targets) {
  if (!targets || targets.length === 0) return 0;
  let n = 1;
  for (let i = 1; i < targets.length; i++) {
    if (!targets[i].simultaneous) n += 1;
  }
  return n;
}

// describeStep is the plain-language timing summary shown under a step. It
// mirrors the escalation engine: groups page one wait apart, repeats follow
// the repeat interval (or the wait when none is set), and the next step pages
// one wait after the last page.
export function describeStep(step, index, total, policy) {
  const wait = minutesToSeconds(step.delay_minutes);
  const repeats = Math.max(0, Number(step.repeat_count) || 0);
  const interval = minutesToSeconds(step.repeat_interval_minutes) || wait;
  const nTargets = (step.targets || []).length;
  const groups = targetGroups(step.targets);
  const out = [];

  if (nTargets === 0) return 'Add a target so this step pages someone.';
  if (groups <= 1) {
    out.push(nTargets === 1 ? 'Pages this target.'
      : nTargets === 2 ? 'Pages both targets at once.' : `Pages all ${nTargets} targets at once.`);
  } else if (wait > 0) {
    out.push(`Pages the targets in ${groups} groups, ${formatSpan(wait)} apart.`);
  } else {
    out.push(`Pages the targets in ${groups} groups, one right after the other.`);
  }
  if (repeats > 0) {
    const gap = interval > 0 ? formatSpan(interval) : '';
    if (repeats === 1) {
      out.push(gap ? `Then pages once more, ${gap} later.` : 'Then pages once more right away.');
    } else {
      out.push(gap ? `Then pages ${repeats} more times, ${gap} apart.` : `Then pages ${repeats} more times with no wait between.`);
    }
  }
  const after = wait > 0 ? `${formatSpan(wait)} after the last page` : 'right after the last page';
  if (step.is_terminal) {
    out.push(`Escalation stops ${after}. Nobody else is paged and the alert stays open.`);
  } else if (index < total - 1) {
    out.push(`If nobody acknowledges, step ${index + 2} pages ${after}.`);
  } else if (policy && policy.loop) {
    out.push(`If nobody acknowledges, step 1 pages again ${after}${policy.max_loops != null ? ` (up to ${policy.max_loops} loop${Number(policy.max_loops) === 1 ? '' : 's'})` : ''}.`);
  } else {
    out.push('This is the last step. The alert stays open with no more pages.');
  }
  return out.join(' ');
}

// reachWarning says when a person picked as a user target cannot be paged
// for some alerts. reach is {high, low} from coverage(), or undefined while
// it loads or when the viewer cannot read it.
export function reachWarning(reach, name) {
  if (!reach) return '';
  const who = name || 'This person';
  if (!reach.high && !reach.low) {
    return `${who} has no verified contact method with a notification rule, so this target will not page them. Only they can add one, under Settings, Contact Methods.`;
  }
  if (!reach.high) return `${who} has no notification rule for critical alerts, so critical alerts will not page them.`;
  if (!reach.low) return `${who} has no notification rule for warning and info alerts, so those will not page them.`;
  return '';
}

// groupReachWarning says when people a team or on-call target would page
// cannot be paged. memberIds are the team's members (an on-call target pages
// whoever of them is on shift); readiness is the users map from
// GET /users/paging-readiness. Returns '' while either is still loading.
export function groupReachWarning(onCall, memberIds, readiness, users) {
  if (!memberIds || !readiness) return '';
  if (memberIds.length === 0) {
    return onCall ? 'This schedule\'s team has no members, so nobody is on call.' : 'This team has no members, so this target pages nobody.';
  }
  const missing = memberIds.filter(id => !(readiness[id] && readiness[id].pageable));
  if (missing.length === 0) return '';
  const nameOf = id => {
    const u = (users || []).find(x => x.id === id);
    return (u && (u.name || u.email)) || 'A member';
  };
  const who = missing.length === 1 ? nameOf(missing[0])
    : missing.length === 2 ? `${nameOf(missing[0])} and ${nameOf(missing[1])}`
      : `${missing.length} members`;
  const verb = missing.length === 1 ? 'has' : 'have';
  if (onCall) {
    return `${who} ${verb} no verified contact method with a notification rule, so a page during their shift reaches nobody. Each person adds their own under Settings, Contact Methods.`;
  }
  return `${who} ${verb} no verified contact method with a notification rule, so this target will not page them. Each person adds their own under Settings, Contact Methods.`;
}

// Client-only keys keep list items stable while steps and targets are added
// or removed. They are never sent to the server.
let keySeq = 0;
function nextKey() {
  keySeq += 1;
  return `k${keySeq}`;
}

export function stepFromAPI(s) {
  return {
    _key: nextKey(),
    id: s.id || '',
    delay_minutes: secondsToMinutes(s.wait_seconds),
    repeat_count: s.repeat_count || 0,
    repeat_interval_minutes: secondsToMinutes(s.repeat_interval_seconds),
    is_terminal: !!s.is_terminal,
    targets: (s.targets || []).map(t => ({
      _key: nextKey(),
      id: t.id || '',
      target_type: t.target_type || 'on_call_primary',
      target_id: t.target_id || '',
      channel_id: t.channel_id || '',
      // Older saves have no explicit value; the server reads those as true.
      simultaneous: t.simultaneous !== false,
    })),
  };
}

export function stepToAPI(s) {
  const out = {
    wait_seconds: minutesToSeconds(s.delay_minutes),
    repeat_count: Number(s.repeat_count) || 0,
    repeat_interval_seconds: minutesToSeconds(s.repeat_interval_minutes),
    is_terminal: !!s.is_terminal,
    targets: (s.targets || []).map((t, i) => {
      const tgt = {
        target_type: t.target_type,
        target_id: t.target_type === 'channel' ? '' : t.target_id,
        channel_id: t.channel_id || '',
        // The first target always starts the step, so it is saved as
        // simultaneous to keep the stored value meaningful.
        simultaneous: i === 0 ? true : !!t.simultaneous,
      };
      if (t.id) tgt.id = t.id;
      return tgt;
    }),
  };
  if (s.id) out.id = s.id;
  return out;
}

function FieldError({ id, msg }) {
  if (!msg) return null;
  return <p id={id} class="field-error">{msg}</p>;
}

// Joins the ids of help and error text for aria-describedby.
function describedBy(...ids) {
  const out = ids.filter(Boolean).join(' ');
  return out || undefined;
}

const LOOKUPS = [
  { key: 'channels', path: '/notification-channels', label: 'notification channels' },
  { key: 'users', path: '/users', label: 'users' },
  { key: 'teams', path: '/teams', label: 'teams', saas: true },
  { key: 'schedules', path: '/schedules', label: 'on-call schedules', saas: true },
];

// Policy create/update: name (required, at most 200 characters) and
// description (optional, at most 2000) are trimmed by the server. A 400
// carries {error, field, fields} so each message goes next to its input.
const EMPTY_POLICY = { name: '', description: '', loop: false, max_loops: '' };
const MAX_POLICY_DESCRIPTION = 2000;

export function EscalationTab() {
  const { toasts, toast } = useToast();
  const writer = canWrite(currentUser.value?.role);
  const [policies, setPolicies] = useState([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState(null);
  // A link from an alert names one policy; it is highlighted and focused,
  // or a note says it no longer exists.
  const [linkedId, setLinkedId] = useState(() => policyIdFromHash(typeof location !== 'undefined' ? location.hash : ''));

  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState(EMPTY_POLICY);
  const [editId, setEditId] = useState(null);
  const [policyError, setPolicyError] = useState('');
  const [policyFieldErrors, setPolicyFieldErrors] = useState({});
  const [savingPolicy, setSavingPolicy] = useState(false);

  // Steps editor. editingSteps is the policy being edited ({id, name}).
  const [editingSteps, setEditingSteps] = useState(null);
  const [steps, setSteps] = useState([]);
  const [stepsLoading, setStepsLoading] = useState(false);
  const [stepsLoadError, setStepsLoadError] = useState(null);
  const [openAlerts, setOpenAlerts] = useState(0);
  const [stepErrors, setStepErrors] = useState({});
  const [stepsError, setStepsError] = useState('');
  const [savingSteps, setSavingSteps] = useState(false);
  const stepsFormRef = useRef(null);

  const [lookups, setLookups] = useState({ channels: [], users: [], teams: [], schedules: [] });
  const [lookupErrors, setLookupErrors] = useState([]);
  const { channels, users, teams, schedules } = lookups;

  // How each user picked as a target can be paged, keyed by user id. Only
  // owners and admins can read another member's contact methods.
  const [reach, setReach] = useState({});
  const canSeeReach = ['owner', 'admin'].includes(currentUser.value?.role);
  useEffect(() => {
    if (!canSeeReach) return;
    const ids = new Set();
    for (const st of steps) {
      for (const t of st.targets || []) {
        if (t.target_type === 'user' && t.target_id && !(t.target_id in reach)) ids.add(t.target_id);
      }
    }
    if (ids.size === 0) return;
    // Mark as loading so each id is fetched once.
    setReach(r => {
      const next = { ...r };
      for (const id of ids) next[id] = null;
      return next;
    });
    for (const id of ids) {
      get(`/users/${encodeURIComponent(id)}/contact-methods`)
        .then(d => setReach(r => ({ ...r, [id]: coverage(d.contact_methods || [], d.notification_rules || []) })))
        .catch(() => setReach(r => ({ ...r, [id]: null })));
    }
  }, [steps, canSeeReach]);

  // Team and on-call targets: which members each team has (keyed by team
  // id) and who in the org can be paged at all. One readiness request
  // covers everyone; team members load once per picked team.
  const [readiness, setReadiness] = useState(null);
  const [teamMembers, setTeamMembers] = useState({});
  useEffect(() => {
    if (!editingSteps) return;
    get('/users/paging-readiness')
      .then(d => setReadiness((d && d.users) || {}))
      .catch(() => setReadiness(null));
  }, [editingSteps && editingSteps.id]);
  const teamOf = t => t.target_type === 'team' ? t.target_id
    : ON_CALL_TYPES.has(t.target_type) ? (schedules.find(s => s.id === t.target_id) || {}).team_id : '';
  useEffect(() => {
    if (isFoss()) return;
    const ids = new Set();
    for (const st of steps) {
      for (const t of st.targets || []) {
        const team = teamOf(t);
        if (team && !(team in teamMembers)) ids.add(team);
      }
    }
    if (ids.size === 0) return;
    setTeamMembers(m => {
      const next = { ...m };
      for (const id of ids) next[id] = null;
      return next;
    });
    for (const id of ids) {
      get(`/teams/${encodeURIComponent(id)}`)
        .then(d => setTeamMembers(m => ({ ...m, [id]: (d.members || []).map(x => x.user_id) })))
        .catch(() => setTeamMembers(m => ({ ...m, [id]: null })));
    }
  }, [steps, schedules]);

  const defaultTarget = () => ({
    _key: nextKey(),
    id: '',
    target_type: isFoss() ? 'user' : 'on_call_primary',
    target_id: isFoss() ? '' : schedules.length === 1 ? schedules[0].id : '',
    // People are paged through their own contact methods, so a channel is
    // an opt-in extra and starts empty.
    channel_id: '',
    simultaneous: true,
  });

  async function load() {
    setLoading(true);
    setListError(null);
    try {
      const data = await get('/escalation-policies');
      setPolicies(data.policies || data || []);
    } catch (e) {
      setListError(e);
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  useEffect(() => {
    const onHash = () => setLinkedId(policyIdFromHash(location.hash));
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  useEffect(() => {
    if (loading || !linkedId) return;
    const el = document.getElementById(POLICY_ANCHOR_PREFIX + linkedId);
    if (!el) return;
    el.scrollIntoView({ block: 'center' });
    el.focus({ preventScroll: true });
  }, [loading, linkedId]);

  async function loadLookups() {
    const failed = [];
    const next = { channels: [], users: [], teams: [], schedules: [] };
    const wanted = LOOKUPS.filter(l => !(l.saas && isFoss()));
    await Promise.all(wanted.map(async l => {
      try {
        const d = await get(l.path);
        next[l.key] = d[l.key] || (Array.isArray(d) ? d : []);
      } catch (_) {
        next[l.key] = [];
        failed.push(l.label);
      }
    }));
    setLookups(next);
    setLookupErrors(LOOKUPS.map(l => l.label).filter(x => failed.includes(x)));
  }
  useEffect(() => { loadLookups(); }, []);

  function openPolicyModal(p) {
    setPolicyError('');
    setPolicyFieldErrors({});
    if (p) {
      setEditId(p.id);
      setForm({
        name: p.name,
        description: p.description || '',
        loop: p.loop || false,
        max_loops: p.max_loops != null ? String(p.max_loops) : '',
      });
    } else {
      setEditId(null);
      setForm(EMPTY_POLICY);
    }
    setShowModal(true);
  }

  async function save() {
    const body = {
      name: form.name.trim(),
      description: form.description.trim(),
      loop: form.loop,
      max_loops: form.loop && form.max_loops !== '' ? +form.max_loops : null,
    };
    setSavingPolicy(true);
    setPolicyError('');
    setPolicyFieldErrors({});
    try {
      const created = editId
        ? await patch(`/escalation-policies/${editId}`, body)
        : await post('/escalation-policies', body);
      setShowModal(false);
      setEditId(null);
      load();
      // A new policy pages nobody until it has steps, so go straight to the
      // steps editor.
      if (!editId && created && created.id) {
        toast('Policy created. Add the steps that page people.');
        openSteps(created);
      } else {
        toast(editId ? 'Policy updated' : 'Policy created');
      }
    } catch (e) {
      const fields = (e && e.status === 400 && e.body && e.body.fields) || {};
      if (fields.name || fields.description) {
        setPolicyFieldErrors(fields);
        // Move focus to the first field that needs attention.
        const first = fields.name ? 'policy-name' : 'policy-description';
        setTimeout(() => document.getElementById(first)?.focus(), 0);
      } else {
        setPolicyError((e && e.message) || 'Could not save the policy');
      }
    } finally {
      setSavingPolicy(false);
    }
  }

  async function deletePolicy(p) {
    if (!confirm(`Delete the policy "${p.name}"? Monitors that use it will stop paging anyone.`)) return;
    try {
      await del(`/escalation-policies/${p.id}`);
      toast('Policy deleted');
      load();
    } catch (e) {
      toast((e && e.message) || 'Could not delete the policy', 'error');
    }
  }

  async function loadSteps(policy) {
    setStepsLoading(true);
    setStepsLoadError(null);
    setSteps([]);
    setOpenAlerts(0);
    try {
      const detail = await get(`/escalation-policies/${policy.id}`);
      setSteps((detail.steps || []).map(stepFromAPI));
      setOpenAlerts(detail.open_alert_count || 0);
    } catch (e) {
      // Never show an empty editor after a failed load: saving it would
      // delete every step on the policy.
      setStepsLoadError(e);
    } finally {
      setStepsLoading(false);
    }
  }

  function openSteps(policy) {
    setEditingSteps({ id: policy.id, name: policy.name, loop: !!policy.loop, max_loops: policy.max_loops ?? null });
    setStepErrors({});
    setStepsError('');
    loadSteps(policy);
  }

  function closeSteps() {
    setEditingSteps(null);
    setSteps([]);
  }

  // After a failed save, move focus to the first field the server rejected so
  // keyboard and screen reader users land on the problem.
  useEffect(() => {
    if (!stepsError || !stepsFormRef.current) return;
    const el = stepsFormRef.current.querySelector('[aria-invalid="true"]')
      || stepsFormRef.current.querySelector('.form-error');
    if (el && el.focus) el.focus();
  }, [stepErrors, stepsError]);

  async function saveSteps() {
    if (stepsLoadError || stepsLoading) return;
    if (steps.length === 0 && !confirm('This policy has no steps, so alerts that use it will not page anyone. Save anyway?')) return;
    if (openAlerts > 0) {
      const n = openAlerts;
      const msg = `${n} open alert${n === 1 ? ' uses' : 's use'} this policy. ${n === 1 ? 'It continues' : 'They continue'} from the same step number with the new settings. Save anyway?`;
      if (!confirm(msg)) return;
    }
    setSavingSteps(true);
    setStepErrors({});
    setStepsError('');
    try {
      await put(`/escalation-policies/${editingSteps.id}/steps`, { steps: steps.map(stepToAPI) });
      closeSteps();
      toast('Steps saved');
      load();
    } catch (e) {
      const fields = (e && e.body && e.body.fields) || {};
      const n = Object.keys(fields).length;
      setStepErrors(fields);
      setStepsError(n > 1
        ? `Steps not saved. Fix the ${n} fields marked below.`
        : (e && e.message) || 'Steps not saved. Try again.');
    } finally {
      setSavingSteps(false);
    }
  }

  function addStep() {
    setSteps([...steps, {
      _key: nextKey(),
      id: '',
      delay_minutes: 5,
      repeat_count: 0,
      repeat_interval_minutes: 0,
      is_terminal: false,
      targets: [defaultTarget()],
    }]);
  }

  function removeStep(i) {
    // Server error paths are indexed by position, so they no longer apply.
    setStepErrors({});
    setSteps(steps.filter((_, j) => j !== i));
  }

  function updateStep(i, p) {
    const s = [...steps];
    s[i] = { ...s[i], ...p };
    setSteps(s);
  }

  function updateTarget(stepIdx, targetIdx, p) {
    const s = [...steps];
    const targets = [...s[stepIdx].targets];
    targets[targetIdx] = { ...targets[targetIdx], ...p };
    s[stepIdx] = { ...s[stepIdx], targets };
    setSteps(s);
  }

  function addTarget(stepIdx) {
    const s = [...steps];
    s[stepIdx] = { ...s[stepIdx], targets: [...s[stepIdx].targets, defaultTarget()] };
    setSteps(s);
  }

  function removeTarget(stepIdx, targetIdx) {
    setStepErrors({});
    const s = [...steps];
    s[stepIdx] = { ...s[stepIdx], targets: s[stepIdx].targets.filter((_, j) => j !== targetIdx) };
    setSteps(s);
  }

  // Field error lookup by server path, e.g. err(0, 'wait_seconds') or
  // err(0, 'target_id', 1).
  function err(i, field, ti) {
    const path = ti === undefined ? `steps[${i}].${field}` : `steps[${i}].targets[${ti}].${field}`;
    return stepErrors[path];
  }

  if (loading && policies.length === 0) return <LoadingPage />;

  const stepsReady = !stepsLoading && !stepsLoadError;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Escalation Policies</h3>
        {writer && <button type="button" class="btn btn-sm btn-primary" onClick={() => openPolicyModal(null)}>Add Policy</button>}
      </div>
      {listError ? (
        <ErrorMessage error={listError} onRetry={load} />
      ) : (
        <div class="settings-list">
          {linkedId && !loading && !policies.some(p => p.id === linkedId) && (
            <p class="form-help policy-linked-missing" role="status">
              The escalation policy linked from that alert has been deleted.
            </p>
          )}
          {policies.map(p => (
            <div key={p.id} id={POLICY_ANCHOR_PREFIX + p.id} tabIndex={p.id === linkedId ? -1 : undefined}
              class={`settings-item${p.id === linkedId ? ' policy-item-linked' : ''}`}
              aria-current={p.id === linkedId ? 'true' : undefined}>
              <div class="policy-main">
                <strong>{p.name}</strong>
                {p.loop && <span class="label-tag">Loop{p.max_loops != null ? ` (max ${p.max_loops})` : ''}</span>}
                {p.description && <p class="policy-description" title={p.description}>{p.description}</p>}
              </div>
              {writer && <div class="btn-group">
                <button type="button" class="btn btn-xs" aria-label={`Edit steps for ${p.name}`} onClick={() => openSteps(p)}>Steps</button>
                <button type="button" class="btn btn-xs" aria-label={`Edit ${p.name}`} onClick={() => openPolicyModal(p)}>Edit</button>
                <button type="button" class="btn btn-xs btn-danger" aria-label={`Delete ${p.name}`} onClick={() => deletePolicy(p)}>Delete</button>
              </div>}
            </div>
          ))}
          {policies.length === 0 && <p class="text-muted">No escalation policies yet. Create one to choose who gets paged when a monitor goes down.</p>}
        </div>
      )}

      <Modal open={showModal} onClose={() => setShowModal(false)} title={editId ? 'Edit Policy' : 'Add Policy'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          {policyError && <p class="form-error" role="alert">{policyError}</p>}
          <div class={`form-group${policyFieldErrors.name ? ' has-error' : ''}`}>
            <label class="required" for="policy-name">Name</label>
            <input id="policy-name" type="text" value={form.name} maxLength={200}
              aria-invalid={policyFieldErrors.name ? 'true' : undefined}
              aria-describedby={policyFieldErrors.name ? 'policy-name-err' : undefined}
              onInput={e => setForm({ ...form, name: e.target.value })} required />
            <FieldError id="policy-name-err" msg={policyFieldErrors.name} />
          </div>
          <div class={`form-group${policyFieldErrors.description ? ' has-error' : ''}`}>
            <label for="policy-description">Description</label>
            <textarea id="policy-description" rows={3} value={form.description} maxLength={MAX_POLICY_DESCRIPTION}
              aria-invalid={policyFieldErrors.description ? 'true' : undefined}
              aria-describedby={describedBy('policy-description-help', policyFieldErrors.description && 'policy-description-err')}
              onInput={e => setForm({ ...form, description: e.target.value })} />
            <p id="policy-description-help" class="form-help">
              Optional. What this policy covers and who owns it.
              {form.description.length > MAX_POLICY_DESCRIPTION - 200 && (
                ` ${MAX_POLICY_DESCRIPTION - form.description.length} characters left.`
              )}
            </p>
            <FieldError id="policy-description-err" msg={policyFieldErrors.description} />
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.loop} aria-describedby="policy-loop-help"
                onChange={e => setForm({ ...form, loop: e.target.checked })} />
              After the last step, restart from step 1
            </label>
            <p id="policy-loop-help" class="form-help">
              A step set to stop escalating ends paging there, even when this is on.
            </p>
          </div>
          {form.loop && (
            <div class="form-group">
              <label for="policy-max-loops">Max loops</label>
              <input id="policy-max-loops" type="number" min="1" step="1" value={form.max_loops}
                aria-describedby="policy-max-loops-help"
                onInput={e => setForm({ ...form, max_loops: e.target.value })} />
              <p id="policy-max-loops-help" class="form-help">Leave blank for no limit.</p>
            </div>
          )}
          <button type="submit" class="btn btn-primary" disabled={savingPolicy}>
            {savingPolicy ? 'Saving...' : 'Save'}
          </button>
        </form>
      </Modal>

      <Modal open={!!editingSteps} onClose={closeSteps}
        title={editingSteps ? `Escalation steps: ${editingSteps.name}` : 'Escalation steps'}>
        {stepsLoading && (
          <div class="steps-loading" role="status">
            <Spinner />
            <span class="sr-only">Loading steps</span>
          </div>
        )}
        {stepsLoadError && (
          <ErrorMessage
            error={{ message: `Could not load the steps for this policy. ${stepsLoadError.message || ''}`.trim() }}
            onRetry={() => loadSteps(editingSteps)} />
        )}
        {stepsReady && (
          <form ref={stepsFormRef} class="steps-builder"
            onSubmit={e => { e.preventDefault(); saveSteps(); }}>
            {openAlerts > 0 && (
              <div class="form-warning" role="status">
                <strong>{openAlerts} open alert{openAlerts === 1 ? ' uses' : 's use'} this policy.</strong>
                {' '}Saved changes apply to {openAlerts === 1 ? 'it' : 'them'} right away, starting from the step {openAlerts === 1 ? 'it is' : 'they are'} on.
              </div>
            )}
            {lookupErrors.length > 0 && (
              <div class="form-warning" role="status">
                Could not load {lookupErrors.join(', ')}. Some choices may be missing.
                {' '}<button type="button" class="btn btn-xs" onClick={loadLookups}>Retry</button>
              </div>
            )}
            {stepsError && <p class="form-error" role="alert" tabIndex={-1}>{stepsError}</p>}
            {steps.length === 0 && (
              <p class="text-muted">No steps yet. Add a step to choose who gets paged and how long to wait before the next step.</p>
            )}
            {steps.map((step, i) => {
              const headingId = `step-${i}-heading`;
              const delayErr = err(i, 'wait_seconds');
              const repeatErr = err(i, 'repeat_count');
              const intervalErr = err(i, 'repeat_interval_seconds');
              const targetsErr = err(i, 'targets');
              const repeats = Number(step.repeat_count) || 0;
              const stoppedAt = steps.findIndex((s, j) => j < i && s.is_terminal);
              const summaryId = `step-${i}-summary`;
              return (
                <div key={step._key} class="step-item" role="group" aria-labelledby={headingId}
                  aria-describedby={summaryId}>
                  <div class="step-item-header">
                    <h4 id={headingId} class="step-number">Step {i + 1}</h4>
                    <button type="button" class="btn btn-xs btn-danger" onClick={() => removeStep(i)}>
                      Remove<span class="sr-only"> step {i + 1}</span>
                    </button>
                  </div>
                  {stoppedAt >= 0 && (
                    <p class="form-warning">
                      This step never pages: step {stoppedAt + 1} stops escalation.
                      Clear "Stop escalating after this step" there to use it.
                    </p>
                  )}
                  <div class="step-fields">
                    <div class={`form-group${delayErr ? ' has-error' : ''}`}>
                      <label class="required" for={`step-${i}-delay`}>
                        Wait before the next step (minutes)
                      </label>
                      <input id={`step-${i}-delay`} type="number" min="0" max="43200" step="any" required
                        inputMode="decimal" value={step.delay_minutes}
                        aria-invalid={delayErr ? 'true' : undefined}
                        aria-describedby={describedBy(`step-${i}-delay-help`, delayErr && `step-${i}-delay-err`)}
                        onInput={e => updateStep(i, { delay_minutes: e.target.value })} />
                      <p id={`step-${i}-delay-help`} class="form-help">
                        Counted from this step's last page. Also the gap between target groups.
                      </p>
                      <FieldError id={`step-${i}-delay-err`} msg={delayErr} />
                    </div>
                    <div class={`form-group${repeatErr ? ' has-error' : ''}`}>
                      <label for={`step-${i}-repeat`}>Repeat pages</label>
                      <input id={`step-${i}-repeat`} type="number" min="0" max="100" step="1"
                        inputMode="numeric" value={step.repeat_count}
                        aria-invalid={repeatErr ? 'true' : undefined}
                        aria-describedby={describedBy(`step-${i}-repeat-help`, repeatErr && `step-${i}-repeat-err`)}
                        onInput={e => updateStep(i, { repeat_count: e.target.value })} />
                      <p id={`step-${i}-repeat-help`} class="form-help">
                        Extra times to page the same targets before moving on. 0 pages once.
                      </p>
                      <FieldError id={`step-${i}-repeat-err`} msg={repeatErr} />
                    </div>
                    {(repeats > 0 || intervalErr) && (
                      <div class={`form-group${intervalErr ? ' has-error' : ''}`}>
                        <label for={`step-${i}-interval`}>Repeat every (minutes)</label>
                        <input id={`step-${i}-interval`} type="number" min="0" max="43200" step="any"
                          inputMode="decimal" value={step.repeat_interval_minutes}
                          aria-invalid={intervalErr ? 'true' : undefined}
                          aria-describedby={describedBy(`step-${i}-interval-help`, intervalErr && `step-${i}-interval-err`)}
                          onInput={e => updateStep(i, { repeat_interval_minutes: e.target.value })} />
                        <p id={`step-${i}-interval-help`} class="form-help">
                          0 uses the wait time above.
                        </p>
                        <FieldError id={`step-${i}-interval-err`} msg={intervalErr} />
                      </div>
                    )}
                  </div>
                  <div class="step-targets">
                    <h5 class="step-targets-title" id={`step-${i}-targets-title`}>Who to page</h5>
                    <p class="form-help step-targets-help">
                      Each person is paged through their own{' '}
                      <a href="/settings/contact-methods">contact methods and notification rules</a>.
                      A channel is optional and gets one message each time this step pages.
                    </p>
                    {targetsErr && <p class="field-error" id={`step-${i}-targets-err`}>{targetsErr}</p>}
                    {(step.targets || []).length === 0 && (
                      <p class="form-help">Add at least one target.</p>
                    )}
                    {(step.targets || []).map((target, ti) => {
                      const idPrefix = `step-${i}-target-${ti}`;
                      const isOnCall = ON_CALL_TYPES.has(target.target_type);
                      const isChannel = target.target_type === 'channel';
                      const typeErr = err(i, 'target_type', ti);
                      const targetErr = err(i, 'target_id', ti);
                      const channelErr = err(i, 'channel_id', ti);
                      const targetLabel = isOnCall ? 'On-call schedule' : target.target_type === 'user' ? 'User' : 'Team';
                      const noOptions = isOnCall ? schedules.length === 0
                        : target.target_type === 'user' ? users.length === 0
                          : target.target_type === 'team' ? teams.length === 0 : false;
                      const startsGroup = ti > 0 && !target.simultaneous;
                      const picked = target.target_type === 'user' ? users.find(u => u.id === target.target_id) : null;
                      const pickedTeam = !picked && target.target_id ? teamOf(target) : '';
                      // Owners and admins get the per-urgency detail; everyone
                      // else falls back to the org-wide readiness check.
                      const reachMsg = picked ? (canSeeReach ? reachWarning(reach[picked.id], picked.name || picked.email)
                        : groupReachWarning(false, [picked.id], readiness, users))
                        : pickedTeam ? groupReachWarning(isOnCall, teamMembers[pickedTeam], readiness, users) : '';
                      return (
                        <div key={target._key} class={`target-item${startsGroup ? ' target-item-later' : ''}`} role="group"
                          aria-label={`Step ${i + 1}, target ${ti + 1}`}>
                          {ti > 0 && (
                            <div class="form-group form-check target-timing">
                              <label>
                                <input type="checkbox" checked={!!target.simultaneous}
                                  aria-describedby={`${idPrefix}-sim-help`}
                                  onChange={e => updateTarget(i, ti, { simultaneous: e.target.checked })} />
                                Page at the same time as the target above
                              </label>
                              <p id={`${idPrefix}-sim-help`} class="form-help">
                                {target.simultaneous
                                  ? 'Paged together with the target above.'
                                  : minutesToSeconds(step.delay_minutes) > 0
                                    ? `Paged ${formatSpan(minutesToSeconds(step.delay_minutes))} after the target above, if nobody has acknowledged.`
                                    : 'Paged right after the target above. Set a wait time to space them out.'}
                              </p>
                            </div>
                          )}
                          <div class="target-fields">
                            <div class={`form-group${typeErr ? ' has-error' : ''}`}>
                              <label for={`${idPrefix}-type`}>Page</label>
                              <select id={`${idPrefix}-type`} value={target.target_type}
                                aria-invalid={typeErr ? 'true' : undefined}
                                aria-describedby={typeErr ? `${idPrefix}-type-err` : undefined}
                                onChange={e => {
                                  const type = e.target.value;
                                  const auto = ON_CALL_TYPES.has(type) && schedules.length === 1 ? schedules[0].id : '';
                                  const patchT = { target_type: type, target_id: auto };
                                  // A channel-only target needs a channel, so pick the only one.
                                  if (type === 'channel' && !target.channel_id && channels.length === 1) {
                                    patchT.channel_id = channels[0].id;
                                  }
                                  updateTarget(i, ti, patchT);
                                }}>
                                {targetTypeOptions(target.target_type).map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
                              </select>
                              <FieldError id={`${idPrefix}-type-err`} msg={typeErr} />
                            </div>
                            {!isChannel && (
                              <div class={`form-group${targetErr ? ' has-error' : ''}`}>
                                <label for={`${idPrefix}-id`} class="required">{targetLabel}</label>
                                <select id={`${idPrefix}-id`} value={target.target_id} required
                                  aria-invalid={targetErr ? 'true' : undefined}
                                  aria-describedby={describedBy(reachMsg && `${idPrefix}-reach`, noOptions && `${idPrefix}-id-help`, targetErr && `${idPrefix}-id-err`)}
                                  onChange={e => updateTarget(i, ti, { target_id: e.target.value })}>
                                  <option value="">{isOnCall ? 'Select a schedule' : `Select a ${target.target_type}`}</option>
                                  {isOnCall && schedules.map(s => (
                                    <option key={s.id} value={s.id}>
                                      {s.name || 'Schedule'} ({s.rotation_interval}){s.current_on_call ? `, now ${s.current_on_call}` : ''}
                                    </option>
                                  ))}
                                  {target.target_type === 'user' && users.map(u => <option key={u.id} value={u.id}>{u.name || u.email}</option>)}
                                  {target.target_type === 'team' && teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
                                </select>
                                {reachMsg && <p id={`${idPrefix}-reach`} class="form-warning">{reachMsg}</p>}
                                {noOptions && (
                                  <p id={`${idPrefix}-id-help`} class="form-help">
                                    {isOnCall
                                      ? <>No on-call schedules yet. <a href="/oncall">Create a schedule</a> first.</>
                                      : target.target_type === 'user' ? 'No users found.' : <>No teams yet. <a href="/settings/teams">Create a team</a> first.</>}
                                  </p>
                                )}
                                <FieldError id={`${idPrefix}-id-err`} msg={targetErr} />
                              </div>
                            )}
                            <div class={`form-group${channelErr ? ' has-error' : ''}`}>
                              <label for={`${idPrefix}-channel`} class={isChannel ? 'required' : undefined}>
                                {isChannel ? 'Channel' : <>Also post to <span class="form-optional">(optional)</span></>}
                              </label>
                              <select id={`${idPrefix}-channel`} value={target.channel_id} required={isChannel}
                                aria-invalid={channelErr ? 'true' : undefined}
                                aria-describedby={describedBy(channels.length === 0 && `${idPrefix}-channel-help`, channelErr && `${idPrefix}-channel-err`)}
                                onChange={e => updateTarget(i, ti, { channel_id: e.target.value })}>
                                <option value="">{isChannel ? 'Select a channel' : 'No channel'}</option>
                                {channels.map(ch => (
                                  <option key={ch.id} value={ch.id}>{ch.name} ({ch.type}){ch.enabled === false ? ', disabled' : ''}</option>
                                ))}
                              </select>
                              {channels.length === 0 && (
                                <p id={`${idPrefix}-channel-help`} class="form-help">
                                  {isChannel
                                    ? <>No notification channels yet. <a href="/settings/channels">Add a channel</a> first.</>
                                    : <>No notification channels yet. <a href="/settings/channels">Add one</a> to also post pages there.</>}
                                </p>
                              )}
                              <FieldError id={`${idPrefix}-channel-err`} msg={channelErr} />
                            </div>
                          </div>
                          <button type="button" class="btn btn-xs btn-danger" onClick={() => removeTarget(i, ti)}>
                            Remove<span class="sr-only"> target {ti + 1} from step {i + 1}</span>
                          </button>
                        </div>
                      );
                    })}
                    <button type="button" class="btn btn-xs" onClick={() => addTarget(i)}>
                      Add target<span class="sr-only"> to step {i + 1}</span>
                    </button>
                  </div>
                  <div class="form-group form-check step-terminal">
                    <label>
                      <input type="checkbox" checked={!!step.is_terminal}
                        aria-describedby={`step-${i}-terminal-help`}
                        onChange={e => updateStep(i, { is_terminal: e.target.checked })} />
                      Stop escalating after this step
                    </label>
                    <p id={`step-${i}-terminal-help`} class="form-help">
                      Later steps never page and the policy does not loop. The alert stays open until someone acknowledges or resolves it.
                    </p>
                  </div>
                  <p id={summaryId} class="step-summary">
                    {describeStep(step, i, steps.length, editingSteps)}
                  </p>
                </div>
              );
            })}
            <div class="steps-builder-actions">
              <button type="button" class="btn btn-sm" onClick={addStep}>Add step</button>
              <button type="submit" class="btn btn-sm btn-primary" disabled={savingSteps}>
                {savingSteps ? 'Saving...' : 'Save steps'}
              </button>
            </div>
          </form>
        )}
      </Modal>
    </div>
  );
}
