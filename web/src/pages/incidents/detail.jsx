import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { currentUser } from '../../state/auth';
import { PageHeader, Card, SeverityIcon, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';

const SEVERITY_MAP = {
  critical: 'critical',
  major: 'warning',
  minor: 'info',
};

const STATUS_COLORS = {
  investigating: '#e67e22',
  identified:    '#2980b9',
  monitoring:    '#2980b9',
  resolved:      '#27ae60',
  maintenance:   '#2980b9',
};

function IncidentStatusPill({ status }) {
  const bg = STATUS_COLORS[status] || 'var(--color-muted)';
  return (
    <span style={{
      display: 'inline-block',
      padding: '3px 10px',
      borderRadius: 9999,
      background: bg,
      color: 'white',
      fontSize: '0.6875rem',
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.5px',
    }}>{status?.toUpperCase() || 'UNKNOWN'}</span>
  );
}

export function IncidentDetailPage({ id }) {
  const [incident, setIncident] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  // Edit incident fields
  const [editingMeta, setEditingMeta] = useState(false);
  const [metaForm, setMetaForm] = useState({});
  const [metaSaving, setMetaSaving] = useState(false);

  // New update form
  const [updateBody, setUpdateBody] = useState('');
  const [updatePublic, setUpdatePublic] = useState(true);
  const [updateStatus, setUpdateStatus] = useState('');
  const [submitting, setSubmitting] = useState(false);

  // Edit update
  const [editUpdateId, setEditUpdateId] = useState(null);
  const [editUpdateBody, setEditUpdateBody] = useState('');
  const [editUpdateSaving, setEditUpdateSaving] = useState(false);

  const role = currentUser.value?.role;
  const userEmail = currentUser.value?.email;
  const canEdit = role === 'member' || role === 'admin';
  const canDelete = role === 'admin';

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get(`/incidents/${id}`);
      setIncident(data);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [id]);

  function openMetaEdit() {
    setMetaForm({
      title: incident.title,
      severity: incident.severity,
      status: incident.status,
    });
    setEditingMeta(true);
  }

  async function saveMeta() {
    setMetaSaving(true);
    try {
      await patch(`/incidents/${id}`, metaForm);
      setEditingMeta(false);
      load();
    } catch (err) {
      alert(err.message || 'Failed to update incident');
    } finally {
      setMetaSaving(false);
    }
  }

  async function deleteIncident() {
    if (!confirm('Delete this incident? This cannot be undone.')) return;
    try {
      await del(`/incidents/${id}`);
      window.location.href = '/incidents';
    } catch (err) {
      alert(err.message || 'Failed to delete incident');
    }
  }

  async function submitUpdate(e) {
    e.preventDefault();
    if (!updateBody.trim()) return;
    setSubmitting(true);
    try {
      await post(`/incidents/${id}/updates`, {
        body: updateBody,
        public: updatePublic,
        status: updateStatus || undefined,
      });
      setUpdateBody('');
      setUpdateStatus('');
      setUpdatePublic(true);
      load();
    } catch (err) {
      alert(err.message || 'Failed to post update');
    } finally {
      setSubmitting(false);
    }
  }

  function startEditUpdate(update) {
    setEditUpdateId(update.id);
    setEditUpdateBody(update.body);
  }

  async function saveEditUpdate(updateId) {
    setEditUpdateSaving(true);
    try {
      await patch(`/incidents/${id}/updates/${updateId}`, { body: editUpdateBody });
      setEditUpdateId(null);
      load();
    } catch (err) {
      alert(err.message || 'Failed to save update');
    } finally {
      setEditUpdateSaving(false);
    }
  }

  async function deleteUpdate(updateId) {
    if (!confirm('Delete this update?')) return;
    try {
      await del(`/incidents/${id}/updates/${updateId}`);
      load();
    } catch (err) {
      alert(err.message || 'Failed to delete update');
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;
  if (!incident) return <ErrorMessage error="Incident not found" />;

  const updates = incident.updates || [];

  return (
    <div class="incident-detail">
      <PageHeader
        title={editingMeta ? 'Edit Incident' : incident.title}
        subtitle={
          !editingMeta && (
            <span style="display: inline-flex; align-items: center; gap: 8px;">
              <IncidentStatusPill status={incident.status} />
              <SeverityIcon severity={SEVERITY_MAP[incident.severity] || 'info'} size={18} />
              <span style="color: var(--color-text-muted); font-size: 0.875rem;">{incident.severity}</span>
            </span>
          )
        }
        actions={
          <div style="display: flex; gap: 8px; align-items: center;">
            {canEdit && !editingMeta && (
              <button class="btn btn-sm" onClick={openMetaEdit}>Edit</button>
            )}
            {canDelete && !editingMeta && (
              <button class="btn btn-sm btn-danger" onClick={deleteIncident}>Delete</button>
            )}
            <a href="/incidents" class="btn btn-sm">Back</a>
          </div>
        }
      />

      {/* Meta edit form */}
      {editingMeta && (
        <Card style="margin-bottom: 1.5rem;">
          <form onSubmit={e => { e.preventDefault(); saveMeta(); }}>
            <div style="display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 1rem;">
              <div class="form-group">
                <label>Title</label>
                <input
                  type="text"
                  value={metaForm.title}
                  onInput={e => setMetaForm({ ...metaForm, title: e.target.value })}
                  required
                />
              </div>
              <div class="form-group">
                <label>Severity</label>
                <select value={metaForm.severity} onChange={e => setMetaForm({ ...metaForm, severity: e.target.value })}>
                  <option value="critical">Critical</option>
                  <option value="major">Major</option>
                  <option value="minor">Minor</option>
                </select>
              </div>
              <div class="form-group">
                <label>Status</label>
                <select value={metaForm.status} onChange={e => setMetaForm({ ...metaForm, status: e.target.value })}>
                  <option value="investigating">Investigating</option>
                  <option value="identified">Identified</option>
                  <option value="monitoring">Monitoring</option>
                  <option value="resolved">Resolved</option>
                  <option value="maintenance">Maintenance</option>
                </select>
              </div>
            </div>
            <div style="display: flex; gap: 8px;">
              <button type="submit" class="btn btn-primary" disabled={metaSaving}>
                {metaSaving ? 'Saving...' : 'Save Changes'}
              </button>
              <button type="button" class="btn" onClick={() => setEditingMeta(false)} disabled={metaSaving}>
                Cancel
              </button>
            </div>
          </form>
        </Card>
      )}

      <div class="detail-grid" style="grid-template-columns: 2fr 1fr;">
        {/* Timeline */}
        <div>
          <h3 style="margin: 0 0 1rem; font-size: 1rem; color: var(--color-text-muted); text-transform: uppercase; letter-spacing: 0.5px;">Timeline</h3>

          {updates.length === 0 ? (
            <p style="color: var(--color-text-muted); margin-bottom: 1.5rem;">No updates yet.</p>
          ) : (
            <div class="incident-timeline">
              {updates.map((update, idx) => {
                const borderColor = STATUS_COLORS[update.status] || 'var(--color-border)';
                const isOwn = update.user_email === userEmail;
                const isEditing = editUpdateId === update.id;

                return (
                  <div
                    key={update.id}
                    class="incident-update"
                    style={{
                      borderLeft: `4px solid ${borderColor}`,
                      paddingLeft: '1rem',
                      marginBottom: '1.25rem',
                      opacity: update.public ? 1 : 0.7,
                    }}
                  >
                    <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px; flex-wrap: wrap;">
                      <span style="font-weight: 600; font-size: 0.875rem;">{update.user_email || 'System'}</span>
                      <span style="color: var(--color-text-muted); font-size: 0.75rem;">{relativeTime(update.created_at)}</span>
                      {update.status && (
                        <IncidentStatusPill status={update.status} />
                      )}
                      {update.public ? (
                        <span style={{
                          display: 'inline-block',
                          padding: '2px 7px',
                          borderRadius: 9999,
                          background: '#27ae60',
                          color: 'white',
                          fontSize: '0.625rem',
                          fontWeight: 700,
                          letterSpacing: '0.5px',
                        }}>PUBLIC</span>
                      ) : (
                        <span style={{
                          display: 'inline-block',
                          padding: '2px 7px',
                          borderRadius: 9999,
                          background: 'var(--color-muted)',
                          color: 'white',
                          fontSize: '0.625rem',
                          fontWeight: 700,
                          letterSpacing: '0.5px',
                        }}>INTERNAL</span>
                      )}
                    </div>

                    {isEditing ? (
                      <div>
                        <textarea
                          rows="3"
                          value={editUpdateBody}
                          onInput={e => setEditUpdateBody(e.target.value)}
                          style="width: 100%; margin-bottom: 8px;"
                        />
                        <div style="display: flex; gap: 8px;">
                          <button class="btn btn-sm btn-primary" disabled={editUpdateSaving}
                            onClick={() => saveEditUpdate(update.id)}>
                            {editUpdateSaving ? 'Saving...' : 'Save'}
                          </button>
                          <button class="btn btn-sm" onClick={() => setEditUpdateId(null)}>Cancel</button>
                        </div>
                      </div>
                    ) : (
                      <p style="margin: 0; white-space: pre-wrap;">{update.body}</p>
                    )}

                    {!isEditing && (canEdit && isOwn || canDelete) && (
                      <div style="display: flex; gap: 8px; margin-top: 6px;">
                        {canEdit && isOwn && (
                          <button class="btn btn-xs" onClick={() => startEditUpdate(update)}>Edit</button>
                        )}
                        {canDelete && (
                          <button class="btn btn-xs btn-danger" onClick={() => deleteUpdate(update.id)}>Delete</button>
                        )}
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          )}

          {/* New update form */}
          {canEdit && (
            <Card>
              <h4 style="margin: 0 0 0.75rem; font-size: 0.9375rem;">Post Update</h4>
              <form onSubmit={submitUpdate}>
                <div class="form-group">
                  <textarea
                    rows="3"
                    value={updateBody}
                    onInput={e => setUpdateBody(e.target.value)}
                    placeholder="Describe what's happening..."
                    required
                  />
                </div>
                <div style="display: flex; align-items: center; gap: 1rem; flex-wrap: wrap; margin-bottom: 0.75rem;">
                  <div class="form-group" style="margin: 0; min-width: 180px;">
                    <label style="margin-bottom: 4px; display: block;">Change Status (optional)</label>
                    <select value={updateStatus} onChange={e => setUpdateStatus(e.target.value)}>
                      <option value="">No change</option>
                      <option value="investigating">Investigating</option>
                      <option value="identified">Identified</option>
                      <option value="monitoring">Monitoring</option>
                      <option value="resolved">Resolved</option>
                      <option value="maintenance">Maintenance</option>
                    </select>
                  </div>
                  <label style="display: flex; align-items: center; gap: 6px; cursor: pointer; padding-top: 1.25rem;">
                    <input
                      type="checkbox"
                      checked={updatePublic}
                      onChange={e => setUpdatePublic(e.target.checked)}
                    />
                    Publish to status page
                  </label>
                </div>
                <button type="submit" class="btn btn-primary" disabled={submitting}>
                  {submitting ? 'Posting...' : 'Post Update'}
                </button>
              </form>
            </Card>
          )}
        </div>

        {/* Sidebar */}
        <div>
          {/* Incident info */}
          <Card style="margin-bottom: 1rem;">
            <h3 style="margin: 0 0 0.75rem; font-size: 0.9375rem;">Details</h3>
            <dl class="config-list">
              <dt>Status</dt>
              <dd><IncidentStatusPill status={incident.status} /></dd>
              <dt>Severity</dt>
              <dd style="display: flex; align-items: center; gap: 6px;">
                <SeverityIcon severity={SEVERITY_MAP[incident.severity] || 'info'} size={16} />
                {incident.severity}
              </dd>
              <dt>Created</dt>
              <dd>{relativeTime(incident.created_at)}</dd>
              {incident.resolved_at && (
                <>
                  <dt>Resolved</dt>
                  <dd>{relativeTime(incident.resolved_at)}</dd>
                </>
              )}
            </dl>
          </Card>

          {/* Affected monitors */}
          {incident.monitor_ids && incident.monitor_ids.length > 0 && (
            <Card style="margin-bottom: 1rem;">
              <h3 style="margin: 0 0 0.75rem; font-size: 0.9375rem;">Affected Monitors</h3>
              <div style="display: flex; flex-direction: column; gap: 6px;">
                {incident.monitor_ids.map(mid => (
                  <a key={mid} href={`/monitors/${mid}`} style="font-size: 0.875rem; color: var(--color-primary);">
                    {(incident.monitors && incident.monitors.find(m => m.id === mid)?.name) || `Monitor ${mid}`}
                  </a>
                ))}
              </div>
            </Card>
          )}

          {/* Status pages */}
          {incident.status_page_ids && incident.status_page_ids.length > 0 && (
            <Card style="margin-bottom: 1rem;">
              <h3 style="margin: 0 0 0.75rem; font-size: 0.9375rem;">Status Pages</h3>
              <div style="display: flex; flex-direction: column; gap: 6px;">
                {incident.status_page_ids.map(spid => (
                  <span key={spid} style="font-size: 0.875rem; color: var(--color-text-muted);">
                    {(incident.status_pages && incident.status_pages.find(sp => sp.id === spid)?.name) || spid}
                  </span>
                ))}
              </div>
            </Card>
          )}

          {/* Originating alert */}
          {incident.alert_id && (
            <Card>
              <h3 style="margin: 0 0 0.75rem; font-size: 0.9375rem;">Originating Alert</h3>
              <a href={`/alerts/${incident.alert_id}`} style="font-size: 0.875rem; color: var(--color-primary);">
                View Alert
              </a>
            </Card>
          )}
        </div>
      </div>
    </div>
  );
}
