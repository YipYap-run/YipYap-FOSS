import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { PageHeader, Card, LoadingPage, ErrorMessage, EmptyState, Modal, Spinner, useToast, ToastContainer } from '../../components/ui';
import { currentUser } from '../../state/auth';
import { canWrite } from '../../state/permissions.mjs';
import { ViewOnlyNote } from '../../components/view-only-note';
import './oncall.css';

/*
 * On-call schedules.
 *
 * API contract (internal/api/handlers/schedules.go):
 *  - GET /schedules lists schedules with current_on_call (null when nobody is
 *    on call, for example before effective_from).
 *  - GET /schedules/{id} adds participants in rotation order and active or
 *    upcoming overrides. GET /schedules/{id}/on-call returns on_call plus a
 *    status: on_call, not_started, no_members or invalid.
 *  - POST/PATCH /schedules take effective_from_local (wall-clock time in the
 *    schedule's timezone) and timezone; the server combines them. A 400 has
 *    {error, field, fields} so each message goes next to its input.
 *  - Overrides take start_at/end_at as instants. The form reads them in the
 *    viewer's own time zone and says so.
 *  - DELETE /schedules/{id} returns 409 with {policies} while an escalation
 *    step still pages the schedule.
 *  - Each participant carries pageable: false when they have no verified
 *    contact method with a notification rule this server and plan can
 *    deliver. A page to them reaches nobody, so the rotation list warns.
 */

const MAX_HOURS = 8784;
const MAX_REASON = 500;

const INTERVAL_LABELS = { daily: 'Daily', weekly: 'Weekly', custom_hours: 'Custom hours' };

// Friendly copy per server field. The server message is kept when it carries
// detail the generic copy would lose.
const SCHEDULE_FIELD_COPY = {
  team_id: m => /not found/.test(m) ? 'That team no longer exists. Pick another one.' : 'Pick a team.',
  rotation_interval: () => 'Pick daily, weekly or custom hours.',
  rotation_interval_hours: () => `Enter a whole number of hours from 1 to ${MAX_HOURS}.`,
  handoff_time: () => 'Use a 24-hour time like 09:00.',
  timezone: () => 'Pick a time zone from the list.',
  effective_from_local: () => 'Enter a valid start date and time.',
  effective_from: () => 'Enter a valid start date and time.',
};

const OVERRIDE_FIELD_COPY = {
  user_id: m => /member/.test(m) ? "Pick someone on this schedule's team." : 'Pick who covers the shift.',
  start_at: () => 'Enter a start date and time.',
  end_at: m => /after/.test(m) ? 'The end must be after the start.'
    : /future/.test(m) ? 'The end must be in the future.' : 'Enter an end date and time.',
  reason: () => `Keep the reason under ${MAX_REASON} characters.`,
};

// fieldErrors turns a 400 body into {field: message} using copy. effective_from
// errors land on the visible effective_from_local inputs.
function fieldErrors(err, copy) {
  const raw = (err && err.status === 400 && err.body && err.body.fields) || {};
  const out = {};
  for (const [k, msg] of Object.entries(raw)) {
    const key = k === 'effective_from' ? 'effective_from_local' : k;
    if (out[key]) continue;
    out[key] = copy[k] ? copy[k](String(msg || '')) : String(msg || '');
  }
  return out;
}

function errorText(err) {
  return (err && err.message) || 'Something went wrong. Try again.';
}

