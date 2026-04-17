import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { PageHeader, Card, LoadingPage, ErrorMessage, Modal, formatTime, relativeTime } from '../../components/ui';

export function OnCallPage() {
  const [schedules, setSchedules] = useState([]);
  const [selected, setSelected] = useState(null);
  const [detail, setDetail] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [showOverride, setShowOverride] = useState(false);
  const [overrideUser, setOverrideUser] = useState('');
  const [overrideUserSearch, setOverrideUserSearch] = useState('');
  const [overrideUsers, setOverrideUsers] = useState([]);
  const [overrideStart, setOverrideStart] = useState('');
  const [overrideEnd, setOverrideEnd] = useState('');
  const [overrideReason, setOverrideReason] = useState('');

  // Schedule create/edit modal state
  const [showScheduleModal, setShowScheduleModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [teams, setTeams] = useState([]);
  const [schedForm, setSchedForm] = useState({
    team_id: '',
    rotation_interval: 'daily',
    rotation_interval_hours: 1,
    handoff_time: '09:00',
    effective_from: '',
    timezone: 'UTC',
  });

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get('/schedules');
      setSchedules(data.schedules || data || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  async function loadDetail(id) {
    setSelected(id);
    try {
      const [s, oncall] = await Promise.all([
        get(`/schedules/${id}`),
        get(`/schedules/${id}/on-call`).catch(() => null),
      ]);
      setDetail({ ...s, on_call: oncall });
    } catch (_) {}
  }

  async function loadTeams() {
    try {
      const data = await get('/teams');
      setTeams(data.teams || data || []);
    } catch (_) {}
  }

  function openScheduleModal(schedule = null) {
    if (schedule) {
      setEditId(schedule.id);
      setSchedForm({
        team_id: schedule.team_id || '',
        rotation_interval: schedule.rotation_interval || 'daily',
        rotation_interval_hours: schedule.rotation_interval_hours || 1,
        handoff_time: schedule.handoff_time || '09:00',
        effective_from: schedule.effective_from ? schedule.effective_from.slice(0, 16) : '',
        timezone: schedule.timezone || 'UTC',
      });
    } else {
      setEditId(null);
      setSchedForm({
        team_id: '',
        rotation_interval: 'daily',
        rotation_interval_hours: 1,
        handoff_time: '09:00',
        effective_from: '',
        timezone: 'UTC',
      });
    }
    loadTeams();
    setShowScheduleModal(true);
  }

  async function saveSchedule() {
    try {
      const body = {
        team_id: schedForm.team_id,
        rotation_interval: schedForm.rotation_interval,
        handoff_time: schedForm.handoff_time,
        effective_from: new Date(schedForm.effective_from).toISOString(),
        timezone: schedForm.timezone,
      };
      if (schedForm.rotation_interval === 'custom_hours') {
        body.rotation_interval_hours = Number(schedForm.rotation_interval_hours);
      }
      if (editId) {
        await patch(`/schedules/${editId}`, body);
      } else {
        await post('/schedules', body);
      }
      setShowScheduleModal(false);
      load();
      if (editId) loadDetail(editId);
    } catch (_) {}
  }

  async function deleteSchedule(id) {
    if (!confirm('Delete this schedule?')) return;
    try {
      await del(`/schedules/${id}`);
      setDetail(null);
      setSelected(null);
      load();
    } catch (_) {}
  }

  async function createOverride(schedId) {
    try {
      await post(`/schedules/${schedId}/overrides`, {
        user_id: overrideUser,
        start_time: new Date(overrideStart).toISOString(),
        end_time: new Date(overrideEnd).toISOString(),
        reason: overrideReason,
      });
      setShowOverride(false);
      setOverrideUser('');
      setOverrideUserSearch('');
      setOverrideStart('');
      setOverrideEnd('');
      setOverrideReason('');
      loadDetail(schedId);
    } catch (_) {}
  }

  async function deleteOverride(schedId, overrideId) {
    try {
      await del(`/schedules/${schedId}/overrides/${overrideId}`);
      loadDetail(schedId);
    } catch (_) {}
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  return (
    <div class="oncall-page">
      <PageHeader title="On-Call" subtitle="Rotation schedules"
        actions={<button class="btn btn-primary" onClick={() => openScheduleModal()}>New Schedule</button>}
      />

      <div class="oncall-grid">
        {/* Current on-call summary */}
        <Card>
          <h3>Currently On Call</h3>
          <div class="oncall-current-list">
            {schedules.map(s => (
              <div key={s.id} class="oncall-current-item" onClick={() => loadDetail(s.id)}>
                <div class="oncall-current-name">{s.name}</div>
                <div class="oncall-current-user">{s.current_on_call || '-'}</div>
              </div>
            ))}
            {schedules.length === 0 && <p class="text-muted">No schedules configured</p>}
          </div>
        </Card>

        {/* Schedule detail */}
        {detail && (
          <Card>
            <div class="schedule-detail-header">
              <h3>{detail.name}</h3>
              <div class="schedule-detail-actions">
                <button class="btn btn-sm" onClick={() => openScheduleModal(detail)}>Edit</button>
                <button class="btn btn-sm btn-danger" onClick={() => deleteSchedule(detail.id)}>Delete</button>
                <button class="btn btn-sm" onClick={async () => {
                  setShowOverride(true);
                  try { const u = await get('/users'); setOverrideUsers(u.users || u || []); } catch { setOverrideUsers([]); }
                }}>
                  Add Override
                </button>
              </div>
            </div>

            {detail.on_call && (
              <div class="oncall-current-detail">
                <p><strong>On call now:</strong> {detail.on_call.user_email || detail.on_call.user_id || '-'}</p>
                {detail.on_call.until && <p class="text-muted">Until {formatTime(detail.on_call.until)}</p>}
              </div>
            )}

            <div class="schedule-rotation">
              <h4>Rotation</h4>
              <p>Type: {detail.rotation_interval || detail.rotation_type || '-'}</p>
              {detail.participants && (
                <div class="rotation-participants">
                  {(detail.participants || []).map((p, i) => (
                    <div key={i} class="rotation-participant">
                      {p.user_email || p.user_id || `Participant ${i + 1}`}
                    </div>
                  ))}
                </div>
              )}
            </div>

            {detail.overrides && detail.overrides.length > 0 && (
              <div class="schedule-overrides">
                <h4>Active Overrides</h4>
                {detail.overrides.map(o => (
                  <div key={o.id} class="override-item">
                    <span>{o.user_email || o.user_id}</span>
                    <span>{formatTime(o.start_time)} - {formatTime(o.end_time)}</span>
                    <button class="btn btn-sm btn-danger" onClick={() => deleteOverride(detail.id, o.id)}>
                      Remove
                    </button>
                  </div>
                ))}
              </div>
            )}

            {/* Simple calendar view */}
            <div class="schedule-calendar">
              <h4>This Week</h4>
              <div class="calendar-week">
                {getWeekDays().map((day, i) => (
                  <div key={i} class={`calendar-day ${isToday(day) ? 'today' : ''}`}>
                    <span class="calendar-day-label">{day.toLocaleDateString('en', { weekday: 'short' })}</span>
                    <span class="calendar-day-num">{day.getDate()}</span>
                  </div>
                ))}
              </div>
            </div>
          </Card>
        )}
      </div>

      <Modal open={showOverride} onClose={() => setShowOverride(false)} title="Add Override">
        <form onSubmit={e => { e.preventDefault(); if (selected) createOverride(selected); }}>
          <div class="form-group" style="position: relative;">
            <label>User</label>
            <input type="text"
              value={overrideUserSearch}
              onInput={e => { setOverrideUserSearch(e.target.value); setOverrideUser(''); }}
              placeholder="Type name or email to search..."
              autocomplete="off"
              required={!overrideUser} />
            {overrideUser && <div style="color: var(--color-up); font-size: 0.8125rem; margin-top: 2px;">Selected: {overrideUserSearch}</div>}
            {overrideUserSearch && !overrideUser && (() => {
              const q = overrideUserSearch.toLowerCase();
              const matches = overrideUsers.filter(u =>
                (u.email || '').toLowerCase().includes(q) ||
                (u.name || '').toLowerCase().includes(q)
              ).slice(0, 8);
              if (matches.length === 0) return <div style="color: var(--color-text-muted); font-size: 0.8125rem; margin-top: 2px;">No users found</div>;
              return (
                <div style="position: absolute; left: 0; right: 0; top: 100%; z-index: 10; background: var(--color-surface); border: 1px solid var(--color-border); border-radius: var(--radius-sm); max-height: 200px; overflow-y: auto; box-shadow: var(--shadow-lg);">
                  {matches.map(u => (
                    <div key={u.id}
                      style="padding: 8px 12px; cursor: pointer; font-size: 0.875rem; border-bottom: 1px solid var(--color-border);"
                      onMouseOver={e => e.currentTarget.style.background = 'var(--color-muted-bg)'}
                      onMouseOut={e => e.currentTarget.style.background = 'transparent'}
                      onClick={() => { setOverrideUser(u.id); setOverrideUserSearch(u.name || u.email); }}>
                      <div style="font-weight: 500;">{u.name || u.email}</div>
                      {u.name && <div style="font-size: 0.75rem; color: var(--color-text-muted);">{u.email}</div>}
                    </div>
                  ))}
                </div>
              );
            })()}
          </div>
          <div class="form-group">
            <label>Start</label>
            <input type="datetime-local" value={overrideStart} onInput={e => setOverrideStart(e.target.value)} required />
          </div>
          <div class="form-group">
            <label>End</label>
            <input type="datetime-local" value={overrideEnd} onInput={e => setOverrideEnd(e.target.value)} required />
          </div>
          <div class="form-group">
            <label>Reason</label>
            <textarea value={overrideReason} onInput={e => setOverrideReason(e.target.value)} placeholder="Why is this override needed?" rows={3} />
          </div>
          <button type="submit" class="btn btn-primary">Create Override</button>
        </form>
      </Modal>

      <Modal open={showScheduleModal} onClose={() => setShowScheduleModal(false)} title={editId ? 'Edit Schedule' : 'New Schedule'}>
        <form onSubmit={e => { e.preventDefault(); saveSchedule(); }}>
          <div class="form-group">
            <label>Team</label>
            <select value={schedForm.team_id} onInput={e => setSchedForm({ ...schedForm, team_id: e.target.value })} required>
              <option value="">Select a team</option>
              {teams.map(t => <option key={t.id} value={t.id}>{t.name || t.id}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Rotation Interval</label>
            <select value={schedForm.rotation_interval} onInput={e => setSchedForm({ ...schedForm, rotation_interval: e.target.value })}>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
              <option value="custom_hours">Custom Hours</option>
            </select>
          </div>
          {schedForm.rotation_interval === 'custom_hours' && (
            <div class="form-group">
              <label>Rotation Interval Hours</label>
              <input type="number" min="1" value={schedForm.rotation_interval_hours}
                onInput={e => setSchedForm({ ...schedForm, rotation_interval_hours: e.target.value })} required />
            </div>
          )}
          <div class="form-group">
            <label>Handoff Time</label>
            <input type="time" value={schedForm.handoff_time}
              onInput={e => setSchedForm({ ...schedForm, handoff_time: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Effective From</label>
            <input type="datetime-local" value={schedForm.effective_from}
              onInput={e => setSchedForm({ ...schedForm, effective_from: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Timezone</label>
            <input type="text" value={schedForm.timezone} placeholder="e.g. America/New_York"
              onInput={e => setSchedForm({ ...schedForm, timezone: e.target.value })} required />
          </div>
          <button type="submit" class="btn btn-primary">{editId ? 'Update Schedule' : 'Create Schedule'}</button>
        </form>
      </Modal>
    </div>
  );
}

function getWeekDays() {
  const now = new Date();
  const day = now.getDay();
  const monday = new Date(now);
  monday.setDate(now.getDate() - ((day + 6) % 7));
  const days = [];
  for (let i = 0; i < 7; i++) {
    const d = new Date(monday);
    d.setDate(monday.getDate() + i);
    days.push(d);
  }
  return days;
}

function isToday(d) {
  const now = new Date();
  return d.getDate() === now.getDate() && d.getMonth() === now.getMonth() && d.getFullYear() === now.getFullYear();
}
