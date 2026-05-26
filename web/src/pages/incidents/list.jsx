import { useState, useEffect } from 'preact/hooks';
import { get, post } from '../../api/client';
import { currentUser } from '../../state/auth';
import {
  PageHeader, Tabs, SearchInput, SeverityIcon, LoadingPage, ErrorMessage, EmptyState, Modal, relativeTime,
} from '../../components/ui';

const SEVERITY_MAP = {
  critical: 'critical',
  major: 'warning',
  minor: 'info',
};

const STATUS_CONFIG = {
  investigating: { bg: '#e67e22', label: 'INVESTIGATING' },
  identified:    { bg: '#2980b9', label: 'IDENTIFIED' },
  monitoring:    { bg: '#2980b9', label: 'MONITORING' },
  resolved:      { bg: '#27ae60', label: 'RESOLVED' },
  maintenance:   { bg: '#2980b9', label: 'MAINTENANCE' },
};

function IncidentStatusPill({ status }) {
  const c = STATUS_CONFIG[status] || { bg: 'var(--color-muted)', label: status?.toUpperCase() || 'UNKNOWN' };
  return (
    <span style={{
      display: 'inline-block',
      padding: '3px 10px',
      borderRadius: 9999,
      background: c.bg,
      color: 'white',
      fontSize: '0.6875rem',
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.5px',
    }}>{c.label}</span>
  );
}

const DEFAULT_FORM = {
  title: '',
  severity: 'minor',
  status: 'investigating',
  message: '',
  monitor_ids: [],
  status_page_ids: [],
};

