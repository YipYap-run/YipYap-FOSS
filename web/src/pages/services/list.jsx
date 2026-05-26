import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { PageHeader, SearchInput, LoadingPage, ErrorMessage, EmptyState, Modal } from '../../components/ui';
import { safeHref } from '../../utils/url';

const DEFAULT_FORM = {
  name: '',
  slug: '',
  description: '',
  owner_team_id: '',
  runbook_url: '',
  notes: '',
};

export function ServiceListPage() {
  const [services, setServices] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [search, setSearch] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ ...DEFAULT_FORM });
  const [saving, setSaving] = useState(false);
  const [teams, setTeams] = useState([]);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get('/services');
      setServices(data.services || data || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  async function loadTeams() {
    try {
      const data = await get('/teams');
      setTeams(data.teams || data || []);
    } catch (_) {}
  }

  useEffect(() => { load(); }, []);

  // Auto-open edit modal when navigated with ?edit=<id>
  useEffect(() => {
    if (!loading && services.length > 0) {
      const params = new URLSearchParams(window.location.search);
      const editParam = params.get('edit');
      if (editParam) {
        const s = services.find(svc => svc.id === editParam);
        if (s) {
          setEditId(s.id);
          setForm({
            name: s.name || '',
            slug: s.slug || '',
            description: s.description || '',
            owner_team_id: s.owner_team_id || '',
            runbook_url: s.runbook_url || '',
            notes: s.notes || '',
          });
          loadTeams();
          setShowModal(true);
        }
      }
    }
  }, [loading, services]);

  function openCreate() {
    setEditId(null);
    setForm({ ...DEFAULT_FORM });
    loadTeams();
    setShowModal(true);
  }

  function openEdit(e, service) {
    e.preventDefault();
    e.stopPropagation();
    setEditId(service.id);
    setForm({
      name: service.name || '',
      slug: service.slug || '',
      description: service.description || '',
      owner_team_id: service.owner_team_id || '',
      runbook_url: service.runbook_url || '',
      notes: service.notes || '',
    });
    loadTeams();
    setShowModal(true);
  }

  function closeModal() {
    setShowModal(false);
    setEditId(null);
    // Clear ?edit= param so the useEffect doesn't re-open the modal after reload.
    if (window.location.search.includes('edit=')) {
      history.replaceState(null, '', window.location.pathname);
    }
  }

  async function save() {
    setSaving(true);
    try {
      const body = {
        name: form.name,
        description: form.description,
        owner_team_id: form.owner_team_id || undefined,
        runbook_url: form.runbook_url,
        notes: form.notes,
      };
      if (form.slug) body.slug = form.slug;

      if (editId) await patch(`/services/${editId}`, body);
      else await post('/services', body);

      closeModal();
      load();
    } catch (err) {
      alert(err.message || 'Failed to save service');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!editId) return;
    if (!confirm('Are you sure you want to delete this service?')) return;
    setSaving(true);
    try {
      await del(`/services/${editId}`);
      closeModal();
      load();
    } catch (err) {
      alert(err.message || 'Failed to delete service');
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  const filtered = services.filter(s => {
    if (search && !s.name.toLowerCase().includes(search.toLowerCase())) return false;
    return true;
  });

  // Build a lookup for team names
  const teamMap = {};
  teams.forEach(t => { teamMap[t.id] = t.name; });

  return (
    <div class="services-page">
      <PageHeader title="Services" subtitle={`${services.length} services`}
        actions={<button class="btn btn-primary" onClick={openCreate}>New Service</button>} />

      <div class="filters-bar">
        <SearchInput value={search} onInput={setSearch} placeholder="Search services..." />
      </div>

      {filtered.length === 0 ? (
        <EmptyState title="No services found"
          description={search ? 'Try adjusting your search' : 'Create your first service to get started'} />
      ) : (
        <div class="monitor-list">
          {filtered.map(s => (
            <a key={s.id} href={`/services/${s.id}`} class="monitor-card">
              <span class="monitor-card-type">{s.slug || ''}</span>
              <h3 class="monitor-card-name">{s.name}</h3>
              {s.description && (
                <p class="monitor-card-subtitle">
                  {s.description.length > 80 ? s.description.slice(0, 80) + '...' : s.description}
                </p>
              )}
              <div class="monitor-card-meta" style="margin-top: 8px">
                {s.owner_team_name && <span class="label-tag">Team: {s.owner_team_name}</span>}
                {s.runbook_url && (
                  safeHref(s.runbook_url)
                    ? <a href={safeHref(s.runbook_url)} target="_blank" rel="noopener noreferrer"
                         class="label-tag" style="color: var(--color-primary); border-color: var(--color-primary)"
                         onClick={e => e.stopPropagation()}>Runbook</a>
                    : <span class="label-tag">Runbook</span>
                )}
              </div>
            </a>
          ))}
        </div>
      )}

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit Service' : 'New Service'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Name</label>
            <input type="text" value={form.name}
                   onInput={e => setForm({ ...form, name: e.target.value })}
                   required placeholder="My Service" />
          </div>
          <div class="form-group">
            <label>Slug</label>
            <input type="text" value={form.slug}
                   onInput={e => setForm({ ...form, slug: e.target.value })}
                   placeholder="Auto-generated if empty" />
          </div>
          <div class="form-group">
            <label>Description</label>
            <textarea rows="3" value={form.description}
                      onInput={e => setForm({ ...form, description: e.target.value })}
                      placeholder="What does this service do?" />
          </div>
          <div class="form-group">
            <label>Owner Team</label>
            <select value={form.owner_team_id}
                    onChange={e => setForm({ ...form, owner_team_id: e.target.value })}>
              <option value="">None</option>
              {teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Runbook URL</label>
            <input type="url" value={form.runbook_url}
                   onInput={e => setForm({ ...form, runbook_url: e.target.value })}
                   placeholder="https://wiki.example.com/runbooks/..." />
          </div>
          <div class="form-group">
            <label>Notes</label>
            <textarea rows="3" value={form.notes}
                      onInput={e => setForm({ ...form, notes: e.target.value })}
                      placeholder="Internal notes about this service" />
          </div>
          <div style="display: flex; gap: 8px; align-items: center;">
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : 'Save'}
            </button>
            {editId && (
              <button type="button" class="btn btn-danger" disabled={saving} onClick={handleDelete}>
                Delete
              </button>
            )}
          </div>
        </form>
      </Modal>
    </div>
  );
}
