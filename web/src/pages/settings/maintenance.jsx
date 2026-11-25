import { canWrite } from '../../state/permissions.mjs';
import { currentUser } from '../../state/auth';
import { useState, useEffect, useRef, useMemo } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, EmptyState, formatTime, useToast, ToastContainer } from '../../components/ui';
import { browserTimeZone, timeZoneGroups, zonedLocalToDate, formatInZone, formatLocalWall, parseLocal } from '../../utils/timezone';

/*
 * Maintenance windows.
 *
 * API contract (POST /maintenance-windows, PATCH /maintenance-windows/{id}):
 *  - name is required; the end must be after the start (400 otherwise).
 *  - Times are wall-clock values in the window's own zone: start_local,
 *    end_local and recurrence_end_local ("YYYY-MM-DDTHH:mm") plus timezone
 *    (an IANA name, default UTC). The server resolves them and returns the
 *    same *_local fields, so Edit never converts through the browser zone.
 *    Recurring windows repeat those wall-clock times in that zone (DST safe).
 *  - recurrence_end_local is optional. Omit it for no end; on PATCH, ""
 *    clears an existing end date.
 *  - monitor_id is "" for every monitor in the org, or a monitor id in the
 *    caller's org. Any other id returns 404.
 *  - suppress_alerts defaults to true when omitted. Alerts that start during
 *    a suppressing window are paged when it ends only if still down.
 *  - days_of_week is a JSON-encoded array string ("[1,3]"), 0 = Sunday.
 *  - Responses carry active_now (an occurrence covers the current time),
 *    which drives the "In progress" badge for repeating windows.
 *  - Errors come back as {error} and are shown inside the modal.
 */

const DAY_NAMES = ['Sun', 'Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat'];

// Match the server limits in handlers/maintenance.go.
const NAME_MAX = 200;
const DESCRIPTION_MAX = 2000;

// GET /monitors returns at most 100 per page (50 by default). Walk every
// page so orgs with many monitors can pick any of them and the list does
// not mislabel windows on later monitors as deleted.
const MONITOR_PAGE = 100;

// Alert timelines link to a window as /settings/maintenance#mw-<id>.
export const WINDOW_ANCHOR_PREFIX = 'mw-';

