import { useState, useEffect } from 'preact/hooks';
import { get, post, del } from '../../api/client';
import { PageHeader, Card, LoadingPage, ErrorMessage } from '../../components/ui';
import { safeHref } from '../../utils/url';

export function ServiceDetailPage({ id }) {
  const [service, setService] = useState(null);
  const [allServices, setAllServices] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [editing, setEditing] = useState(false);

  // Link form state
  const [linkLabel, setLinkLabel] = useState('');
  const [linkUrl, setLinkUrl] = useState('');

  // Dependency form state
  const [depServiceId, setDepServiceId] = useState('');
  const [depRelationship, setDepRelationship] = useState('');

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const [svc, svcs] = await Promise.all([
        get(`/services/${id}`),
        get('/services').catch(() => ({ services: [] })),
      ]);
      setService(svc);
      const list = svcs.services || svcs || [];
      setAllServices(list.filter(s => s.id !== id));
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [id]);

  async function addLink(e) {
    e.preventDefault();
    if (!linkLabel.trim() || !linkUrl.trim()) return;
    await post(`/services/${id}/links`, { label: linkLabel, url_template: linkUrl });
    setLinkLabel('');
    setLinkUrl('');
    load();
  }

  async function removeLink(linkId) {
    await del(`/services/${id}/links/${linkId}`);
    load();
  }

  async function addDependency(e) {
    e.preventDefault();
    if (!depServiceId || !depRelationship.trim()) return;
    await post(`/services/${id}/dependencies`, { depends_on_service_id: depServiceId, relationship: depRelationship });
    setDepServiceId('');
    setDepRelationship('');
    load();
  }

  async function removeDependency(depId) {
    await del(`/services/${id}/dependencies/${depId}`);
    load();
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;
  if (!service) return <ErrorMessage error="Service not found" />;

  const links = service.links || [];
  const dependencies = service.dependencies || [];
  const dependents = service.dependents || [];
  const monitors = service.monitors || [];

  return (
    <div class="service-detail">
      <PageHeader
        title={service.name}
        subtitle={service.slug}
        actions={
          <button class={`btn btn-sm ${editing ? 'btn-primary' : ''}`} onClick={() => setEditing(!editing)}>
            {editing ? 'Done' : 'Edit'}
          </button>
        }
      />

      <div class="detail-grid">
        {/* Service Info */}
        <Card>
          <h3>Service Info</h3>
          <dl class="config-list">
            <dt>Name</dt><dd>{service.name}</dd>
            <dt>Slug</dt><dd>{service.slug}</dd>
            {service.description && <><dt>Description</dt><dd>{service.description}</dd></>}
            {service.runbook_url && (
              <><dt>Runbook</dt><dd>
                {safeHref(service.runbook_url)
                  ? <a href={safeHref(service.runbook_url)} target="_blank" rel="noopener">{service.runbook_url}</a>
                  : <span>{service.runbook_url}</span>}
              </dd></>
            )}
            {service.notes && <><dt>Notes</dt><dd>{service.notes}</dd></>}
          </dl>
        </Card>

        {/* Linked Monitors */}
        <Card>
          <h3>Linked Monitors</h3>
          {monitors.length > 0 ? (
            <table class="data-table">
              <thead>
                <tr>
                  <th>Name</th>
                  <th>Type</th>
                </tr>
              </thead>
              <tbody>
                {monitors.map(m => (
                  <tr key={m.id}>
                    <td><a href={`/monitors/${m.id}`}>{m.name}</a></td>
                    <td>{m.type}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p class="text-muted">No monitors linked to this service</p>
          )}
        </Card>

        {/* Service Links */}
        <Card>
          <h3>Service Links</h3>
          {links.length > 0 ? (
            <table class="data-table">
              <thead>
                <tr>
                  <th>Label</th>
                  <th>URL</th>
                  {editing && <th></th>}
                </tr>
              </thead>
              <tbody>
                {links.map(l => (
                  <tr key={l.id}>
                    <td>{l.label}</td>
                    <td>
                      {safeHref(l.url_template)
                        ? <a href={safeHref(l.url_template)} target="_blank" rel="noopener">{l.url_template}</a>
                        : <span>{l.url_template}</span>}
                    </td>
                    {editing && (
                      <td>
                        <button class="btn btn-xs btn-danger" onClick={() => removeLink(l.id)}>Delete</button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p class="text-muted">No links yet</p>
          )}
          {editing && (
            <form onSubmit={addLink} class="form-group" style="margin-top:.75rem;display:flex;gap:.5rem;align-items:flex-end">
              <input type="text" placeholder="Label" value={linkLabel} onInput={e => setLinkLabel(e.target.value)} />
              <input type="text" placeholder="URL" value={linkUrl} onInput={e => setLinkUrl(e.target.value)} />
              <button type="submit" class="btn btn-sm btn-primary">Add</button>
            </form>
          )}
        </Card>

        {/* Dependencies */}
        <Card>
          <h3>Dependencies</h3>
          {dependencies.length > 0 ? (
            <table class="data-table">
              <thead>
                <tr>
                  <th>Service</th>
                  <th>Relationship</th>
                  {editing && <th></th>}
                </tr>
              </thead>
              <tbody>
                {dependencies.map(d => (
                  <tr key={d.id}>
                    <td><a href={`/services/${d.depends_on_service_id}`}>{d.service_name}</a></td>
                    <td>{d.relationship}</td>
                    {editing && (
                      <td>
                        <button class="btn btn-xs btn-danger" onClick={() => removeDependency(d.id)}>Delete</button>
                      </td>
                    )}
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p class="text-muted">No dependencies</p>
          )}
          {editing && (
            <form onSubmit={addDependency} class="form-group" style="margin-top:.75rem;display:flex;gap:.5rem;align-items:flex-end">
              <select value={depServiceId} onChange={e => setDepServiceId(e.target.value)}>
                <option value="">Select service...</option>
                {allServices.map(s => (
                  <option key={s.id} value={s.id}>{s.name}</option>
                ))}
              </select>
              <input type="text" placeholder="Relationship" value={depRelationship} onInput={e => setDepRelationship(e.target.value)} />
              <button type="submit" class="btn btn-sm btn-primary">Add</button>
            </form>
          )}
        </Card>

        {/* Dependents (read-only) */}
        <Card>
          <h3>Dependents</h3>
          {dependents.length > 0 ? (
            <table class="data-table">
              <thead>
                <tr>
                  <th>Service</th>
                  <th>Relationship</th>
                </tr>
              </thead>
              <tbody>
                {dependents.map(d => (
                  <tr key={d.service_id || d.id}>
                    <td><a href={`/services/${d.service_id}`}>{d.service_name}</a></td>
                    <td>{d.relationship}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          ) : (
            <p class="text-muted">No other services depend on this one</p>
          )}
        </Card>
      </div>
    </div>
  );
}
