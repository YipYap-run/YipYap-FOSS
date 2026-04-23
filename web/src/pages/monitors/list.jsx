import { useState, useEffect } from 'preact/hooks';
import { get, post, patch, del, put } from '../../api/client';
import { PageHeader, Card, StatusBadge, StatusDot, SearchInput, LoadingPage, ErrorMessage, EmptyState, Modal, MiniUptimeBar } from '../../components/ui';
import { currentOrg, appMeta } from '../../state/auth';
import { useFormDraft } from '../../hooks/useFormDraft';

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
  group_id: '',
  runbook_url: '',
  service_id: '',
  description: '',
  auto_resolve: false,
  tags: '',
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
    group_id: monitor.group_id || '',
    enabled: monitor.enabled !== false,
    runbook_url: monitor.runbook_url || '',
    service_id: monitor.service_id || '',
    description: monitor.description || '',
    auto_resolve: monitor.auto_resolve || false,
    tags: monitor.labels ? Object.entries(monitor.labels).map(([k, v]) => v ? `${k}:${v}` : k).join(', ') : '',
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
            <label class="required">URL</label>
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
            <label>Body Match <span class="form-optional">(optional)</span></label>
            <input type="text" value={form.http_body_match} onInput={e => set('http_body_match', e.target.value)} placeholder="Optional substring to match in response" />
          </div>
          <div class="form-group">
            <label>Headers (JSON) <span class="form-optional">(optional)</span></label>
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
            <label class="required">Host</label>
            <input type="text" value={form.tcp_host} onInput={e => set('tcp_host', e.target.value)} required placeholder="example.com" />
          </div>
          <div class="form-group">
            <label class="required">Port</label>
            <input type="number" value={form.tcp_port} onInput={e => set('tcp_port', e.target.value)} required placeholder="443" />
          </div>
        </>
      );
    case 'ping':
      return (
        <div class="form-group">
          <label class="required">Host</label>
          <input type="text" value={form.ping_host} onInput={e => set('ping_host', e.target.value)} required placeholder="example.com" />
        </div>
      );
    case 'dns':
      return (
        <>
          <div class="form-group">
            <label class="required">Hostname</label>
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

function MonitorCard({ m, openEdit }) {
  const isPending = m.enabled && !m.status && m.uptime_pct == null && !m.recent_checks;
  return (
    <a key={m.id} href={`/monitors/${m.id}`} class="monitor-card">
      <div class="monitor-card-row">
        {isPending
          ? <span title="Pending first check" style={{
              display: 'inline-block', width: 10, height: 10, borderRadius: '50%',
              background: 'var(--color-info, #3b82f6)', flexShrink: 0,
              animation: 'pending-pulse 1.5s ease-in-out infinite',
            }} />
          : <StatusDot status={m.status || 'unknown'} />}
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
          {isPending
            ? <span class="monitor-card-uptime" style={{ color: 'var(--color-text-muted, #94a3b8)' }}>Pending</span>
            : m.uptime_pct != null
              ? <span class="monitor-card-uptime" style={{ color: m.uptime_pct >= 99.9 ? 'var(--color-up)' : m.uptime_pct >= 99 ? 'var(--color-warning)' : 'var(--color-down)' }}>
                  {m.uptime_pct.toFixed(m.uptime_pct === 100 ? 0 : 2)}%
                </span>
              : null}
        </div>
      </div>
    </a>
  );
}

export function MonitorListPage() {
  const [monitors, setMonitors] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [search, setSearch] = useState('');
  const [typeFilter, setTypeFilter] = useState('');
  const [tagFilter, setTagFilter] = useState('');
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm, { hasDraft, discard: discardDraft, clearDraft }] = useFormDraft('monitor-form', DEFAULT_FORM);
  const [policies, setPolicies] = useState([]);
  const [services, setServices] = useState([]);
  const [saving, setSaving] = useState(false);
  const [matchRules, setMatchRules] = useState([]);
  const [monitorStates, setMonitorStates] = useState([]);
  const [groups, setGroups] = useState([]);
  const [groupView, setGroupView] = useState(false);
  const [showGroupModal, setShowGroupModal] = useState(false);
  const [editGroupId, setEditGroupId] = useState(null);
  const [groupForm, setGroupForm] = useState({ name: '', description: '' });
  const [collapsedGroups, setCollapsedGroups] = useState({});

  useEffect(() => {
    if (!hasDraft) return;
    const handler = e => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [hasDraft]);

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

  async function loadMonitorStates() {
    try {
      const data = await get('/monitor-states');
      setMonitorStates(data.states || data || []);
    } catch (_) { /* optional feature */ }
  }

  async function loadMatchRules(monitorId) {
    if (!monitorId) { setMatchRules([]); return; }
    try {
      const data = await get(`/monitors/${monitorId}/rules`);
      setMatchRules(data.rules || data || []);
    } catch (_) { setMatchRules([]); }
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

  async function loadGroups() {
    try {
      const data = await get('/monitor-groups');
      setGroups(data || []);
    } catch (_) {}
  }

  useEffect(() => { load(); loadGroups(); }, []);

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
          loadMonitorStates();
          loadMatchRules(m.id);
          setShowModal(true);
        }
      }
    }
  }, [loading, monitors]);

  function openCreate() {
    setEditId(null);
    setForm({ ...DEFAULT_FORM });
    setMatchRules([]);
    loadPolicies();
    loadServices();
    loadMonitorStates();
    loadGroups();
    setShowModal(true);
  }

  function openEdit(e, monitor) {
    e.preventDefault();
    e.stopPropagation();
    setEditId(monitor.id);
    setForm(parseConfigToForm(monitor));
    loadPolicies();
    loadServices();
    loadMonitorStates();
    loadGroups();
    loadMatchRules(monitor.id);
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
        description: form.description || '',
        auto_resolve: !!form.auto_resolve,
      };
      if (form.latency_warning_ms) body.latency_warning_ms = +form.latency_warning_ms;
      if (form.latency_critical_ms) body.latency_critical_ms = +form.latency_critical_ms;
      if (form.escalation_policy_id) body.escalation_policy_id = form.escalation_policy_id;
      body.group_id = form.group_id || '';
      if (form.runbook_url) body.runbook_url = form.runbook_url;
      if (form.service_id) body.service_id = form.service_id;

      // Parse tags into labels map.
      if (form.tags && form.tags.trim()) {
        const labels = {};
        form.tags.split(',').forEach(tag => {
          const t = tag.trim();
          if (!t) return;
          const idx = t.indexOf(':');
          if (idx > 0) {
            labels[t.slice(0, idx).trim()] = t.slice(idx + 1).trim();
          } else {
            labels[t] = '';
          }
        });
        body.labels = labels;
      } else {
        body.labels = {};
      }

      let monitorId = editId;
      if (editId) {
        await patch(`/monitors/${editId}`, body);
      } else {
        const created = await post('/monitors', body);
        monitorId = created?.id || created?.monitor?.id;
      }

      // Save match rules if we have a monitor ID.
      if (monitorId && matchRules.length > 0) {
        const rulesToSave = matchRules.map((r, idx) => ({
          status_code: r.status_code || null,
          status_code_min: r.status_code_min || null,
          status_code_max: r.status_code_max || null,
          body_match: r.body_match || '',
          body_match_mode: r.body_match_mode || 'contains',
          header_match: r.header_match || '',
          header_value: r.header_value || '',
          state_id: r.state_id,
          position: idx,
        }));
        try {
          await put(`/monitors/${monitorId}/rules`, { rules: rulesToSave });
        } catch (_) { /* non-fatal */ }
      } else if (monitorId && matchRules.length === 0 && editId) {
        // Clear rules if all were removed.
        try {
          await put(`/monitors/${monitorId}/rules`, { rules: [] });
        } catch (_) { /* non-fatal */ }
      }

      clearDraft();
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

  function openGroupCreate() {
    setEditGroupId(null);
    setGroupForm({ name: '', description: '' });
    setShowGroupModal(true);
  }

  function openGroupEdit(group) {
    setEditGroupId(group.id);
    setGroupForm({ name: group.name, description: group.description || '' });
    setShowGroupModal(true);
  }

  async function saveGroup() {
    if (!groupForm.name) { alert('Name is required'); return; }
    try {
      if (editGroupId) {
        await patch(`/monitor-groups/${editGroupId}`, groupForm);
      } else {
        await post('/monitor-groups', groupForm);
      }
      setShowGroupModal(false);
      loadGroups();
    } catch (err) {
      alert(err.message || 'Failed to save group');
    }
  }

  async function deleteGroup() {
    if (!editGroupId) return;
    if (!confirm('Delete this group? Monitors will be ungrouped, not deleted.')) return;
    try {
      await del(`/monitor-groups/${editGroupId}`);
      setShowGroupModal(false);
      loadGroups();
      load();
    } catch (err) {
      alert(err.message || 'Failed to delete group');
    }
  }

  function toggleGroupCollapse(gid) {
    setCollapsedGroups(prev => ({ ...prev, [gid]: !prev[gid] }));
  }

  function worstStatus(monitors) {
    if (monitors.some(m => m.status === 'down')) return 'down';
    if (monitors.some(m => m.status === 'degraded')) return 'degraded';
    if (monitors.some(m => m.status === 'up')) return 'up';
    return 'unknown';
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  const types = [...new Set(monitors.map(m => m.type))].sort();
  // Collect all unique label keys from monitors for the tag filter bar.
  const allTags = [...new Set(monitors.flatMap(m => {
    if (!m.labels) return [];
    return Object.entries(m.labels).map(([k, v]) => v ? `${k}:${v}` : k);
  }))].sort();
  const filtered = monitors.filter(m => {
    if (search && !m.name.toLowerCase().includes(search.toLowerCase())) return false;
    if (typeFilter && m.type !== typeFilter) return false;
    if (tagFilter) {
      if (!m.labels) return false;
      const idx = tagFilter.indexOf(':');
      if (idx > 0) {
        const k = tagFilter.slice(0, idx);
        const v = tagFilter.slice(idx + 1);
        if (m.labels[k] !== v) return false;
      } else {
        if (!(tagFilter in m.labels)) return false;
      }
    }
    return true;
  });

  return (
    <div class="monitors-page">
      <PageHeader title="Monitors" subtitle={`${monitors.length} monitors`}
        actions={
          appMeta.value?.billing_enabled && currentOrg.value?.plan === 'free' && monitors.length >= 10
            ? <a href="/settings/billing" class="btn btn-primary">Upgrade to add more monitors</a>
            : <div style="display: flex; gap: 8px;">
                <button class="btn" onClick={openGroupCreate}>New Group</button>
                <button class="btn btn-primary" onClick={openCreate}>New Monitor</button>
              </div>
        } />

      <div class="filters-bar">
        <SearchInput value={search} onInput={setSearch} placeholder="Search monitors..." />
        <select class="filter-select" value={typeFilter} onChange={e => setTypeFilter(e.target.value)}>
          <option value="">All types</option>
          {types.map(t => <option key={t} value={t}>{t}</option>)}
        </select>
        {groups.length > 0 && (
          <button class={`btn btn-sm ${groupView ? 'btn-primary' : ''}`} onClick={() => setGroupView(!groupView)}>
            {groupView ? 'List View' : 'Group View'}
          </button>
        )}
      </div>

      {allTags.length > 0 && (
        <div class="tag-filter-bar" style="display: flex; flex-wrap: wrap; gap: 6px; margin-bottom: 16px;">
          {tagFilter && (
            <button class="label-tag label-tag--active" onClick={() => setTagFilter('')}
              style="cursor: pointer; background: var(--color-primary); color: white;">
              {tagFilter} &times;
            </button>
          )}
          {allTags.filter(t => t !== tagFilter).map(tag => (
            <button key={tag} class="label-tag" onClick={() => setTagFilter(tag)}
              style="cursor: pointer; background: var(--color-surface-raised, #1e293b); border: 1px solid var(--color-border); color: var(--color-text-secondary);">
              {tag}
            </button>
          ))}
        </div>
      )}

      {filtered.length === 0 ? (
        <EmptyState title="No monitors found"
          description={search || typeFilter ? 'Try adjusting your filters' : 'Create your first monitor to get started'} />
      ) : groupView && groups.length > 0 ? (
        <div class="monitor-list">
          {groups.map(g => {
            const groupMonitors = filtered.filter(m => m.group_id === g.id);
            if (groupMonitors.length === 0) return null;
            const collapsed = collapsedGroups[g.id];
            return (
              <div key={g.id} style="margin-bottom: 16px;">
                <div onClick={() => toggleGroupCollapse(g.id)}
                  style="display: flex; align-items: center; gap: 8px; padding: 10px 12px; background: var(--color-surface-raised, #1e293b); border-radius: 8px; cursor: pointer; margin-bottom: 4px;">
                  <StatusDot status={worstStatus(groupMonitors)} />
                  <strong style="flex: 1;">{g.name}</strong>
                  <span style="font-size: 0.8125rem; color: var(--color-text-muted);">{groupMonitors.length} monitors</span>
                  <button class="btn btn-xs" onClick={e => { e.stopPropagation(); openGroupEdit(g); }}>Edit</button>
                  <span style="font-size: 0.75rem;">{collapsed ? '\u25B6' : '\u25BC'}</span>
                </div>
                {!collapsed && groupMonitors.map(m => <MonitorCard key={m.id} m={m} openEdit={openEdit} />)}
              </div>
            );
          })}
          {(() => {
            const ungrouped = filtered.filter(m => !m.group_id || !groups.find(g => g.id === m.group_id));
            if (ungrouped.length === 0) return null;
            return (
              <div style="margin-bottom: 16px;">
                <div style="padding: 10px 12px; background: var(--color-surface-raised, #1e293b); border-radius: 8px; margin-bottom: 4px;">
                  <strong>Ungrouped</strong>
                  <span style="font-size: 0.8125rem; color: var(--color-text-muted); margin-left: 8px;">{ungrouped.length} monitors</span>
                </div>
                {ungrouped.map(m => <MonitorCard key={m.id} m={m} openEdit={openEdit} />)}
              </div>
            );
          })()}
        </div>
      ) : (
        <div class="monitor-list">
          {filtered.map(m => <MonitorCard key={m.id} m={m} openEdit={openEdit} />)}
        </div>
      )}

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit Monitor' : 'New Monitor'}>
        {hasDraft && !editId && (
          <div style="display: flex; align-items: center; gap: 8px; padding: 8px 12px; margin-bottom: 12px; background: var(--color-warning-bg, #fff8e1); border-radius: 6px; font-size: 0.875rem;">
            <span>You have an unsaved draft.</span>
            <button type="button" class="btn btn-sm" onClick={discardDraft}>Discard</button>
          </div>
        )}
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label class="required">Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required placeholder="My Monitor" />
          </div>
          <div class="form-group">
            <label class="required">Type</label>
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
            <label>Escalation Policy <span class="form-optional">(optional)</span></label>
            <select value={form.escalation_policy_id} onChange={e => setForm({ ...form, escalation_policy_id: e.target.value })}>
              <option value="">None</option>
              {policies.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Group <span class="form-optional">(optional)</span></label>
            <select value={form.group_id} onChange={e => setForm({ ...form, group_id: e.target.value })}>
              <option value="">None</option>
              {groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label>Runbook URL <span class="form-optional">(optional)</span></label>
            <input type="url" value={form.runbook_url}
                   onInput={e => setForm({ ...form, runbook_url: e.target.value })}
                   placeholder="https://wiki.example.com/runbooks/..." />
          </div>
          {appMeta.value?.edition !== 'foss' && (
            <div class="form-group">
              <label>Service <span class="form-optional">(optional)</span></label>
              <select value={form.service_id}
                      onChange={e => setForm({ ...form, service_id: e.target.value })}>
                <option value="">None</option>
                {services.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
              </select>
            </div>
          )}
          <div class="form-group">
            <label>Description <span class="form-optional">(optional)</span></label>
            <textarea rows="3" value={form.description}
              onInput={e => setForm({ ...form, description: e.target.value })}
              placeholder="Optional description for this monitor" />
          </div>
          <div class="form-group">
            <label>Tags <span class="form-optional">(optional)</span></label>
            <input type="text" value={form.tags}
              onInput={e => setForm({ ...form, tags: e.target.value })}
              placeholder="env:prod, team:backend, region:us-east" />
            <p class="form-help">Comma-separated tags. Use key:value format for labeled tags.</p>
          </div>
          <div style="margin-top: 16px; margin-bottom: 8px;">
            <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 4px;">
              <h4 style="margin: 0; font-size: 0.9375rem;">Match Rules</h4>
              <span class="form-optional">(optional)</span>
            </div>
            <p style="font-size: 0.8125rem; color: var(--color-text-muted); margin-bottom: 8px;">
              Rules are evaluated in order. First match wins. If no rule matches, default status logic applies.
            </p>
            {matchRules.map((rule, idx) => (
              <div key={idx} class="card" style="padding: 12px; margin-bottom: 8px;">
                <div style="display: grid; grid-template-columns: 1fr 1fr 1fr; gap: 8px;">
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Status Code</label>
                    <input type="number" placeholder="e.g. 200"
                      value={rule.status_code || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Body Match</label>
                    <input type="text" placeholder="e.g. ok"
                      value={rule.body_match || ''}
                      onInput={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, body_match: e.target.value } : r))} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Assign State</label>
                    <select value={rule.state_id || ''}
                      onChange={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, state_id: e.target.value } : r))}>
                      <option value="">-- select --</option>
                      {monitorStates.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
                    </select>
                  </div>
                </div>
                <div style="display: grid; grid-template-columns: 1fr 1fr 1fr 1fr; gap: 8px; margin-top: 8px;">
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Status Min</label>
                    <input type="number" placeholder="e.g. 200"
                      value={rule.status_code_min || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code_min: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Status Max</label>
                    <input type="number" placeholder="e.g. 299"
                      value={rule.status_code_max || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code_max: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Body Mode</label>
                    <select value={rule.body_match_mode || 'contains'}
                      onChange={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, body_match_mode: e.target.value } : r))}>
                      <option value="contains">Contains</option>
                      <option value="not_contains">Not Contains</option>
                      <option value="regex">Regex</option>
                    </select>
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label style="font-size: 0.75rem;">Header</label>
                    <input type="text" placeholder="X-Status"
                      value={rule.header_match || ''}
                      onInput={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, header_match: e.target.value } : r))} />
                  </div>
                </div>
                <div style="margin-top: 8px; display: flex; justify-content: flex-end;">
                  <button type="button" class="btn btn-xs btn-danger" onClick={() => setMatchRules(matchRules.filter((_, i) => i !== idx))}>
                    Remove
                  </button>
                </div>
              </div>
            ))}
            <button type="button" class="btn btn-sm" onClick={() => setMatchRules([...matchRules, { status_code: null, body_match: '', body_match_mode: 'contains', state_id: '', position: matchRules.length }])}>
              + Add Rule
            </button>
          </div>

          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.auto_resolve} onChange={e => setForm({ ...form, auto_resolve: e.target.checked })} />
              Auto-resolve alerts on recovery
            </label>
          </div>
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

      <Modal open={showGroupModal} onClose={() => setShowGroupModal(false)} title={editGroupId ? 'Edit Group' : 'New Group'}>
        <form onSubmit={e => { e.preventDefault(); saveGroup(); }}>
          <div class="form-group">
            <label class="required">Name</label>
            <input type="text" value={groupForm.name} onInput={e => setGroupForm({ ...groupForm, name: e.target.value })} required placeholder="Group name" />
          </div>
          <div class="form-group">
            <label>Description <span class="form-optional">(optional)</span></label>
            <textarea rows="3" value={groupForm.description}
              onInput={e => setGroupForm({ ...groupForm, description: e.target.value })}
              placeholder="Optional description" />
          </div>
          <div style="display: flex; gap: 8px;">
            <button type="submit" class="btn btn-primary">Save</button>
            {editGroupId && (
              <button type="button" class="btn btn-danger" onClick={deleteGroup}>Delete Group</button>
            )}
          </div>
        </form>
      </Modal>
    </div>
  );
}
