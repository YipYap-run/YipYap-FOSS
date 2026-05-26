import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Card, Modal, Spinner, useToast, ToastContainer } from '../../components/ui';

const HEALTH_CLASSES = [
  { value: 'healthy', label: 'Healthy' },
  { value: 'degraded', label: 'Degraded' },
  { value: 'unhealthy', label: 'Unhealthy' },
];

const SEVERITIES = [
  { value: 'critical', label: 'Critical' },
  { value: 'warning', label: 'Warning' },
  { value: 'info', label: 'Info' },
];

const DEFAULT_STATE = {
  name: '',
  color: '#6366f1',
  health_class: 'healthy',
  severity: 'info',
  position: 0,
};

export function MonitorStatesTab() {
  const [states, setStates] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ ...DEFAULT_STATE });
  const [saving, setSaving] = useState(false);
  const { toasts, toast } = useToast();

  async function load() {
    setLoading(true);
    try {
      const data = await get('/monitor-states');
      setStates(data.states || data || []);
    } catch (err) {
      toast(err.message || 'Failed to load states', 'error');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  function openCreate() {
    setEditId(null);
    setForm({ ...DEFAULT_STATE, position: states.length });
    setShowModal(true);
  }

  function openEdit(state) {
    setEditId(state.id);
    setForm({
      name: state.name,
      color: state.color,
      health_class: state.health_class,
      severity: state.severity,
      position: state.position,
    });
    setShowModal(true);
  }

  function closeModal() {
    setShowModal(false);
    setEditId(null);
  }

  async function save() {
    setSaving(true);
    try {
      const body = {
        name: form.name,
        color: form.color,
        health_class: form.health_class,
        severity: form.severity,
        position: +form.position || 0,
      };
      if (editId) {
        await patch(`/monitor-states/${editId}`, body);
        toast('State updated');
      } else {
        await post('/monitor-states', body);
        toast('State created');
      }
      closeModal();
      load();
    } catch (err) {
      toast(err.message || 'Failed to save state', 'error');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete(id) {
    if (!confirm('Delete this custom state? Any match rules using it will break.')) return;
    try {
      await del(`/monitor-states/${id}`);
      toast('State deleted');
      load();
    } catch (err) {
      toast(err.message || 'Failed to delete state', 'error');
    }
  }

  if (loading) return <Spinner />;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div style="display: flex; justify-content: space-between; align-items: center; margin-bottom: 16px;">
        <div>
          <p style="font-size: 0.875rem; color: var(--color-text-muted);">
            Custom monitor states extend the built-in Up/Degraded/Down with your own labels, colors, and health classifications.
          </p>
        </div>
        <button class="btn btn-primary btn-sm" onClick={openCreate}>Add State</button>
      </div>

      <div style="display: grid; gap: 8px;">
        {states.map(state => (
          <Card key={state.id}>
            <div style="display: flex; align-items: center; gap: 12px; padding: 12px;">
              <span style={{
                display: 'inline-block',
                width: 16,
                height: 16,
                borderRadius: '50%',
                background: state.color,
                flexShrink: 0,
                border: '2px solid var(--color-border)',
              }} />
              <div style="flex: 1; min-width: 0;">
                <div style="display: flex; align-items: center; gap: 8px;">
                  <strong>{state.name}</strong>
                  {state.is_builtin && (
                    <span style="font-size: 0.6875rem; color: var(--color-text-muted); background: var(--color-surface-raised); padding: 1px 6px; border-radius: 4px;">
                      built-in
                    </span>
                  )}
                </div>
                <div style="font-size: 0.8125rem; color: var(--color-text-muted);">
                  {state.health_class} / {state.severity} / position {state.position}
                </div>
              </div>
              {!state.is_builtin && (
                <div style="display: flex; gap: 4px;">
                  <button class="btn btn-xs" onClick={() => openEdit(state)}>Edit</button>
                  <button class="btn btn-xs btn-danger" onClick={() => handleDelete(state.id)}>Delete</button>
                </div>
              )}
              {state.is_builtin && (
                <button class="btn btn-xs" onClick={() => openEdit(state)}>Edit</button>
              )}
            </div>
          </Card>
        ))}
        {states.length === 0 && (
          <p style="text-align: center; color: var(--color-text-muted); padding: 24px;">
            No custom states yet. The built-in states (Up, Degraded, Down) are always available.
          </p>
        )}
      </div>

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit State' : 'New State'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label class="required">Name</label>
            <input type="text" value={form.name} required
              onInput={e => setForm({ ...form, name: e.target.value })}
              placeholder="e.g. Maintenance" />
          </div>
          <div class="form-group">
            <label>Color</label>
            <div style="display: flex; gap: 8px; align-items: center;">
              <input type="color" value={form.color}
                onInput={e => setForm({ ...form, color: e.target.value })}
                style="width: 48px; height: 32px; padding: 2px; cursor: pointer;" />
              <input type="text" value={form.color}
                onInput={e => setForm({ ...form, color: e.target.value })}
                placeholder="#6366f1" style="flex: 1;" />
            </div>
          </div>
          <div class="form-group">
            <label>Health Class</label>
            <select value={form.health_class} onChange={e => setForm({ ...form, health_class: e.target.value })}>
              {HEALTH_CLASSES.map(h => <option key={h.value} value={h.value}>{h.label}</option>)}
            </select>
            <p class="form-help">Determines how this state maps to up/degraded/down for alerting logic.</p>
          </div>
          <div class="form-group">
            <label>Severity</label>
            <select value={form.severity} onChange={e => setForm({ ...form, severity: e.target.value })}>
              {SEVERITIES.map(s => <option key={s.value} value={s.value}>{s.label}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Position</label>
            <input type="number" value={form.position} min="0"
              onInput={e => setForm({ ...form, position: e.target.value })} />
            <p class="form-help">Display order (lower numbers appear first).</p>
          </div>
          <div style="display: flex; gap: 8px;">
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : 'Save'}
            </button>
            <button type="button" class="btn" onClick={closeModal}>Cancel</button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