export function IncidentListPage() {
  const [incidents, setIncidents] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [tab, setTab] = useState('active');
  const [search, setSearch] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ ...DEFAULT_FORM });
  const [saving, setSaving] = useState(false);
  const [monitors, setMonitors] = useState([]);
  const [statusPages, setStatusPages] = useState([]);

  const role = currentUser.value?.role;
  const canCreate = role === 'owner' || role === 'admin' || role === 'member';

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get(`/incidents?status=${tab}`);
      setIncidents(data.incidents || data || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  async function loadDeps() {
    try {
      const [mon, sp] = await Promise.all([
        get('/monitors').catch(() => []),
        get('/status-pages').catch(() => []),
      ]);
      setMonitors(mon.monitors || mon || []);
      setStatusPages(sp.status_pages || sp || []);
    } catch (_) {}
  }

  useEffect(() => { load(); }, [tab]);

  function openCreate() {
    setForm({ ...DEFAULT_FORM });
    loadDeps();
    setShowModal(true);
  }

  function closeModal() {
    setShowModal(false);
  }

  function toggleMultiSelect(field, id) {
    setForm(f => {
      const arr = f[field];
      return {
        ...f,
        [field]: arr.includes(id) ? arr.filter(x => x !== id) : [...arr, id],
      };
    });
  }

  async function save() {
    if (!form.title.trim()) return;
    setSaving(true);
    try {
      await post('/incidents', {
        title: form.title,
        severity: form.severity,
        status: form.status,
        message: form.message || undefined,
        monitor_ids: form.monitor_ids.length ? form.monitor_ids : undefined,
        status_page_ids: form.status_page_ids.length ? form.status_page_ids : undefined,
      });
      closeModal();
      load();
    } catch (err) {
      alert(err.message || 'Failed to create incident');
    } finally {
      setSaving(false);
    }
  }

  const tabs = [
    { key: 'active', label: 'Active' },
    { key: 'resolved', label: 'Resolved' },
  ];

  const filtered = incidents.filter(inc => {
    if (!search) return true;
    return (inc.title || '').toLowerCase().includes(search.toLowerCase());
  });

  return (
    <div class="incidents-page">
      <PageHeader
        title="Incidents"
        subtitle={`${incidents.length} ${tab} incidents`}
        actions={canCreate && (
          <button class="btn btn-primary" onClick={openCreate}>New Incident</button>
        )}
      />

      <Tabs tabs={tabs} active={tab} onChange={setTab} />

      <div class="filters-bar">
        <SearchInput value={search} onInput={setSearch} placeholder="Search incidents..." />
      </div>

      {loading ? <LoadingPage /> : error ? <ErrorMessage error={error} onRetry={load} /> : (
        <div class="alert-list">
          {filtered.length === 0 ? (
            <EmptyState
              title={`No ${tab} incidents`}
              description={tab === 'active' ? 'All clear!' : 'No resolved incidents found'}
            />
          ) : (
            filtered.map(inc => (
              <a key={inc.id} href={`/incidents/${inc.id}`} class="alert-card">
                <div class="alert-card-left">
                  <SeverityIcon severity={SEVERITY_MAP[inc.severity] || 'info'} />
                  <div class="alert-card-info">
                    <h4>{inc.title}</h4>
                    {inc.monitor_ids && inc.monitor_ids.length > 0 && (
                      <p class="alert-card-detail">
                        {inc.monitor_ids.length} affected monitor{inc.monitor_ids.length !== 1 ? 's' : ''}
                      </p>
                    )}
                  </div>
                </div>
                <div class="alert-card-right">
                  <IncidentStatusPill status={inc.status} />
                  <span class="alert-card-time">{relativeTime(inc.created_at)}</span>
                </div>
              </a>
            ))
          )}
        </div>
      )}

      <Modal open={showModal} onClose={closeModal} title="New Incident">
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Title</label>
            <input
              type="text"
              value={form.title}
              onInput={e => setForm({ ...form, title: e.target.value })}
              required
              placeholder="Brief description of the incident"
            />
          </div>
          <div class="form-group">
            <label>Severity</label>
            <select value={form.severity} onChange={e => setForm({ ...form, severity: e.target.value })}>
              <option value="critical">Critical</option>
              <option value="major">Major</option>
              <option value="minor">Minor</option>
            </select>
          </div>
          <div class="form-group">
            <label>Initial Status</label>
            <select value={form.status} onChange={e => setForm({ ...form, status: e.target.value })}>
              <option value="investigating">Investigating</option>
              <option value="identified">Identified</option>
              <option value="monitoring">Monitoring</option>
              <option value="maintenance">Maintenance</option>
            </select>
          </div>
          <div class="form-group">
            <label>Initial Message</label>
            <textarea
              rows="3"
              value={form.message}
              onInput={e => setForm({ ...form, message: e.target.value })}
              placeholder="What's happening? (optional)"
            />
          </div>
          {monitors.length > 0 && (
            <div class="form-group">
              <label>Affected Monitors</label>
              <div style="display: flex; flex-direction: column; gap: 4px; max-height: 140px; overflow-y: auto; border: 1px solid var(--color-border); border-radius: 6px; padding: 8px;">
                {monitors.map(m => (
                  <label key={m.id} style="display: flex; align-items: center; gap: 8px; cursor: pointer; font-weight: normal;">
                    <input
                      type="checkbox"
                      checked={form.monitor_ids.includes(m.id)}
                      onChange={() => toggleMultiSelect('monitor_ids', m.id)}
                    />
                    {m.name}
                  </label>
                ))}
              </div>
            </div>
          )}
          {statusPages.length > 0 && (
            <div class="form-group">
              <label>Affected Status Pages</label>
              <div style="display: flex; flex-direction: column; gap: 4px; max-height: 140px; overflow-y: auto; border: 1px solid var(--color-border); border-radius: 6px; padding: 8px;">
                {statusPages.map(sp => (
                  <label key={sp.id} style="display: flex; align-items: center; gap: 8px; cursor: pointer; font-weight: normal;">
                    <input
                      type="checkbox"
                      checked={form.status_page_ids.includes(sp.id)}
                      onChange={() => toggleMultiSelect('status_page_ids', sp.id)}
                    />
                    {sp.name || sp.slug}
                  </label>
                ))}
              </div>
            </div>
          )}
          <div style="display: flex; gap: 8px;">
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Creating...' : 'Create Incident'}
            </button>
            <button type="button" class="btn" onClick={closeModal} disabled={saving}>
              Cancel
            </button>
          </div>
        </form>
      </Modal>
    </div>
  );
}
