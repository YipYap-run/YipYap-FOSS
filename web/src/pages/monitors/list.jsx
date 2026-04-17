import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { PageHeader, Card, StatusBadge, StatusDot, SearchInput, LoadingPage, ErrorMessage, EmptyState, Modal, MiniUptimeBar } from '../../components/ui';
import { currentOrg, appMeta } from '../../state/auth';

const DEFAULT_FORM = {
  name: '',
  type: 'http',
  interval_seconds: 60,
  timeout_seconds: 10,
  latency_warning_ms: '',
  latency_critical_ms: '',
  down_severity: 'critical',
  degraded_severity: 'warning',
  escalation_policy_id: '',
  runbook_url: '',
  service_id: '',
  enabled: true,
  // HTTP
  http_url: '',
  http_method: 'GET',
  http_expected_status: 200,
  http_body_match: '',
  http_headers: '',
  http_body: '',
  // TCP
  tcp_host: '',
  tcp_port: '',
  // Ping
  ping_host: '',
  // DNS
  dns_hostname: '',
  dns_record_type: 'A',
  dns_expected: '',
  dns_nameserver: '',
  // Heartbeat
  heartbeat_grace_period: 300,
};

function buildConfig(form) {
  switch (form.type) {
    case 'http': {
      const cfg = { method: form.http_method, url: form.http_url, expected_status: +form.http_expected_status || 200 };
      if (form.http_body_match) cfg.body_match = form.http_body_match;
      if (form.http_body) cfg.body = form.http_body;
      if (form.http_headers) {
        try { cfg.headers = JSON.parse(form.http_headers); } catch (_) {}
      }
      return cfg;
    }
    case 'tcp':
      return { host: form.tcp_host, port: +form.tcp_port };
    case 'ping':
      return { host: form.ping_host };
    case 'dns': {
      const cfg = { hostname: form.dns_hostname, record_type: form.dns_record_type };
      if (form.dns_expected) cfg.expected = form.dns_expected;
      if (form.dns_nameserver) cfg.nameserver = form.dns_nameserver;
      return cfg;
    }
    case 'heartbeat':
      return { grace_period_seconds: +form.heartbeat_grace_period || 300 };
    default:
      return {};
  }
}

function parseConfigToForm(monitor) {
  const cfg = monitor.config || {};
  const base = {
    ...DEFAULT_FORM,
    name: monitor.name || '',
    type: monitor.type || 'http',
    interval_seconds: monitor.interval_seconds || 60,
    timeout_seconds: monitor.timeout_seconds || 10,
    latency_warning_ms: monitor.latency_warning_ms || '',
    latency_critical_ms: monitor.latency_critical_ms || '',
    down_severity: monitor.down_severity || 'critical',
    degraded_severity: monitor.degraded_severity || 'warning',
    escalation_policy_id: monitor.escalation_policy_id || '',
    enabled: monitor.enabled !== false,
    runbook_url: monitor.runbook_url || '',
    service_id: monitor.service_id || '',
  };

  switch (monitor.type) {
    case 'http':
      base.http_url = cfg.url || '';
      base.http_method = cfg.method || 'GET';
      base.http_expected_status = cfg.expected_status || 200;
      base.http_body_match = cfg.body_match || '';
      base.http_headers = cfg.headers ? JSON.stringify(cfg.headers, null, 2) : '';
      base.http_body = cfg.body || '';
      break;
    case 'tcp':
      base.tcp_host = cfg.host || '';
      base.tcp_port = cfg.port || '';
      break;
    case 'ping':
      base.ping_host = cfg.host || '';
      break;
    case 'dns':
      base.dns_hostname = cfg.hostname || '';
      base.dns_record_type = cfg.record_type || 'A';
      base.dns_expected = cfg.expected || '';
      base.dns_nameserver = cfg.nameserver || '';
      break;
    case 'heartbeat':
      base.heartbeat_grace_period = cfg.grace_period_seconds || 300;
      break;
  }
  return base;
}