export function windowIdFromHash(hash) {
  const h = (hash || '').replace(/^#/, '');
  if (!h.startsWith(WINDOW_ANCHOR_PREFIX)) return '';
  try {
    return decodeURIComponent(h.slice(WINDOW_ANCHOR_PREFIX.length));
  } catch {
    return '';
  }
}
const MONITOR_PAGE_CAP = 100;

export async function fetchAllMonitors(getFn = get) {
  const all = [];
  for (let page = 0; page < MONITOR_PAGE_CAP; page++) {
    const d = await getFn(`/monitors?limit=${MONITOR_PAGE}&offset=${page * MONITOR_PAGE}`);
    const rows = (d && d.monitors) || (Array.isArray(d) ? d : []);
    all.push(...rows);
    if (rows.length < MONITOR_PAGE) break;
  }
  return all;
}

const EMPTY_FORM = {
  name: '', description: '', start_at: '', end_at: '', monitor_id: '', public: false,
  suppress_alerts: true, recurrence_type: 'none', recurrence_end_at: '', days_of_week: [], day_of_month: 1,
  timezone: 'UTC',
};

// toLocalInput turns an RFC3339 timestamp into the "YYYY-MM-DDTHH:mm" value
// a datetime-local input expects, in the browser's time zone.
export function toLocalInput(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  if (isNaN(d.getTime())) return '';
  const pad = n => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}T${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function parseDays(v) {
  if (Array.isArray(v)) return v;
  try {
    const days = JSON.parse(v || '[]');
    return Array.isArray(days) ? days.filter(d => Number.isInteger(d) && d >= 0 && d <= 6) : [];
  } catch {
    return [];
  }
}

function formFromWindow(w) {
  return {
    name: w.name || '',
    description: w.description || '',
    start_at: w.start_local || toLocalInput(w.start_at),
    end_at: w.end_local || toLocalInput(w.end_at),
    monitor_id: w.monitor_id || '',
    public: !!w.public,
    suppress_alerts: w.suppress_alerts !== false,
    recurrence_type: w.recurrence_type || 'none',
    recurrence_end_at: w.recurrence_end_local || toLocalInput(w.recurrence_end_at),
    days_of_week: parseDays(w.days_of_week),
    day_of_month: w.day_of_month || 1,
    timezone: w.timezone || 'UTC',
  };
}

// buildMaintenanceBody turns the form into the request body. Times go as
// wall-clock values plus the chosen zone; the server combines them. When
// editing, an empty recurrence end is sent as "" so the server clears it.
export function buildMaintenanceBody(form, { editing = false } = {}) {
  const recurrence = form.recurrence_type || 'none';
  const body = {
    name: form.name.trim(),
    description: form.description,
    timezone: form.timezone || 'UTC',
    start_local: form.start_at,
    end_local: form.end_at,
    monitor_id: form.monitor_id || '',
    public: form.public,
    suppress_alerts: form.suppress_alerts,
    recurrence_type: recurrence,
    days_of_week: recurrence === 'weekly' ? JSON.stringify([...(form.days_of_week || [])].sort((a, b) => a - b)) : '[]',
    day_of_month: recurrence === 'monthly' ? (form.day_of_month || 1) : 0,
  };
  if (recurrence !== 'none' && form.recurrence_end_at) {
    body.recurrence_end_local = form.recurrence_end_at;
  } else if (editing) {
    body.recurrence_end_local = '';
  }
  return body;
}

// validate returns the first problem as {field, message}, or null.
function validate(form) {
  if (!form.name.trim()) return { field: 'name', message: 'Enter a name.' };
  if (form.name.length > NAME_MAX) return { field: 'name', message: `Use ${NAME_MAX} characters or fewer for the name.` };
  if ((form.description || '').length > DESCRIPTION_MAX) return { field: 'description', message: `Use ${DESCRIPTION_MAX} characters or fewer for the description.` };
  if (!form.start_at) return { field: 'start', message: 'Enter a start date and time.' };
  if (!form.end_at) return { field: 'end', message: 'Enter an end date and time.' };
  if (form.end_at <= form.start_at) return { field: 'end', message: 'The end must be after the start.' };
  if (form.recurrence_type === 'weekly' && !(form.days_of_week || []).length) return { field: 'days', message: 'Pick at least one day of the week.' };
  if (form.recurrence_type === 'monthly') {
    const d = Number(form.day_of_month);
    if (!Number.isInteger(d) || d < 1 || d > 31) return { field: 'dom', message: 'Enter a day of the month from 1 to 31.' };
  }
  if (form.recurrence_type !== 'none' && form.recurrence_end_at && form.recurrence_end_at < form.start_at) {
    return { field: 'recur-end', message: 'The repeat end date must be on or after the start.' };
  }
  return null;
}

// serverProblem maps an API error to a friendly message and the field it
// belongs to, so the right input gets marked and focused.
function serverProblem(e) {
  const raw = (e && e.message) || '';
  if (e && e.readonly) return { field: '', message: raw };
  if (/^timezone/i.test(raw)) return { field: 'tz', message: 'Pick a time zone from the list.' };
  if (/^start_local/i.test(raw)) return { field: 'start', message: 'Enter a valid start date and time.' };
  if (/^end_local/i.test(raw)) return { field: 'end', message: 'Enter a valid end date and time.' };
  if (/^recurrence_end_local/i.test(raw)) return { field: 'recur-end', message: 'Enter a valid repeat end date.' };
  if (/monitor not found/i.test(raw)) return { field: 'monitor', message: 'That monitor no longer exists. Pick another one.' };
  if (/^name must be/i.test(raw)) return { field: 'name', message: `Use ${NAME_MAX} characters or fewer for the name.` };
  if (/^name/i.test(raw)) return { field: 'name', message: 'Enter a name.' };
  if (/^description/i.test(raw)) return { field: 'description', message: `Use ${DESCRIPTION_MAX} characters or fewer for the description.` };
  if (/^recurrence_end_at/i.test(raw) || /recurrence_end_at/i.test(raw)) return { field: 'recur-end', message: 'The repeat end date must be on or after the start.' };
  if (/end_at must be after/i.test(raw)) return { field: 'end', message: 'The end must be after the start.' };
  if (/start_at and end_at/i.test(raw)) return { field: 'start', message: 'Enter a start and an end.' };
  if (/days_of_week/i.test(raw)) return { field: 'days', message: 'Pick at least one day of the week.' };
  if (/day_of_month/i.test(raw)) return { field: 'dom', message: 'Enter a day of the month from 1 to 31.' };
  return { field: '', message: raw || 'Could not save the maintenance window. Try again.' };
}

// windowStatus classifies a window for the list badge. The server's
// active_now covers repeating windows; one-off windows also fall back to
// their start and end.
export function windowStatus(w, now = Date.now()) {
  const start = new Date(w.start_at).getTime();
  const end = new Date(w.end_at).getTime();
  if (w.active_now) return 'active';
  if (w.recurrence_type && w.recurrence_type !== 'none') {
    if (w.recurrence_end_at && new Date(w.recurrence_end_at).getTime() < now) return 'ended';
    if (start > now) return 'upcoming';
    return 'recurring';
  }
  if (now < start) return 'upcoming';
  if (now <= end) return 'active';
  return 'ended';
}

const STATUS_LABEL = {
  active: { text: 'In progress', cls: 'badge-warning' },
  upcoming: { text: 'Scheduled', cls: 'badge-info' },
  recurring: { text: 'Repeating', cls: 'badge-info' },
  ended: { text: 'Ended', cls: 'badge-muted' },
};
const STATUS_ORDER = { active: 0, recurring: 1, upcoming: 2, ended: 3 };

// dayDiff counts calendar days between two wall-clock values.
function dayDiff(a, b) {
  const pa = parseLocal(a);
  const pb = parseLocal(b);
  if (!pa || !pb) return 0;
  return Math.round((Date.UTC(pb.y, pb.mo - 1, pb.d) - Date.UTC(pa.y, pa.mo - 1, pa.d)) / 86400000);
}

// recurrenceSummary describes when a repeating window recurs, using its
// own zone's wall-clock dates.
function recurrenceSummary(w) {
  const type = w.recurrence_type || 'none';
  if (type === 'none') return '';
  let s = '';
  if (type === 'daily') s = 'Repeats daily';
  if (type === 'weekly') {
    const days = parseDays(w.days_of_week).sort((a, b) => a - b).map(d => DAY_NAMES[d]);
    s = days.length ? `Repeats weekly on ${days.join(', ')}` : 'Repeats weekly';
  }
  if (type === 'monthly') s = `Repeats monthly on day ${w.day_of_month || 1}`;
  if (w.start_local) s += ` from ${formatLocalWall(w.start_local, { dateOnly: true })}`;
  if (w.recurrence_end_at) {
    s += ` until ${w.recurrence_end_local ? formatLocalWall(w.recurrence_end_local, { dateOnly: true }) : formatTime(w.recurrence_end_at)}`;
  }
  return s;
}

// windowTimes is the list's "when" text in the window's own zone. A
// repeating window shows its daily time span; an overnight span (end wall
// time on or before the start) says so.
function windowTimes(w) {
  if (!w.start_local || !w.end_local) return `${formatTime(w.start_at)} to ${formatTime(w.end_at)}`;
  const repeating = w.recurrence_type && w.recurrence_type !== 'none';
  if (!repeating) {
    const sameDay = dayDiff(w.start_local, w.end_local) === 0;
    return `${formatLocalWall(w.start_local)} to ${sameDay ? formatLocalWall(w.end_local, { timeOnly: true }) : formatLocalWall(w.end_local)}`;
  }
  let days = dayDiff(w.start_local, w.end_local);
  if (days <= 0 && w.end_local.slice(11) <= w.start_local.slice(11)) days = 1;
  const later = days === 1 ? ' the next day' : days > 1 ? ` ${days} days later` : '';
  return `${formatLocalWall(w.start_local, { timeOnly: true })} to ${formatLocalWall(w.end_local, { timeOnly: true })}${later}`;
}

// viewerTimes shows a one-off window's start and end in the viewer's zone
// when the window uses another one. Repeating windows are left out: their
// offset to the viewer can change between occurrences.
function viewerTimes(w, viewerZone) {
  const tz = w.timezone || 'UTC';
  if (tz === viewerZone || (w.recurrence_type && w.recurrence_type !== 'none')) return '';
  const start = new Date(w.start_at);
  const end = new Date(w.end_at);
  if (isNaN(start.getTime()) || isNaN(end.getTime())) return '';
  return `${formatInZone(start, viewerZone)} to ${formatInZone(end, viewerZone)} your time`;
}

// inBrowserZone shows the start and end in the viewer's own zone when the
// window uses a different one, so a typo in the zone is easy to spot.
function inBrowserZone(form, viewerZone) {
  const tz = form.timezone || 'UTC';
  if (tz === viewerZone || !form.start_at || !form.end_at) return '';
  const start = zonedLocalToDate(form.start_at, tz);
  const end = zonedLocalToDate(form.end_at, tz);
  if (!start || !end || end <= start) return '';
  return `${formatInZone(start, viewerZone)} to ${formatInZone(end, viewerZone)} in your time zone (${viewerZone}).`;
}

export function MaintenanceTab() {
  const writer = canWrite(currentUser.value?.role);
  const [windows, setWindows] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState(EMPTY_FORM);
  const [editId, setEditId] = useState(null);
  const [monitors, setMonitors] = useState([]);
  const [monitorsError, setMonitorsError] = useState(false);
  const [saving, setSaving] = useState(false);
  const [formError, setFormError] = useState(null);
  const [pendingDelete, setPendingDelete] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  // A link from an alert timeline names one window; it is highlighted and
  // focused, or a note says it no longer exists.
  const [linkedId, setLinkedId] = useState(() => windowIdFromHash(typeof location !== 'undefined' ? location.hash : ''));
  const formRef = useRef(null);
  const { toasts, toast } = useToast();
  const viewerZone = useMemo(() => browserTimeZone(), []);
  const zoneGroups = useMemo(
    () => (showModal ? timeZoneGroups([viewerZone, form.timezone]) : null),
    [showModal, viewerZone, form.timezone],
  );

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      const data = await get('/maintenance-windows');
      setWindows((data && data.windows) || data || []);
    } catch (e) {
      setLoadError(e);
    }
    setLoading(false);
  }

  function loadMonitors() {
    setMonitorsError(false);
    return fetchAllMonitors()
      .then(setMonitors)
      .catch(() => setMonitorsError(true));
  }

  useEffect(() => { load(); loadMonitors(); }, []);

  useEffect(() => {
    const onHash = () => setLinkedId(windowIdFromHash(location.hash));
    window.addEventListener('hashchange', onHash);
    return () => window.removeEventListener('hashchange', onHash);
  }, []);

  useEffect(() => {
    if (loading || !linkedId) return;
    const el = document.getElementById(WINDOW_ANCHOR_PREFIX + linkedId);
    if (!el) return;
    el.scrollIntoView({ block: 'center' });
    el.focus({ preventScroll: true });
  }, [loading, linkedId]);

  // When a save fails, move focus to the rejected field (or the message
  // itself) so keyboard and screen reader users land on the problem.
  useEffect(() => {
    if (!formError || !formRef.current) return;
    const el = formRef.current.querySelector('input[aria-invalid="true"], select[aria-invalid="true"], textarea[aria-invalid="true"]')
      || formRef.current.querySelector('.form-error');
    if (el && el.focus) el.focus();
  }, [formError]);

  function openCreate() {
    setEditId(null);
    setForm({ ...EMPTY_FORM, timezone: viewerZone });
    setFormError(null);
    setShowModal(true);
  }

  function openEdit(w) {
    setEditId(w.id);
    setForm(formFromWindow(w));
    setFormError(null);
    setShowModal(true);
  }

  function closeModal() {
    if (saving) return;
    setShowModal(false);
    setEditId(null);
  }

  async function save() {
    if (saving) return;
    const problem = validate(form);
    if (problem) { setFormError(problem); return; }
    setFormError(null);
    setSaving(true);
    const editing = !!editId;
    try {
      const body = buildMaintenanceBody(form, { editing });
      if (editing) await patch(`/maintenance-windows/${editId}`, body);
      else await post('/maintenance-windows', body);
      setSaving(false);
      setShowModal(false);
      setEditId(null);
      toast(editing ? 'Maintenance window updated' : 'Maintenance window scheduled');
      load();
    } catch (e) {
      setSaving(false);
      const p = serverProblem(e);
      if (p.field === 'monitor') loadMonitors();
      setFormError(p);
    }
  }

  function askDelete(w) {
    setDeleteError('');
    setPendingDelete(w);
  }

  function closeDelete() {
    if (deleting) return;
    setPendingDelete(null);
  }

  async function confirmDelete() {
    if (!pendingDelete || deleting) return;
    setDeleting(true);
    setDeleteError('');
    try {
      await del(`/maintenance-windows/${pendingDelete.id}`);
      if (pendingDelete.id === linkedId) {
        // Deleting the linked window here is not the "already deleted" case.
        setLinkedId('');
        try { history.replaceState(history.state, '', location.pathname + location.search); } catch { /* ignore */ }
      }
      setDeleting(false);
      setPendingDelete(null);
      toast('Maintenance window deleted');
      load();
    } catch (e) {
      setDeleting(false);
      setDeleteError((e && e.message) || 'Could not delete the maintenance window. Try again.');
    }
  }

  if (loading) return <LoadingPage />;
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  const set = patchObj => setForm(f => ({ ...f, ...patchObj }));
  const monitorName = id => {
    if (!id) return 'All monitors';
    const m = monitors.find(x => x.id === id);
    if (m) return m.name || m.id;
    return monitorsError ? 'Single monitor' : 'Deleted monitor';
  };
  const errField = formError ? formError.field : '';
  const invalid = field => (errField === field ? 'true' : undefined);
  const describedBy = (field, hint) => [errField === field ? 'mw-error' : '', hint || ''].filter(Boolean).join(' ') || undefined;
  const now = Date.now();
  const sorted = [...windows].sort((a, b) => {
    const sa = STATUS_ORDER[windowStatus(a, now)];
    const sb = STATUS_ORDER[windowStatus(b, now)];
    if (sa !== sb) return sa - sb;
    const ta = new Date(a.start_at).getTime();
    const tb = new Date(b.start_at).getTime();
    return sa === STATUS_ORDER.ended ? tb - ta : ta - tb;
  });
  const monitorMissing = form.monitor_id && !monitors.some(m => m.id === form.monitor_id);

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Maintenance windows</h3>
        {writer && windows.length > 0 && <button type="button" class="btn btn-sm btn-primary" onClick={openCreate}>Schedule maintenance</button>}
      </div>
      <p class="text-muted mw-intro">
        Plan work that may take monitors down. While a window is in progress, its alerts are held so nobody gets paged for expected downtime. When it ends, alerts for monitors that are still down are paged; monitors that recovered page nobody.
      </p>
      {linkedId && !windows.some(w => w.id === linkedId) && (
        <p class="form-help mw-linked-missing" role="status">
          The maintenance window linked from that alert has been deleted.
        </p>
      )}

      {windows.length === 0 ? (
        <EmptyState
          title="No maintenance scheduled"
          description="Schedule a window before planned work so expected downtime does not page your team."
          action={writer ? <button type="button" class="btn btn-primary" onClick={openCreate}>Schedule maintenance</button> : null}
        />
      ) : (
        <ul class="settings-list mw-list" aria-label="Maintenance windows">
          {sorted.map(w => {
            const status = STATUS_LABEL[windowStatus(w, now)];
            const repeat = recurrenceSummary(w);
            const yours = viewerTimes(w, viewerZone);
            const linked = w.id === linkedId;
            return (
              <li key={w.id} id={WINDOW_ANCHOR_PREFIX + w.id} tabIndex={linked ? -1 : undefined}
                class={`settings-item mw-item${linked ? ' mw-item-linked' : ''}`}
                aria-current={linked ? 'true' : undefined}>
                <div class="mw-item-main">
                  <div class="mw-item-title">
                    <strong>{w.name}</strong>
                    <span class={`badge badge-sm ${status.cls}`}>{status.text}</span>
                    {w.public && <span class="badge badge-sm badge-muted">Public</span>}
                    {w.suppress_alerts === false
                      ? <span class="badge badge-sm badge-muted">Alerts not silenced</span>
                      : <span class="badge badge-sm badge-info">Suppresses alerts</span>}
                  </div>
                  <dl class="mw-item-meta text-muted">
                    <div><dt>Scope</dt><dd>{monitorName(w.monitor_id)}</dd></div>
                    <div><dt>When</dt><dd>{windowTimes(w)}</dd></div>
                    <div><dt>Time zone</dt><dd>{(w.timezone || 'UTC').replace(/_/g, ' ')}</dd></div>
                    {yours && <div><dt>In your time zone</dt><dd>({yours})</dd></div>}
                    {repeat && <div><dt>Repeats</dt><dd>{repeat}</dd></div>}
                  </dl>
                  {w.description && <p class="mw-item-desc">{w.description}</p>}
                </div>
                {writer && <div class="btn-group mw-item-actions">
                  <button type="button" class="btn btn-sm btn-outline" onClick={() => openEdit(w)} aria-label={`Edit ${w.name}`}>Edit</button>
                  <button type="button" class="btn btn-sm btn-danger" onClick={() => askDelete(w)} aria-label={`Delete ${w.name}`}>Delete</button>
                </div>}
              </li>
            );
          })}
        </ul>
      )}

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit maintenance window' : 'Schedule maintenance'}>
        <form ref={formRef} onSubmit={e => { e.preventDefault(); save(); }} noValidate aria-busy={saving ? 'true' : undefined}>
          {formError && <p id="mw-error" class="form-error" role="alert" tabIndex={-1}>{formError.message}</p>}
          <div class="form-group">
            <label for="mw-name">Name</label>
            <input id="mw-name" type="text" value={form.name} maxLength={NAME_MAX} onInput={e => set({ name: e.target.value })}
              required aria-required="true" aria-invalid={invalid('name')} aria-describedby={describedBy('name')} />
          </div>
          <div class="form-group">
            <label for="mw-description">Description <span class="form-optional">optional</span></label>
            <textarea id="mw-description" rows={2} maxLength={DESCRIPTION_MAX} value={form.description} onInput={e => set({ description: e.target.value })}
              aria-invalid={invalid('description')} aria-describedby={describedBy('description', form.public ? 'mw-description-hint' : '')} />
            {form.public && <p id="mw-description-hint" class="form-help">Shown on the public status page.</p>}
          </div>
          <fieldset class="mw-when">
            <legend>When</legend>
            <div class="mw-time-row">
              <div class="form-group">
                <label for="mw-start">Start</label>
                <input id="mw-start" type="datetime-local" value={form.start_at} onInput={e => set({ start_at: e.target.value })}
                  required aria-required="true" aria-invalid={invalid('start')} aria-describedby={describedBy('start', 'mw-tz-hint')} />
              </div>
              <div class="form-group">
                <label for="mw-end">End</label>
                <input id="mw-end" type="datetime-local" value={form.end_at} min={form.start_at || undefined} onInput={e => set({ end_at: e.target.value })}
                  required aria-required="true" aria-invalid={invalid('end')} aria-describedby={describedBy('end', 'mw-tz-hint')} />
              </div>
            </div>
            <div class="form-group mw-tz">
              <label for="mw-tz">Time zone</label>
              <select id="mw-tz" value={form.timezone} onChange={e => set({ timezone: e.target.value })}
                aria-invalid={invalid('tz')} aria-describedby={describedBy('tz', 'mw-tz-hint')}>
                {zoneGroups && (
                  <optgroup label="Suggested">
                    {zoneGroups.suggested.map(z => <option key={`s-${z.value}`} value={z.value}>{z.label}</option>)}
                  </optgroup>
                )}
                {zoneGroups && zoneGroups.groups.map(g => (
                  <optgroup key={g.region} label={g.region}>
                    {g.zones.map(z => <option key={z.value} value={z.value}>{z.label}</option>)}
                  </optgroup>
                ))}
              </select>
              <p id="mw-tz-hint" class="form-help mw-tz-hint">
                Start and end are local times in {(form.timezone || 'UTC').replace(/_/g, ' ')}. Repeating windows keep the same local time when daylight saving starts or ends.
                {' '}{inBrowserZone(form, viewerZone)}
              </p>
            </div>
          </fieldset>
          <div class="form-group">
            <label for="mw-monitor">Monitor</label>
            <select id="mw-monitor" value={form.monitor_id} onChange={e => set({ monitor_id: e.target.value })}
              aria-invalid={invalid('monitor')} aria-describedby={describedBy('monitor', monitorsError ? 'mw-monitor-hint' : '')}>
              <option value="">All monitors</option>
              {monitorMissing && <option value={form.monitor_id}>{monitorsError ? 'Current monitor' : 'Deleted monitor'}</option>}
              {monitors.map(m => <option key={m.id} value={m.id}>{m.name || m.id}</option>)}
            </select>
            {monitorsError && (
              <p id="mw-monitor-hint" class="form-help">
                Could not load your monitors. <button type="button" class="btn btn-sm btn-outline" onClick={loadMonitors}>Retry</button>
              </p>
            )}
          </div>
          <div class="form-group">
            <label for="mw-recurrence">Repeat</label>
            <select id="mw-recurrence" value={form.recurrence_type} onChange={e => set({ recurrence_type: e.target.value })}>
              <option value="none">Does not repeat</option>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
            </select>
          </div>
          {form.recurrence_type === 'weekly' && (
            <fieldset class="form-group mw-days" aria-describedby={describedBy('days')}>
              <legend>Days of the week</legend>
              <div class="mw-days-list">
                {DAY_NAMES.map((day, i) => (
                  <label key={i} class="mw-day">
                    <input type="checkbox" checked={(form.days_of_week || []).includes(i)}
                      aria-invalid={invalid('days')}
                      onChange={e => {
                        const days = (form.days_of_week || []).filter(d => d !== i);
                        if (e.target.checked) days.push(i);
                        set({ days_of_week: days });
                      }} />
                    {day}
                  </label>
                ))}
              </div>
            </fieldset>
          )}
          {form.recurrence_type === 'monthly' && (
            <div class="form-group">
              <label for="mw-dom">Day of the month</label>
              <input id="mw-dom" type="number" inputMode="numeric" min="1" max="31" value={form.day_of_month}
                onInput={e => set({ day_of_month: parseInt(e.target.value, 10) || '' })}
                aria-invalid={invalid('dom')} aria-describedby={describedBy('dom', 'mw-dom-hint')} />
              <p id="mw-dom-hint" class="form-help">In months without this day, the window is skipped.</p>
            </div>
          )}
          {form.recurrence_type !== 'none' && (
            <div class="form-group">
              <label for="mw-recur-end">Stop repeating after <span class="form-optional">optional</span></label>
              <input id="mw-recur-end" type="datetime-local" value={form.recurrence_end_at} min={form.start_at || undefined}
                onInput={e => set({ recurrence_end_at: e.target.value })}
                aria-invalid={invalid('recur-end')} aria-describedby={describedBy('recur-end', 'mw-recur-end-hint')} />
              <p id="mw-recur-end-hint" class="form-help">Leave empty to repeat until you delete the window.</p>
            </div>
          )}
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.suppress_alerts} onChange={e => set({ suppress_alerts: e.target.checked })}
                aria-describedby="mw-suppress-hint" />
              Silence alerts during this window
            </label>
            <p id="mw-suppress-hint" class="form-help">
              {form.suppress_alerts
                ? 'Alerts are held until the window ends, then paged only if the monitor is still down.'
                : 'Alerts page as usual during this window.'}
            </p>
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.public} onChange={e => set({ public: e.target.checked })} />
              Show on the public status page
            </label>
          </div>
          <div class="mw-form-actions">
            <button type="button" class="btn btn-outline" onClick={closeModal} disabled={saving}>Cancel</button>
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : (editId ? 'Save changes' : 'Schedule')}
            </button>
          </div>
        </form>
      </Modal>

      <Modal open={!!pendingDelete} onClose={closeDelete} title="Delete maintenance window?">
        {pendingDelete && (
          <div>
            {deleteError && <p class="form-error" role="alert">{deleteError}</p>}
            <p class="mw-confirm-text">
              <strong>{pendingDelete.name}</strong> will be removed.
              {pendingDelete.suppress_alerts !== false && windowStatus(pendingDelete) === 'active'
                ? ' It is in progress, so alerts it is holding are paged right away for monitors that are still down. Monitors that recovered page nobody.'
                : ''}
            </p>
            <div class="mw-form-actions">
              <button type="button" class="btn btn-outline" onClick={closeDelete} disabled={deleting}>Cancel</button>
              <button type="button" class="btn btn-danger" onClick={confirmDelete} disabled={deleting}>
                {deleting ? 'Deleting...' : 'Delete window'}
              </button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