const browserTZ = (() => {
  try { return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'; } catch { return 'UTC'; }
})();

function emptyScheduleForm() {
  return {
    team_id: '',
    rotation_interval: 'weekly',
    rotation_interval_hours: 12,
    handoff_time: '09:00',
    effective_from_date: '',
    effective_from_time: '09:00',
    timezone: browserTZ,
  };
}

export function OnCallPage() {
  const { toasts, toast } = useToast();
  const [schedules, setSchedules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState(null);
  const [detailLoading, setDetailLoading] = useState(false);
  const [detailError, setDetailError] = useState(null);
  const detailHeading = useRef(null);
  const focusDetail = useRef(false);

  const [scheduleModal, setScheduleModal] = useState(null); // null | { id, form }
  const [overrideOpen, setOverrideOpen] = useState(false);
  const [confirm, setConfirm] = useState(null); // null | { kind, ... }

  async function load(selectId) {
    setError(null);
    try {
      const data = await get('/schedules');
      const list = Array.isArray(data) ? data : (data.schedules || []);
      setSchedules(list);
      const want = selectId !== undefined ? selectId : selected;
      const keep = want && list.some(s => s.id === want) ? want : (list[0] && list[0].id);
      if (keep) loadDetail(keep); else { setSelected(null); setDetail(null); }
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  async function loadDetail(id) {
    setSelected(id);
    setDetailLoading(true);
    setDetailError(null);
    try {
      const [s, oncall] = await Promise.all([
        get(`/schedules/${id}`),
        get(`/schedules/${id}/on-call`).catch(() => null),
      ]);
      setDetail({ ...s, on_call: oncall });
    } catch (err) {
      setDetail(null);
      setDetailError(err);
    } finally {
      setDetailLoading(false);
    }
  }

  // Move focus to the detail heading after a list pick, so keyboard and
  // screen reader users land on what they opened (it sits below the list on
  // narrow screens).
  useEffect(() => {
    if (focusDetail.current && detail && !detailLoading && detailHeading.current) {
      focusDetail.current = false;
      detailHeading.current.focus();
    }
  }, [detail, detailLoading]);

  function pick(id) {
    focusDetail.current = true;
    loadDetail(id);
  }

  function openCreate() {
    setScheduleModal({ id: null, form: emptyScheduleForm() });
  }

  function openEdit(s) {
    const ef = s.effective_from_local || '';
    setScheduleModal({
      id: s.id,
      form: {
        team_id: s.team_id || '',
        rotation_interval: s.rotation_interval || 'daily',
        rotation_interval_hours: s.rotation_interval_hours || 12,
        handoff_time: s.handoff_time || '09:00',
        effective_from_date: ef.slice(0, 10),
        effective_from_time: ef.slice(11, 16),
        timezone: s.timezone || browserTZ,
      },
    });
  }

  async function onScheduleSaved(saved, wasEdit) {
    setScheduleModal(null);
    toast(wasEdit ? 'Schedule updated' : 'Schedule created');
    await load(saved && saved.id);
  }

  async function runConfirm() {
    const c = confirm;
    setConfirm({ ...c, busy: true, error: null, policies: null });
    try {
      if (c.kind === 'schedule') {
        await del(`/schedules/${c.id}`);
        setConfirm(null);
        toast('Schedule deleted');
        setSelected(null);
        setDetail(null);
        load(null);
      } else {
        await del(`/schedules/${c.scheduleId}/overrides/${c.id}`);
        setConfirm(null);
        toast('Override removed');
        loadDetail(c.scheduleId);
      }
    } catch (err) {
      if (err.status === 409 && err.body && Array.isArray(err.body.policies)) {
        setConfirm({ ...c, busy: false, blocked: true, policies: err.body.policies });
      } else {
        setConfirm({ ...c, busy: false, error: errorText(err) });
      }
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={() => { setLoading(true); load(); }} />;

  const writer = canWrite(currentUser.value?.role);
  const newButton = <button type="button" class="btn btn-primary" onClick={openCreate}>New schedule</button>;

  return (
    <div class="oncall-page">
      <PageHeader title="On-call" subtitle="Who is on call now, and who is next, for each team" actions={schedules.length > 0 && writer && newButton} />
      <ViewOnlyNote />

      {schedules.length === 0 ? (
        <Card>
          <EmptyState
            title="No on-call schedules yet"
            description="A schedule rotates a team's members through on-call shifts. Escalation steps can then page whoever is on call."
            action={writer ? newButton : undefined}
          />
        </Card>
      ) : (
        <div class="oncall-grid">
          <Card>
            <h2 class="oncall-section-title" id="oncall-list-title">Schedules</h2>
            <ul class="oncall-list" aria-labelledby="oncall-list-title">
              {schedules.map(s => (
                <li key={s.id}>
                  <button type="button" class={`oncall-list-item${selected === s.id ? ' is-selected' : ''}`}
                    aria-current={selected === s.id ? 'true' : undefined} onClick={() => pick(s.id)}>
                    <span class="oncall-list-name">{s.name || 'Unnamed team'}</span>
                    <span class="oncall-list-status">{listStatus(s)}</span>
                  </button>
                </li>
              ))}
            </ul>
          </Card>

          <Card class="oncall-detail" aria-busy={detailLoading ? 'true' : undefined}>
            {detailLoading && !detail && <div class="oncall-detail-loading"><Spinner /><span class="sr-only">Loading schedule</span></div>}
            {detailError && (
              <div class="form-error" role="alert">
                <p>Could not load this schedule. {errorText(detailError)}</p>
                <button type="button" class="btn btn-sm" onClick={() => loadDetail(selected)}>Retry</button>
              </div>
            )}
            {detail && (
              <ScheduleDetail
                detail={detail}
                writer={writer}
                headingRef={detailHeading}
                onEdit={() => openEdit(detail)}
                onDelete={() => setConfirm({ kind: 'schedule', id: detail.id, name: detail.name })}
                onAddOverride={() => setOverrideOpen(true)}
                onRemoveOverride={o => setConfirm({ kind: 'override', id: o.id, scheduleId: detail.id, name: o.user_email || 'this person', o })}
              />
            )}
          </Card>
        </div>
      )}

      {scheduleModal && (
        <ScheduleModal
          editId={scheduleModal.id}
          initial={scheduleModal.form}
          onClose={() => setScheduleModal(null)}
          onSaved={onScheduleSaved}
        />
      )}

      {overrideOpen && detail && (
        <OverrideModal
          schedule={detail}
          onClose={() => setOverrideOpen(false)}
          onSaved={() => { setOverrideOpen(false); toast('Override added'); loadDetail(detail.id); }}
        />
      )}

      {confirm && (
        <Modal open onClose={() => !confirm.busy && setConfirm(null)}
          title={confirm.kind === 'schedule' ? 'Delete schedule' : 'Remove override'}>
          {confirm.blocked ? (
            <div>
              <p role="alert">
                Escalation policies still page this schedule, so it cannot be deleted. Remove it from the steps of these policies first:
              </p>
              <ul class="oncall-policy-list">
                {confirm.policies.map(p => <li key={p.id}>{p.name}</li>)}
              </ul>
              <div class="oncall-modal-actions">
                <a class="btn btn-primary" href="/settings/escalation">Open escalation policies</a>
                <button type="button" class="btn" onClick={() => setConfirm(null)}>Close</button>
              </div>
            </div>
          ) : (
            <div>
              {confirm.kind === 'schedule' ? (
                <p>Delete the on-call schedule for <strong>{confirm.name}</strong>? Its overrides are deleted too. This cannot be undone.</p>
              ) : (
                <p>Remove the override for <strong>{confirm.name}</strong> ({formatRange(confirm.o.start_at, confirm.o.end_at)})? The regular rotation covers that time again.</p>
              )}
              {confirm.error && <p class="form-error" role="alert">{confirm.error}</p>}
              <div class="oncall-modal-actions">
                <button type="button" class="btn btn-danger" disabled={confirm.busy} onClick={runConfirm}>
                  {confirm.busy ? 'Working...' : (confirm.kind === 'schedule' ? 'Delete schedule' : 'Remove override')}
                </button>
                <button type="button" class="btn" disabled={confirm.busy} onClick={() => setConfirm(null)}>Cancel</button>
              </div>
            </div>
          )}
        </Modal>
      )}

      <ToastContainer toasts={toasts} />
    </div>
  );
}

function listStatus(s) {
  if (s.current_on_call) return s.current_on_call;
  if (s.effective_from && new Date(s.effective_from).getTime() > Date.now()) {
    return `Starts ${formatInstant(s.effective_from)}`;
  }
  return 'Nobody on call';
}

function ScheduleDetail({ detail, writer, headingRef, onEdit, onDelete, onAddOverride, onRemoveOverride }) {
  const participants = detail.participants || [];
  const unreachable = participants.filter(p => p.pageable === false);
  const overrides = detail.overrides || [];
  const canOverride = participants.length > 0;
  return (
    <div>
      <div class="oncall-detail-header">
        <h2 class="oncall-detail-title" ref={headingRef} tabIndex={-1}>{detail.name || 'Unnamed team'}</h2>
        {writer && <div class="oncall-detail-actions">
          <button type="button" class="btn btn-sm btn-primary" onClick={onAddOverride} disabled={!canOverride}
            aria-describedby={canOverride ? undefined : 'oncall-no-members'}>Add override</button>
          <button type="button" class="btn btn-sm" onClick={onEdit}>Edit</button>
          <button type="button" class="btn btn-sm btn-danger" onClick={onDelete}>Delete</button>
        </div>}
      </div>

      <OnCallNow detail={detail} />

      <section class="oncall-section" aria-labelledby="oncall-rotation-title">
        <h3 class="oncall-section-title" id="oncall-rotation-title">Rotation</h3>
        <dl class="oncall-facts">
          <div><dt>Hand off</dt><dd>{rotationSummary(detail)}</dd></div>
          <div><dt>Time zone</dt><dd>{detail.timezone || '-'}</dd></div>
          <div><dt>Starts</dt><dd>{detail.effective_from_local ? `${formatWallClock(detail.effective_from_local)} (${detail.timezone})` : '-'}</dd></div>
        </dl>
        {participants.length === 0 ? (
          <p class="text-muted" id="oncall-no-members">
            This team has no members, so nobody is on call. <a href="/settings/teams">Add people to the team</a>.
          </p>
        ) : (
          <>
            {unreachable.length > 0 && (
              <div class="form-warning oncall-unreachable" role="status" id="oncall-unreachable">
                <p>
                  {unreachable.length === 1
                    ? `${unreachable[0].user_email || 'One member'} has no verified contact method with a notification rule, so a page during their shift reaches nobody.`
                    : `${unreachable.length} members have no verified contact method with a notification rule, so a page during their shifts reaches nobody.`}
                </p>
                <p>Each person adds their own under <a href="/settings/contact-methods">Settings, Contact Methods</a>.</p>
              </div>
            )}
            <ol class="oncall-order" aria-label="Rotation order">
              {participants.map(p => (
                <li key={p.user_id}>
                  <span>{p.user_email || p.user_id}</span>
                  {detail.current_on_call_user_id === p.user_id && <span class="badge badge-sm badge-up">On call</span>}
                  {p.pageable === false && (
                    <span class="badge badge-sm badge-warning">Cannot be paged</span>
                  )}
                </li>
              ))}
            </ol>
          </>
        )}
      </section>

      <section class="oncall-section" aria-labelledby="oncall-overrides-title">
        <h3 class="oncall-section-title" id="oncall-overrides-title">Overrides</h3>
        {overrides.length === 0 ? (
          <p class="text-muted">No current or upcoming overrides. Add one when someone covers a shift for a teammate.</p>
        ) : (
          <ul class="oncall-overrides">
            {overrides.map(o => (
              <li key={o.id} class="oncall-override">
                <div class="oncall-override-main">
                  <div>
                    <strong>{o.user_email || o.user_id}</strong>{' '}
                    <span class={`badge badge-sm ${o.active ? 'badge-up' : 'badge-muted'}`}>{o.active ? 'Active' : 'Upcoming'}</span>
                  </div>
                  <div class="oncall-override-time">{formatRange(o.start_at, o.end_at)}</div>
                  {o.reason && <div class="oncall-override-reason">{o.reason}</div>}
                </div>
                {writer && <button type="button" class="btn btn-sm" onClick={() => onRemoveOverride(o)}
                  aria-label={`Remove override for ${o.user_email || 'this person'}, ${formatRange(o.start_at, o.end_at)}`}>
                  Remove
                </button>}
              </li>
            ))}
          </ul>
        )}
      </section>
    </div>
  );
}

function OnCallNow({ detail }) {
  const oc = detail.on_call;
  let body;
  let tone = 'muted';
  if (!oc) {
    body = <p>Could not check who is on call right now.</p>;
  } else if (oc.on_call) {
    tone = 'up';
    body = (
      <>
        <p class="oncall-now-who">
          {oc.user_email || oc.user_id}
          {oc.override && <span class="badge badge-sm badge-info">Override</span>}
        </p>
        {oc.override && oc.reason && <p class="text-muted">Reason: {oc.reason}</p>}
        {oc.until && <p class="text-muted">Until {formatInstant(oc.until)}</p>}
      </>
    );
  } else if (oc.status === 'not_started') {
    tone = 'info';
    body = (
      <>
        <p class="oncall-now-who">Not started yet</p>
        <p class="text-muted">
          Nobody is on call until the rotation starts on {formatInstant(oc.starts_at || detail.effective_from)}. Steps that page this schedule reach no one before then.
        </p>
      </>
    );
  } else if (oc.status === 'no_members') {
    tone = 'warning';
    body = (
      <>
        <p class="oncall-now-who">Nobody is on call</p>
        <p class="text-muted">This team has no members. <a href="/settings/teams">Add people to the team</a> so escalation steps have someone to page.</p>
      </>
    );
  } else {
    tone = 'warning';
    body = (
      <>
        <p class="oncall-now-who">Nobody is on call</p>
        <p class="text-muted">The schedule settings need attention{oc.message ? `: ${oc.message}` : ''}. Edit the schedule to fix them.</p>
      </>
    );
  }
  return (
    <section class={`oncall-now oncall-now-${tone}`} aria-labelledby="oncall-now-title">
      <h3 class="oncall-section-title" id="oncall-now-title">On call now</h3>
      {body}
    </section>
  );
}

function ScheduleModal({ editId, initial, onClose, onSaved }) {
  const [form, setForm] = useState(initial);
  const [teams, setTeams] = useState(null);
  const [teamsError, setTeamsError] = useState(null);
  const [errors, setErrors] = useState({});
  const [formError, setFormError] = useState(null);
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);

  async function loadTeams() {
    setTeamsError(null);
    try {
      const data = await get('/teams');
      setTeams(Array.isArray(data) ? data : (data.teams || []));
    } catch (err) {
      setTeamsError(err);
      setTeams([]);
    }
  }
  useEffect(() => { loadTeams(); }, []);

  // After a failed save, move focus to the first invalid input, or to the
  // summary when the error is not tied to a field.
  useEffect(() => {
    if (!formRef.current || (!formError && Object.keys(errors).length === 0)) return;
    const el = formRef.current.querySelector('[aria-invalid="true"]') || formRef.current.querySelector('.form-error');
    if (el) el.focus();
  }, [errors, formError]);

  const set = (k, v, field = k) => {
    setForm(f => ({ ...f, [k]: v }));
    if (errors[field]) setErrors(e => { const n = { ...e }; delete n[field]; return n; });
  };

  async function submit(e) {
    e.preventDefault();
    if (saving) return;
    const local = {};
    if (!form.team_id) local.team_id = SCHEDULE_FIELD_COPY.team_id('');
    if (!form.effective_from_date || !form.effective_from_time) local.effective_from_local = SCHEDULE_FIELD_COPY.effective_from_local();
    if (!/^([01]\d|2[0-3]):[0-5]\d$/.test(form.handoff_time || '')) local.handoff_time = SCHEDULE_FIELD_COPY.handoff_time();
    if (form.rotation_interval === 'custom_hours') {
      const h = Number(form.rotation_interval_hours);
      if (!Number.isInteger(h) || h < 1 || h > MAX_HOURS) local.rotation_interval_hours = SCHEDULE_FIELD_COPY.rotation_interval_hours();
    }
    if (Object.keys(local).length) {
      setFormError(null);
      setErrors(local);
      return;
    }
    // The typed date and time go as-is; the server reads them in the
    // schedule's timezone, not the browser's.
    const body = {
      team_id: form.team_id,
      rotation_interval: form.rotation_interval,
      handoff_time: form.handoff_time,
      effective_from_local: `${form.effective_from_date}T${form.effective_from_time}`,
      timezone: form.timezone,
    };
    if (form.rotation_interval === 'custom_hours') body.rotation_interval_hours = Number(form.rotation_interval_hours);
    setSaving(true);
    setErrors({});
    setFormError(null);
    try {
      const saved = editId ? await patch(`/schedules/${editId}`, body) : await post('/schedules', body);
      onSaved(saved, !!editId);
    } catch (err) {
      const fe = fieldErrors(err, SCHEDULE_FIELD_COPY);
      if (Object.keys(fe).length) setErrors(fe);
      else setFormError(errorText(err));
      setSaving(false);
    }
  }

  const inv = k => (errors[k] ? 'true' : undefined);
  const desc = (k, ...hints) => [errors[k] ? `sched-${k}-error` : '', ...hints].filter(Boolean).join(' ') || undefined;
  const err = k => errors[k] && <p class="field-error" id={`sched-${k}-error`}>{errors[k]}</p>;
  const nErrors = Object.keys(errors).length;
  const tzDiffers = form.timezone && form.timezone !== browserTZ;

  return (
    <Modal open onClose={onClose} title={editId ? 'Edit schedule' : 'New schedule'}>
      <form ref={formRef} onSubmit={submit} noValidate>
        {formError && <p class="form-error" role="alert" tabIndex={-1}>{formError}</p>}
        {nErrors > 0 && <p class="sr-only" role="alert">{nErrors === 1 ? 'One field needs attention.' : `${nErrors} fields need attention.`}</p>}

        {teams === null ? (
          <div class="form-group"><Spinner /><span class="sr-only">Loading teams</span></div>
        ) : teamsError ? (
          <div class="form-error" role="alert">
            <p>Could not load teams. {errorText(teamsError)}</p>
            <button type="button" class="btn btn-sm" onClick={() => { setTeams(null); loadTeams(); }}>Retry</button>
          </div>
        ) : teams.length === 0 ? (
          <div class="oncall-callout">
            <p><strong>No teams yet</strong></p>
            <p>A schedule rotates the members of a team. Create a team first, then come back.</p>
            <a href="/settings/teams" class="btn btn-sm btn-primary">Create a team</a>
          </div>
        ) : (
          <div class={`form-group${errors.team_id ? ' has-error' : ''}`}>
            <label class="required" for="sched-team">Team</label>
            <select id="sched-team" value={form.team_id} onChange={e => set('team_id', e.target.value)}
              required aria-required="true" aria-invalid={inv('team_id')} aria-describedby={desc('team_id', 'sched-team-hint')}>
              <option value="">Select a team</option>
              {teams.map(t => <option key={t.id} value={t.id}>{t.name || t.id}</option>)}
            </select>
            <p class="form-help" id="sched-team-hint">Members take turns in their team order.</p>
            {err('team_id')}
          </div>
        )}

        <div class={`form-group${errors.rotation_interval ? ' has-error' : ''}`}>
          <label class="required" for="sched-interval">Rotation</label>
          <select id="sched-interval" value={form.rotation_interval} onChange={e => set('rotation_interval', e.target.value)}
            aria-invalid={inv('rotation_interval')} aria-describedby={desc('rotation_interval', 'sched-interval-hint')}>
            <option value="daily">Daily</option>
            <option value="weekly">Weekly</option>
            <option value="custom_hours">Custom hours</option>
          </select>
          <p class="form-help" id="sched-interval-hint">{intervalHint(form)}</p>
          {err('rotation_interval')}
        </div>

        {form.rotation_interval === 'custom_hours' && (
          <div class={`form-group${errors.rotation_interval_hours ? ' has-error' : ''}`}>
            <label class="required" for="sched-hours">Shift length in hours</label>
            <input id="sched-hours" type="number" inputMode="numeric" min="1" max={MAX_HOURS} step="1"
              value={form.rotation_interval_hours} onInput={e => set('rotation_interval_hours', e.target.value)}
              required aria-required="true" aria-invalid={inv('rotation_interval_hours')} aria-describedby={desc('rotation_interval_hours')} />
            {err('rotation_interval_hours')}
          </div>
        )}

        <div class={`form-group${errors.handoff_time ? ' has-error' : ''}`}>
          <label class="required" for="sched-handoff">Handoff time</label>
          <input id="sched-handoff" type="time" value={form.handoff_time} onInput={e => set('handoff_time', e.target.value)}
            required aria-required="true" aria-invalid={inv('handoff_time')} aria-describedby={desc('handoff_time', 'sched-tz-hint')} />
          {err('handoff_time')}
        </div>

        <fieldset class={`form-group oncall-fieldset${errors.effective_from_local ? ' has-error' : ''}`}
          aria-describedby={desc('effective_from_local', 'sched-start-hint')}>
          <legend class="required">Rotation starts</legend>
          <div class="oncall-datetime">
            <label class="sr-only" for="sched-start-date">Start date</label>
            <input id="sched-start-date" type="date" value={form.effective_from_date}
              onInput={e => set('effective_from_date', e.target.value, 'effective_from_local')}
              required aria-required="true" aria-invalid={inv('effective_from_local')} aria-describedby={desc('effective_from_local', 'sched-start-hint')} />
            <label class="sr-only" for="sched-start-time">Start time</label>
            <input id="sched-start-time" type="time" value={form.effective_from_time}
              onInput={e => set('effective_from_time', e.target.value, 'effective_from_local')}
              required aria-required="true" aria-invalid={inv('effective_from_local')} aria-describedby={desc('effective_from_local', 'sched-start-hint')} />
          </div>
          <p class="form-help" id="sched-start-hint">The first person in the team order is on call from this moment until the next handoff.</p>
          {err('effective_from_local')}
        </fieldset>

        <div class={`form-group${errors.timezone ? ' has-error' : ''}`}>
          <label class="required" for="sched-timezone">Time zone</label>
          <select id="sched-timezone" value={form.timezone} onChange={e => set('timezone', e.target.value)}
            required aria-required="true" aria-invalid={inv('timezone')} aria-describedby={desc('timezone', 'sched-tz-hint')}>
            {zoneOptions(form.timezone).map(tz => <option key={tz} value={tz}>{tz}</option>)}
          </select>
          <p class="form-help" id="sched-tz-hint">
            The start and handoff times are in {form.timezone || 'this time zone'}.
            {tzDiffers && ` Your browser is in ${browserTZ}.`}
          </p>
          {err('timezone')}
        </div>

        <div class="oncall-modal-actions">
          <button type="submit" class="btn btn-primary" disabled={saving || teams === null || (teams && teams.length === 0)}>
            {saving ? 'Saving...' : (editId ? 'Save schedule' : 'Create schedule')}
          </button>
          <button type="button" class="btn" onClick={onClose} disabled={saving}>Cancel</button>
        </div>
      </form>
    </Modal>
  );
}

function OverrideModal({ schedule, onClose, onSaved }) {
  const participants = schedule.participants || [];
  const [form, setForm] = useState(() => {
    const now = new Date();
    return { user_id: '', start_date: isoDate(now), start_time: isoTime(now), end_date: '', end_time: '', reason: '' };
  });
  const [errors, setErrors] = useState({});
  const [formError, setFormError] = useState(null);
  const [saving, setSaving] = useState(false);
  const formRef = useRef(null);

  useEffect(() => {
    if (!formRef.current || (!formError && Object.keys(errors).length === 0)) return;
    const el = formRef.current.querySelector('[aria-invalid="true"]') || formRef.current.querySelector('.form-error');
    if (el) el.focus();
  }, [errors, formError]);

  const set = (k, v, field = k) => {
    setForm(f => ({ ...f, [k]: v }));
    if (errors[field]) setErrors(e => { const n = { ...e }; delete n[field]; return n; });
  };

  async function submit(e) {
    e.preventDefault();
    if (saving) return;
    // The date and time inputs are read in the viewer's zone; a string with
    // no offset parses as local time.
    const start = form.start_date && form.start_time ? new Date(`${form.start_date}T${form.start_time}`) : null;
    const end = form.end_date && form.end_time ? new Date(`${form.end_date}T${form.end_time}`) : null;
    const local = {};
    if (!form.user_id) local.user_id = OVERRIDE_FIELD_COPY.user_id('');
    if (!start || isNaN(start)) local.start_at = OVERRIDE_FIELD_COPY.start_at();
    if (!end || isNaN(end)) local.end_at = OVERRIDE_FIELD_COPY.end_at('');
    else if (start && !isNaN(start) && end <= start) local.end_at = OVERRIDE_FIELD_COPY.end_at('after');
    else if (end <= new Date()) local.end_at = OVERRIDE_FIELD_COPY.end_at('future');
    if (form.reason.trim().length > MAX_REASON) local.reason = OVERRIDE_FIELD_COPY.reason();
    if (Object.keys(local).length) {
      setFormError(null);
      setErrors(local);
      return;
    }
    setSaving(true);
    setErrors({});
    setFormError(null);
    try {
      await post(`/schedules/${schedule.id}/overrides`, {
        user_id: form.user_id,
        start_at: start.toISOString(),
        end_at: end.toISOString(),
        reason: form.reason.trim(),
      });
      onSaved();
    } catch (err) {
      const fe = fieldErrors(err, OVERRIDE_FIELD_COPY);
      if (Object.keys(fe).length) setErrors(fe);
      else setFormError(errorText(err));
      setSaving(false);
    }
  }

  const inv = k => (errors[k] ? 'true' : undefined);
  const desc = (k, ...hints) => [errors[k] ? `ovr-${k}-error` : '', ...hints].filter(Boolean).join(' ') || undefined;
  const err = k => errors[k] && <p class="field-error" id={`ovr-${k}-error`}>{errors[k]}</p>;
  const nErrors = Object.keys(errors).length;

  return (
    <Modal open onClose={onClose} title={`Add override: ${schedule.name || 'schedule'}`}>
      <form ref={formRef} onSubmit={submit} noValidate>
        {formError && <p class="form-error" role="alert" tabIndex={-1}>{formError}</p>}
        {nErrors > 0 && <p class="sr-only" role="alert">{nErrors === 1 ? 'One field needs attention.' : `${nErrors} fields need attention.`}</p>}

        <div class={`form-group${errors.user_id ? ' has-error' : ''}`}>
          <label class="required" for="ovr-user">Who covers</label>
          <select id="ovr-user" value={form.user_id} onChange={e => set('user_id', e.target.value)}
            required aria-required="true" aria-invalid={inv('user_id')} aria-describedby={desc('user_id', 'ovr-user-hint')}>
            <option value="">Select a team member</option>
            {participants.map(p => <option key={p.user_id} value={p.user_id}>{p.user_email || p.user_id}</option>)}
          </select>
          <p class="form-help" id="ovr-user-hint">Only members of this schedule's team can cover a shift.</p>
          {err('user_id')}
        </div>

        <fieldset class={`form-group oncall-fieldset${errors.start_at ? ' has-error' : ''}`} aria-describedby={desc('start_at', 'ovr-tz-hint')}>
          <legend class="required">Starts</legend>
          <div class="oncall-datetime">
            <label class="sr-only" for="ovr-start-date">Start date</label>
            <input id="ovr-start-date" type="date" value={form.start_date} onInput={e => set('start_date', e.target.value, 'start_at')}
              required aria-required="true" aria-invalid={inv('start_at')} aria-describedby={desc('start_at')} />
            <label class="sr-only" for="ovr-start-time">Start time</label>
            <input id="ovr-start-time" type="time" value={form.start_time} onInput={e => set('start_time', e.target.value, 'start_at')}
              required aria-required="true" aria-invalid={inv('start_at')} aria-describedby={desc('start_at')} />
          </div>
          {err('start_at')}
        </fieldset>

        <fieldset class={`form-group oncall-fieldset${errors.end_at ? ' has-error' : ''}`} aria-describedby={desc('end_at', 'ovr-tz-hint')}>
          <legend class="required">Ends</legend>
          <div class="oncall-datetime">
            <label class="sr-only" for="ovr-end-date">End date</label>
            <input id="ovr-end-date" type="date" value={form.end_date} min={form.start_date || undefined}
              onInput={e => set('end_date', e.target.value, 'end_at')}
              required aria-required="true" aria-invalid={inv('end_at')} aria-describedby={desc('end_at')} />
            <label class="sr-only" for="ovr-end-time">End time</label>
            <input id="ovr-end-time" type="time" value={form.end_time} onInput={e => set('end_time', e.target.value, 'end_at')}
              required aria-required="true" aria-invalid={inv('end_at')} aria-describedby={desc('end_at')} />
          </div>
          {err('end_at')}
          <p class="form-help" id="ovr-tz-hint">
            Times are in your time zone ({browserTZ}).
            {schedule.timezone && schedule.timezone !== browserTZ && ` The schedule hands off in ${schedule.timezone}.`}
          </p>
        </fieldset>

        <div class={`form-group${errors.reason ? ' has-error' : ''}`}>
          <label for="ovr-reason">Reason <span class="form-optional">(optional)</span></label>
          <textarea id="ovr-reason" rows={3} maxLength={MAX_REASON} value={form.reason}
            onInput={e => set('reason', e.target.value)} placeholder="For example: covering while Sam is on leave"
            aria-invalid={inv('reason')} aria-describedby={desc('reason', 'ovr-reason-count')} />
          <p class="form-help" id="ovr-reason-count">{form.reason.length} of {MAX_REASON} characters</p>
          {err('reason')}
        </div>

        <div class="oncall-modal-actions">
          <button type="submit" class="btn btn-primary" disabled={saving}>{saving ? 'Saving...' : 'Add override'}</button>
          <button type="button" class="btn" onClick={onClose} disabled={saving}>Cancel</button>
        </div>
      </form>
    </Modal>
  );
}

function intervalHint(form) {
  switch (form.rotation_interval) {
    case 'daily': return 'The next person takes over every day at the handoff time.';
    case 'weekly': return 'The next person takes over every 7 days, on the start date\'s weekday at the handoff time.';
    default: return 'The next person takes over after each shift. Shifts count from the handoff time on the start date.';
  }
}

function rotationSummary(s) {
  const at = s.handoff_time || '00:00';
  switch (s.rotation_interval) {
    case 'daily': return `Daily at ${at}`;
    case 'weekly': {
      const day = s.effective_from_local ? weekdayOf(s.effective_from_local) : '';
      return day ? `Weekly, ${day}s at ${at}` : `Weekly at ${at}`;
    }
    case 'custom_hours': return `Every ${s.rotation_interval_hours} hours, counted from ${at}`;
    default: return INTERVAL_LABELS[s.rotation_interval] || s.rotation_interval || '-';
  }
}

// wallClockDate builds a Date whose UTC fields hold a schedule-local wall
// clock, so formatting with timeZone UTC shows it without conversion.
function wallClockDate(local) {
  const m = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})/.exec(local || '');
  if (!m) return null;
  return new Date(Date.UTC(+m[1], +m[2] - 1, +m[3], +m[4], +m[5]));
}

function formatWallClock(local) {
  const d = wallClockDate(local);
  if (!d) return local || '-';
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short', timeZone: 'UTC' });
}

function weekdayOf(local) {
  const d = wallClockDate(local);
  return d ? d.toLocaleDateString(undefined, { weekday: 'long', timeZone: 'UTC' }) : '';
}

function formatInstant(ts) {
  if (!ts) return '-';
  const d = new Date(ts);
  if (isNaN(d)) return '-';
  return d.toLocaleString(undefined, { dateStyle: 'medium', timeStyle: 'short' });
}

function formatRange(a, b) {
  return `${formatInstant(a)} to ${formatInstant(b)}`;
}

function pad(n) { return String(n).padStart(2, '0'); }
function isoDate(d) { return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`; }
function isoTime(d) { return `${pad(d.getHours())}:${pad(d.getMinutes())}`; }

// zoneOptions lists IANA zones from the browser, always including UTC and
// the current value so an existing schedule's zone stays selectable.
function zoneOptions(current) {
  let zones = [];
  try { zones = Intl.supportedValuesOf('timeZone'); } catch { zones = []; }
  const set = new Set(zones);
  set.add('UTC');
  if (current) set.add(current);
  return [...set].sort();
}