function TypeConfigFields({ form, setForm }) {
  const set = (key, val) => setForm({ ...form, [key]: val });

  switch (form.type) {
    case 'http':
      return (
        <>
          <div class="form-group">
            <label>URL</label>
            <input type="url" value={form.http_url} onInput={e => set('http_url', e.target.value)} required placeholder="https://example.com" />
          </div>
          <div class="form-group">
            <label>Method</label>
            <select value={form.http_method} onChange={e => set('http_method', e.target.value)}>
              {['GET', 'POST', 'PUT', 'DELETE', 'HEAD', 'PATCH'].map(m => <option key={m} value={m}>{m}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Expected Status</label>
            <input type="number" value={form.http_expected_status} onInput={e => set('http_expected_status', e.target.value)} />
          </div>
          <div class="form-group">
            <label>Body Match</label>
            <input type="text" value={form.http_body_match} onInput={e => set('http_body_match', e.target.value)} placeholder="Optional substring to match in response" />
          </div>
          <div class="form-group">
            <label>Headers (JSON)</label>
            <input type="text" value={form.http_headers} onInput={e => set('http_headers', e.target.value)} placeholder='{"Authorization": "Bearer ..."}' />
          </div>
          <div class="form-group">
            <label>Body</label>
            <textarea rows="3" value={form.http_body} onInput={e => set('http_body', e.target.value)} placeholder="Optional request body" />
          </div>
        </>
      );
    case 'tcp':
      return (
        <>
          <div class="form-group">
            <label>Host</label>
            <input type="text" value={form.tcp_host} onInput={e => set('tcp_host', e.target.value)} required placeholder="example.com" />
          </div>
          <div class="form-group">
            <label>Port</label>
            <input type="number" value={form.tcp_port} onInput={e => set('tcp_port', e.target.value)} required placeholder="443" />
          </div>
        </>
      );
    case 'ping':
      return (
        <div class="form-group">
          <label>Host</label>
          <input type="text" value={form.ping_host} onInput={e => set('ping_host', e.target.value)} required placeholder="example.com" />
        </div>
      );
    case 'dns':
      return (
        <>
          <div class="form-group">
            <label>Hostname</label>
            <input type="text" value={form.dns_hostname} onInput={e => set('dns_hostname', e.target.value)} required placeholder="example.com" />
          </div>
          <div class="form-group">
            <label>Record Type</label>
            <select value={form.dns_record_type} onChange={e => set('dns_record_type', e.target.value)}>
              {['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS'].map(t => <option key={t} value={t}>{t}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Expected</label>
            <input type="text" value={form.dns_expected} onInput={e => set('dns_expected', e.target.value)} placeholder="Optional expected value" />
          </div>
          <div class="form-group">
            <label>Nameserver</label>
            <input type="text" value={form.dns_nameserver} onInput={e => set('dns_nameserver', e.target.value)} placeholder="Optional, e.g. 8.8.8.8" />
          </div>
        </>
      );
    case 'heartbeat':
      return (
        <div class="form-group">
          <label>Grace Period (seconds)</label>
          <input type="number" value={form.heartbeat_grace_period} onInput={e => set('heartbeat_grace_period', e.target.value)} />
        </div>
      );
    default:
      return null;
  }
}

export function MonitorListPage() {
  const [monitors, setMonitors] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ ...DEFAULT_FORM });
  const [policies, setPolicies] = useState([]);
  const [services, setServices] = useState([]);
  const [saving, setSaving] = useState(false);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get('/monitors');
      setMonitors(data.monitors || data || []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  async function loadPolicies() {
    try {
      const data = await get('/escalation-policies');
      setPolicies(data.policies || data || []);
    } catch (_) {}
  }

  async function loadServices() {
    try {
      const data = await get('/services');
      setServices(data.services || data || []);
    } catch (_) {}
  }

  useEffect(() => { load(); }, []);

  // Auto-open edit modal when navigated with ?edit=<id>
  useEffect(() => {
    if (!loading && monitors.length > 0) {
      const params = new URLSearchParams(window.location.search);
      const editParam = params.get('edit');
      if (editParam) {
        const m = monitors.find(mon => mon.id === editParam);
        if (m) {
          setEditId(m.id);
          setForm(parseConfigToForm(m));
          loadPolicies();
          loadServices();
          setShowModal(true);
        }
      }
    }
  }, [loading, monitors]);

  function openCreate() {
    setEditId(null);
    setForm({ ...DEFAULT_FORM });
    loadPolicies();
    loadServices();
    setShowModal(true);
  }

  function openEdit(e, monitor) {
    e.preventDefault();
    e.stopPropagation();
    setEditId(monitor.id);
    setForm(parseConfigToForm(monitor));
    loadPolicies();
    loadServices();
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
        type: form.type,
        config: buildConfig(form),
        interval_seconds: +form.interval_seconds || 60,
        timeout_seconds: +form.timeout_seconds || 10,
        down_severity: form.down_severity,
        degraded_severity: form.degraded_severity,
        enabled: form.enabled,
      };
      if (form.latency_warning_ms) body.latency_warning_ms = +form.latency_warning_ms;
      if (form.latency_critical_ms) body.latency_critical_ms = +form.latency_critical_ms;
      if (form.escalation_policy_id) body.escalation_policy_id = form.escalation_policy_id;
      if (form.runbook_url) body.runbook_url = form.runbook_url;
      if (form.service_id) body.service_id = form.service_id;

      if (editId) await patch(`/monitors/${editId}`, body);
      else await post('/monitors', body);

      closeModal();
      load();
    } catch (err) {
      alert(err.message || 'Failed to save monitor');
    } finally {
      setSaving(false);
    }
  }

  async function handleDelete() {
    if (!editId) return;
    if (!confirm('Are you sure you want to delete this monitor?')) return;
    setSaving(true);
    try {
      await del(`/monitors/${editId}`);
      closeModal();
      load();
    } catch (err) {
      alert(err.message || 'Failed to delete monitor');
    } finally {
      setSaving(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  const types = [...new Set(monitors.map(m => m.type))].sort();
  const filtered = monitors.filter(m => {
    if (search && !m.name.toLowerCase().includes(search.toLowerCase())) return false;
    if (typeFilter && m.type !== typeFilter) return false;
    return true;
  });

  return (
    <div class="monitors-page">
      <PageHeader title="Monitors" subtitle={`${monitors.length} monitors`}
        actions={
          appMeta.value?.billing_enabled && currentOrg.value?.plan === 'free' && monitors.length >= 10
            ? <a href="/settings/billing" class="btn btn-primary">Upgrade to add more monitors</a>
            : <button class="btn btn-primary" onClick={openCreate}>New Monitor</button>
        } />

      <div class="filters-bar">
        <SearchInput value={search} onInput={setSearch} placeholder="Search monitors..." />
        <select class="filter-select" value={typeFilter} onChange={e => setTypeFilter(e.target.value)}>
          <option value="">All types</option>
          {types.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
      </div>

      {filtered.length === 0 ? (
        <EmptyState title="No monitors found"
          description={search || typeFilter ? 'Try adjusting your filters' : 'Create your first monitor to get started'} />
      ) : (
        <div class="monitor-list">
          {filtered.map(m => (
            <a key={m.id} href={`/monitors/${m.id}`} class="monitor-card">
              <div class="monitor-card-row">
                <StatusDot status={m.status || 'unknown'} />
                <div class="monitor-card-body">
                  <h3 class="monitor-card-name">{m.name}</h3>
                  <p class="monitor-card-subtitle">
                    {m.type.toUpperCase()} · {m.interval_seconds || 60}s interval
                    {m.tls_days_remaining > 0 && ` · TLS valid ${m.tls_days_remaining} days`}
                    {m.status === 'degraded' && ' · Degraded'}
                  </p>
                  {m.labels && Object.keys(m.labels).length > 0 && (
                    <div class="monitor-card-labels">
                      {Object.entries(m.labels).map(([k, v]) => (
                        <span key={k} class="label-tag">{k}: {v}</span>
                      ))}
                    </div>
                  )}
                </div>
                <div class="monitor-card-right">
                  {m.recent_checks && <MiniUptimeBar checks={m.recent_checks} />}
                  {m.uptime_pct != null && (
                    <span class="monitor-card-uptime" style={{ color: m.uptime_pct >= 99.9 ? 'var(--color-up)' : m.uptime_pct >= 99 ? 'var(--color-warning)' : 'var(--color-down)' }}>
                      {m.uptime_pct.toFixed(m.uptime_pct === 100 ? 0 : 2)}%
                    </span>
                  )}
                </div>
              </div>
            </a>
          ))}
        </div>
      )}

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit Monitor' : 'New Monitor'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required placeholder="My Monitor" />
          </div>
          <div class="form-group">
            <label>Type</label>
            <select value={form.type} onChange={e => setForm({ ...form, type: e.target.value })}>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="dns">DNS</option>
              <option value="heartbeat">Heartbeat</option>
            </select>
          </div>

          <TypeConfigFields form={form} setForm={setForm} />

          <div class="form-group">
            <label>Interval (seconds)</label>
            <input type="number" value={form.interval_seconds}
                   min={currentOrg.value?.plan === 'enterprise' ? 10 : currentOrg.value?.plan === 'free' ? 300 : 30}
                   onInput={e => setForm({ ...form, interval_seconds: e.target.value })} />
            {currentOrg.value?.plan === 'free' && <p class="form-help">Free plan minimum: 300s (5 min). <a href="/settings/billing">Upgrade</a> for faster checks.</p>}
          </div>
          <div class="form-group">
            <label>Timeout (seconds)</label>
            <input type="number" value={form.timeout_seconds} onInput={e => setForm({ ...form, timeout_seconds: e.target.value })} />
          </div>
          <div class="form-group">
            <label>Latency Warning (ms)</label>
            <input type="number" value={form.latency_warning_ms} onInput={e => setForm({ ...form, latency_warning_ms: e.target.value })} placeholder="e.g. 500" />
          </div>
          <div class="form-group">
            <label>Latency Critical (ms)</label>
            <input type="number" value={form.latency_critical_ms} onInput={e => setForm({ ...form, latency_critical_ms: e.target.value })} placeholder="e.g. 2000" />
          </div>
          <div class="form-group">
            <label>Down Severity</label>
            <select value={form.down_severity} onChange={e => setForm({ ...form, down_severity: e.target.value })}>
              <option value="critical">Critical</option>
              <option value="warning">Warning</option>
              <option value="info">Info</option>
            </select>
          </div>
          <div class="form-group">
            <label>Degraded Severity</label>
            <select value={form.degraded_severity} onChange={e => setForm({ ...form, degraded_severity: e.target.value })}>
              <option value="critical">Critical</option>
              <option value="warning">Warning</option>
              <option value="info">Info</option>
            </select>
          </div>
          <div class="form-group">
            <label>Escalation Policy</label>
            <select value={form.escalation_policy_id} onChange={e => setForm({ ...form, escalation_policy_id: e.target.value })}>
              <option value="">None</option>
              {policies.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Runbook URL</label>
            <input type="url" value={form.runbook_url}
                   onInput={e => setForm({ ...form, runbook_url: e.target.value })}
                   placeholder="https://wiki.example.com/runbooks/..." />
          </div>
          {appMeta.value?.edition !== 'foss' && (
            <div class="form-group">
              <label>Service</label>
              <select value={form.service_id}
                      onChange={e => setForm({ ...form, service_id: e.target.value })}>
                <option value="">None</option>
                {services.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
              </select>
            </div>
          )}
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.enabled} onChange={e => setForm({ ...form, enabled: e.target.checked })} />
              Enabled
            </label>
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
