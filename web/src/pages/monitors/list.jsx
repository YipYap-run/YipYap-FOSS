import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch, del, put } from '../../api/client';
import { PageHeader, Card, StatusBadge, StatusDot, SearchInput, LoadingPage, ErrorMessage, EmptyState, Modal, MiniUptimeBar, useToast, ToastContainer } from '../../components/ui';
import { currentOrg, currentUser, appMeta } from '../../state/auth';
import { canWrite } from '../../state/permissions.mjs';
import { ViewOnlyNote } from '../../components/view-only-note';
import { UpgradeLink } from '../../components/upgrade-link';
import { useFormDraft } from '../../hooks/useFormDraft';

import { monitorLimits, serverMessageParts } from '../../utils/plan-limits';
import './monitors.css';

function currentLimits() {
  return monitorLimits(appMeta.value, currentOrg.value);
}

// The interval floor the form enforces. An existing monitor saved under an
// older, lower floor (for example before a downgrade) keeps its interval,
// so the form accepts that value too.
function intervalFloor(originalInterval) {
  const min = currentLimits().minInterval;
  return originalInterval > 0 && originalInterval < min ? originalInterval : min;
}

function IntervalHelp({ originalInterval }) {
  const lim = currentLimits();
  const min = lim.minInterval;
  const legacy = originalInterval > 0 && originalInterval < min
    ? <> This monitor checks every {originalInterval}s, below the current minimum. It keeps that interval until you change it.</>
    : null;
  if (lim.source === 'server') {
    return <p id="mon-interval-help" class="form-help">Minimum on this server: {min}s.{legacy}</p>;
  }
  if (!lim.plan) {
    return <p id="mon-interval-help" class="form-help">Your plan's minimum is checked when you save.{legacy}</p>;
  }
  if (lim.plan === 'free') {
    return (
      <p id="mon-interval-help" class="form-help">
        Free plan minimum: {min}s (5 min).{legacy}{' '}
        {appMeta.value?.billing_enabled && <><UpgradeLink fallback="Ask an owner or admin to upgrade for faster checks.">Upgrade</UpgradeLink> for faster checks.</>}
      </p>
    );
  }
  return <p id="mon-interval-help" class="form-help">Minimum for your plan: {min}s.{legacy}</p>;
}

// ServerMessage renders an API error as text, with links to the hosted
// product made clickable. It never uses innerHTML.
function ServerMessage({ message }) {
  return serverMessageParts(message).map((p, i) => (p.href
    ? <a key={i} href={p.href} target="_blank" rel="noopener noreferrer">{p.text}</a>
    : <span key={i}>{p.text}</span>));
}

// MonitorCapNote explains why New Monitor is unavailable when the org is at
// its monitor cap. The SaaS free plan shows an upgrade link in the header
// instead, so this covers the FOSS operator cap and plans with no upgrade.
function MonitorCapNote({ count, max, source }) {
  return (
    <p class="monitor-cap-note" id="monitor-cap-note">
      {source === 'server'
        ? <>This server allows up to {max} monitors per organization, and you have {count}. Ask your server administrator to raise the limit, or delete a monitor you no longer need.</>
        : <>Your plan allows up to {max} monitors, and you have {count}. Delete a monitor you no longer need, or contact support to raise the limit.</>}
    </p>
  );
}

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

// Maps the config path the API names in a 400 ("field") to the form key of
// the input that holds it. Host fields depend on the monitor type.
export function targetErrorKey(type, field) {
  switch (field) {
    case 'config.url': return 'http_url';
    case 'config.host': return type === 'ping' ? 'ping_host' : 'tcp_host';
    case 'config.hostname': return 'dns_hostname';
    case 'config.nameserver': return 'dns_nameserver';
    default: return '';
  }
}

// Collects the server's field errors that belong to target inputs, keyed by
// form key. Returns null when the error names none of them.
function targetErrorsFrom(type, err) {
  const fields = (err && err.body && err.body.fields) || {};
  const out = {};
  for (const [field, msg] of Object.entries(fields)) {
    const key = targetErrorKey(type, field);
    if (key) out[key] = msg;
  }
  return Object.keys(out).length ? out : null;
}

// A labelled target input with its hint and server error wired up for
// assistive tech (label for, aria-invalid, aria-describedby).
function TargetInput({ id, label, required, type = 'text', value, onInput, placeholder, error, hint, inputMode }) {
  const errId = `${id}-err`;
  const hintId = `${id}-hint`;
  const describedBy = [error ? errId : '', hint ? hintId : ''].filter(Boolean).join(' ') || undefined;
  return (
    <div class={`form-group${error ? ' has-error' : ''}`}>
      <label for={id} class={required ? 'required' : undefined}>{label}</label>
      <input id={id} type={type} value={value} onInput={onInput} required={required}
        aria-required={required ? 'true' : undefined} placeholder={placeholder}
        inputMode={inputMode} autoComplete="off" spellCheck={false} autoCapitalize="off"
        aria-invalid={error ? 'true' : undefined} aria-describedby={describedBy} />
      {error && <p id={errId} class="field-error">{error}</p>}
      {hint && <p id={hintId} class="form-help">{hint}</p>}
    </div>
  );
}

function TypeConfigFields({ form, setForm, errors = {}, clearError }) {
  const set = (key, val) => {
    setForm({ ...form, [key]: val });
    if (errors[key] && clearError) clearError(key);
  };
  // Checks run from the YipYap servers. On SaaS that means the target must
  // be public; self-hosted installs may allow private targets.
  const publicHint = appMeta.value?.edition === 'foss'
    ? ''
    : 'Checks run from YipYap servers, so the host must be reachable from the public internet.';

  switch (form.type) {
    case 'http':
      return (
        <>
          <TargetInput id="mon-http-url" label="URL" required type="url" value={form.http_url}
            onInput={e => set('http_url', e.target.value)} placeholder="https://example.com"
            error={errors.http_url} hint={publicHint} inputMode="url" />
          <div class="form-group">
            <label for="mon-http-method">Method</label>
            <select id="mon-http-method" value={form.http_method} onChange={e => set('http_method', e.target.value)}>
              {['GET', 'POST', 'PUT', 'DELETE', 'HEAD', 'PATCH'].map(m => <option key={m} value={m}>{m}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label for="mon-http-status">Expected Status</label>
            <input id="mon-http-status" type="number" value={form.http_expected_status} onInput={e => set('http_expected_status', e.target.value)} />
          </div>
          <div class="form-group">
            <label for="mon-http-body-match">Body Match <span class="form-optional">(optional)</span></label>
            <input id="mon-http-body-match" type="text" value={form.http_body_match} onInput={e => set('http_body_match', e.target.value)} placeholder="Optional substring to match in response" />
          </div>
          <div class="form-group">
            <label for="mon-http-headers">Headers (JSON) <span class="form-optional">(optional)</span></label>
            <input id="mon-http-headers" type="text" value={form.http_headers} onInput={e => set('http_headers', e.target.value)} placeholder='{"Authorization": "Bearer ..."}' />
          </div>
          <div class="form-group">
            <label for="mon-http-body">Body</label>
            <textarea id="mon-http-body" rows="3" value={form.http_body} onInput={e => set('http_body', e.target.value)} placeholder="Optional request body" />
          </div>
        </>
      );
    case 'tcp':
      return (
        <>
          <TargetInput id="mon-tcp-host" label="Host" required value={form.tcp_host}
            onInput={e => set('tcp_host', e.target.value)} placeholder="example.com"
            error={errors.tcp_host} hint={publicHint} />
          <div class="form-group">
            <label for="mon-tcp-port" class="required">Port</label>
            <input id="mon-tcp-port" type="number" min="1" max="65535" value={form.tcp_port} onInput={e => set('tcp_port', e.target.value)} required aria-required="true" placeholder="443" />
          </div>
        </>
      );
    case 'ping':
      return (
        <TargetInput id="mon-ping-host" label="Host" required value={form.ping_host}
          onInput={e => set('ping_host', e.target.value)} placeholder="example.com"
          error={errors.ping_host} hint={publicHint} />
      );
    case 'dns':
      return (
        <>
          <TargetInput id="mon-dns-hostname" label="Hostname" required value={form.dns_hostname}
            onInput={e => set('dns_hostname', e.target.value)} placeholder="example.com"
            error={errors.dns_hostname} />
          <div class="form-group">
            <label for="mon-dns-type">Record Type</label>
            <select id="mon-dns-type" value={form.dns_record_type} onChange={e => set('dns_record_type', e.target.value)}>
              {['A', 'AAAA', 'CNAME', 'MX', 'TXT', 'NS'].map(t => <option key={t} value={t}>{t}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label for="mon-dns-expected">Expected</label>
            <input id="mon-dns-expected" type="text" value={form.dns_expected} onInput={e => set('dns_expected', e.target.value)} placeholder="Optional expected value" />
          </div>
          <TargetInput id="mon-dns-nameserver" label={<>Nameserver <span class="form-optional">(optional)</span></>}
            value={form.dns_nameserver} onInput={e => set('dns_nameserver', e.target.value)}
            placeholder="e.g. 8.8.8.8" error={errors.dns_nameserver}
            hint={publicHint ? 'Leave blank to use the default resolver. A custom nameserver must be reachable from the public internet.' : ''} />
        </>
      );
    case 'heartbeat':
      return (
        <div class="form-group">
          <label for="mon-hb-grace">Grace Period (seconds)</label>
          <input id="mon-hb-grace" type="number" value={form.heartbeat_grace_period} onInput={e => set('heartbeat_grace_period', e.target.value)} />
        </div>
      );
    default:
      return null;
  }
}

function MonitorCard({ m, openEdit }) {
  // Optimistic placeholder for a monitor that was just created but is not yet
  // visible via the follower-read list. Not a link (its detail read would also
  // be stale). Replaced by the real card once the list catches up.
  if (m._optimistic) {
    return (
      <div class="monitor-card monitor-card--creating" aria-busy="true">
        <div class="monitor-card-row">
          <span class="monitor-creating-dot" title="Creating monitor" />
          <div class="monitor-card-body">
            <h3 class="monitor-card-name">{m.name || 'New monitor'}</h3>
            <p class="monitor-card-subtitle">{(m.type || '').toUpperCase()} · Creating…</p>
          </div>
          <div class="monitor-card-right">
            <span class="monitor-card-uptime" style={{ color: 'var(--color-text-muted)' }}>Creating…</span>
          </div>
        </div>
      </div>
    );
  }
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
  // Monitors created this session but not yet reflected by the list re-fetch.
  // GET /monitors uses a YugabyteDB follower read (up to 5s stale), so a freshly
  // created monitor (especially a fresh account's first) would not appear yet.
  // These render as pulsing placeholders, reconciled away once the real row lands.
  const [optimistic, setOptimistic] = useState([]);
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
  // Server-side target errors, keyed by form key (http_url, tcp_host, ...).
  const [targetErrors, setTargetErrors] = useState({});
  const formRef = useRef(null);
  // A save error that is not about one target input (an operator cap, the
  // plan interval floor, a bad value). Shown at the top of the form.
  const [formError, setFormError] = useState('');
  const formErrorRef = useRef(null);
  // The interval the edited monitor had when the form opened, so an
  // unchanged interval below today's floor is left alone on save.
  const [originalInterval, setOriginalInterval] = useState(0);
  const [matchRules, setMatchRules] = useState([]);
  const [monitorStates, setMonitorStates] = useState([]);
  const [groups, setGroups] = useState([]);
  const [groupView, setGroupView] = useState(false);
  const [showGroupModal, setShowGroupModal] = useState(false);
  const [editGroupId, setEditGroupId] = useState(null);
  const [groupForm, setGroupForm] = useState({ name: '', description: '' });
  const [collapsedGroups, setCollapsedGroups] = useState({});
  const { toasts, toast } = useToast();

  useEffect(() => {
    if (!hasDraft) return;
    const handler = e => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', handler);
    return () => window.removeEventListener('beforeunload', handler);
  }, [hasDraft]);

  async function load(silent = false) {
    if (!silent) setLoading(true);
    setError(null);
    try {
      // The list endpoint pages at 100 at most. Fetch every page so orgs
      // with more monitors see all of them, and the cap count is right.
      const list = [];
      const pageSize = 100;
      for (let offset = 0; ; offset += pageSize) {
        const data = await get(`/monitors?limit=${pageSize}&offset=${offset}`);
        const page = data?.monitors || data || [];
        list.push(...page);
        if (page.length < pageSize || offset >= 10000) break;
      }
      setMonitors(list);
      // Drop placeholders the (eventually consistent) list now includes.
      setOptimistic(prev => prev.filter(o => !list.some(m => m.id === o.id)));
    } catch (err) {
      if (!silent) setError(err);
    } finally {
      if (!silent) setLoading(false);
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

  // While placeholders exist, re-poll the list silently until the follower
  // replica catches up (staleness window is 5s), then stop. Give up after ~12s
  // so a placeholder can never pulse forever.
  useEffect(() => {
    if (optimistic.length === 0) return;
    let n = 0;
    const id = setInterval(() => {
      n += 1;
      load(true);
      if (n >= 8) { clearInterval(id); setOptimistic([]); }
    }, 1500);
    return () => clearInterval(id);
  }, [optimistic.length]);

  // Auto-open edit modal when navigated with ?edit=<id>
  useEffect(() => {
    if (!loading && monitors.length > 0 && canWrite(currentUser.value?.role)) {
      const params = new URLSearchParams(window.location.search);
      const editParam = params.get('edit');
      if (editParam) {
        const m = monitors.find(mon => mon.id === editParam);
        if (m) {
          setEditId(m.id);
          setTargetErrors({});
          setFormError('');
          setOriginalInterval(m.interval_seconds || 0);
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
    setTargetErrors({});
    setFormError('');
    setOriginalInterval(0);
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
    setTargetErrors({});
    setFormError('');
    setOriginalInterval(monitor.interval_seconds || 0);
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
    setTargetErrors({});
    setFormError('');
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
      // PATCH keeps any field the body leaves out, so send the cleared
      // values explicitly (0 / '') or a cleared field would not save.
      body.latency_warning_ms = +form.latency_warning_ms || 0;
      body.latency_critical_ms = +form.latency_critical_ms || 0;
      body.escalation_policy_id = form.escalation_policy_id || '';
      body.group_id = form.group_id || '';
      // Always send these so an empty value clears the prior selection
      // (picking "Service: None" or wiping the runbook URL must persist).
      // Earlier we only sent on truthy, which silently no-op'd at the API.
      body.runbook_url = form.runbook_url || '';
      body.service_id = form.service_id || '';

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

      // PATCH keeps omitted fields. Leave an unchanged interval out, so a
      // monitor saved under an older, lower floor can still be edited.
      if (editId && originalInterval && body.interval_seconds === originalInterval) {
        delete body.interval_seconds;
      }

      let monitorId = editId;
      if (editId) {
        await patch(`/monitors/${editId}`, body);
      } else {
        const created = await post('/monitors', body);
        const createdMon = created?.monitor || created;
        monitorId = createdMon?.id;
        if (createdMon?.id) {
          // Show it immediately. The list re-fetch reads a follower replica
          // that can lag up to 5s, so without this the new monitor would not
          // appear yet (a fresh account's first monitor leaves the page blank).
          setOptimistic(prev => [{ ...createdMon, _optimistic: true }, ...prev.filter(o => o.id !== createdMon.id)]);
        }
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
          header_match_mode: r.header_match_mode || '',
          state_id: r.state_id,
          position: idx,
        }));
        try {
          // The endpoint takes a bare array (matches the GET shape); a {rules:...}
          // wrapper fails to decode server-side.
          await put(`/monitors/${monitorId}/rules`, rulesToSave);
        } catch (err) {
          toast('Monitor saved, but its match rules did not: ' + (err.message || 'unknown error'), 'error');
        }
      } else if (monitorId && matchRules.length === 0 && editId) {
        // Clear rules if all were removed.
        try {
          await put(`/monitors/${monitorId}/rules`, []);
        } catch (_) { /* non-fatal: clearing already-empty rules */ }
      }

      clearDraft();
      closeModal();
      load(true);
    } catch (err) {
      // A refused target is shown next to its input, and focus moves there
      // so keyboard and screen reader users land on the problem.
      const fieldErrs = targetErrorsFrom(form.type, err);
      // Anything else (an operator cap, the interval floor, a bad value)
      // goes at the top of the form, where it stays until the next save.
      if (fieldErrs) {
        setTargetErrors(fieldErrs);
      } else if (err.readonly) {
        // The API client already showed the read-only toast.
      } else {
        setFormError(err.message || 'Could not save the monitor. Try again.');
      }
    } finally {
      setSaving(false);
    }
  }

  useEffect(() => {
    if (!formRef.current || Object.keys(targetErrors).length === 0) return;
    const el = formRef.current.querySelector('[aria-invalid="true"]');
    if (el && el.focus) el.focus();
  }, [targetErrors]);

  // Move focus to a form-level error so keyboard and screen reader users
  // hear it without hunting for it.
  useEffect(() => {
    if (formError && formErrorRef.current) formErrorRef.current.focus();
  }, [formError]);

  async function handleDelete() {
    if (!editId) return;
    if (!confirm('Are you sure you want to delete this monitor?')) return;
    setSaving(true);
    try {
      await del(`/monitors/${editId}`);
      closeModal();
      load();
    } catch (err) {
      toast(err.message || 'Failed to delete monitor', 'error');
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
    if (!groupForm.name) { toast('Name is required', 'error'); return; }
    try {
      if (editGroupId) {
        await patch(`/monitor-groups/${editGroupId}`, groupForm);
      } else {
        await post('/monitor-groups', groupForm);
      }
      setShowGroupModal(false);
      loadGroups();
    } catch (err) {
      toast(err.message || 'Failed to save group', 'error');
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
      toast(err.message || 'Failed to delete group', 'error');
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

  // Show optimistic placeholders (created this session, not yet in the
  // follower-read list) ahead of the fetched monitors.
  const displayMonitors = optimistic.length
    ? [...optimistic.filter(o => !monitors.some(m => m.id === o.id)), ...monitors]
    : monitors;

  const types = [...new Set(displayMonitors.map(m => m.type))].sort();
  // Collect all unique label keys from monitors for the tag filter bar.
  const allTags = [...new Set(displayMonitors.flatMap(m => {
    if (!m.labels) return [];
    return Object.entries(m.labels).map(([k, v]) => v ? `${k}:${v}` : k);
  }))].sort();
  const filtered = displayMonitors.filter(m => {
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

  // Viewers are read-only: no create or edit controls.
  const writer = canWrite(currentUser.value?.role);
  // Monitor cap: the plan limit on SaaS, the operator setting in FOSS.
  // The server enforces it; this keeps people from filling in a form that
  // can only be refused.
  const limits = currentLimits();
  const monitorCount = displayMonitors.length;
  const atCap = limits.maxMonitors > 0 && monitorCount >= limits.maxMonitors;
  const canUpgrade = atCap && limits.source === 'plan' && appMeta.value?.billing_enabled;
  const countLabel = `${monitorCount} ${monitorCount === 1 ? 'monitor' : 'monitors'}`;

  return (
    <div class="monitors-page">
      <ToastContainer toasts={toasts} />
      <PageHeader title="Monitors"
        subtitle={limits.maxMonitors > 0 ? `${monitorCount} of ${limits.maxMonitors} monitors` : countLabel}
        actions={
          !writer ? null
          : <div class="monitors-actions">
              <button class="btn" data-readonly-block onClick={openGroupCreate}>New Group</button>
              {canUpgrade
                ? <UpgradeLink class="btn btn-primary" fallback="Monitor limit reached. Ask an owner or admin to upgrade.">Upgrade to add more monitors</UpgradeLink>
                : <button class="btn btn-primary" data-readonly-block onClick={openCreate}
                          disabled={atCap} aria-describedby={atCap ? 'monitor-cap-note' : undefined}>
                    New Monitor
                  </button>}
            </div>
        } />
      {atCap && !canUpgrade && <MonitorCapNote count={monitorCount} max={limits.maxMonitors} source={limits.source} />}
      <ViewOnlyNote />

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
          description={search || typeFilter ? 'Try adjusting your filters' : writer ? 'Create your first monitor to get started' : 'No monitors yet. Owners, admins and members can add them.'} />
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
                  {writer && <button class="btn btn-xs" aria-label={`Edit group ${g.name}`} onClick={e => { e.stopPropagation(); openGroupEdit(g); }}>Edit</button>}
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
        <form ref={formRef} onSubmit={e => { e.preventDefault(); save(); }}>
          {formError && (
            <div class="form-error" role="alert" tabIndex={-1} ref={formErrorRef}>
              <ServerMessage message={formError} />
            </div>
          )}
          <div class="form-group">
            <label for="mon-name" class="required">Name</label>
            <input id="mon-name" type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required placeholder="My Monitor" />
          </div>
          <div class="form-group">
            <label for="mon-type" class="required">Type</label>
            <select id="mon-type" value={form.type} disabled={!!editId} aria-describedby={editId ? 'mon-type-help' : undefined}
                    onChange={e => { setTargetErrors({}); setForm({ ...form, type: e.target.value }); }}>
              <option value="http">HTTP</option>
              <option value="tcp">TCP</option>
              <option value="ping">Ping</option>
              <option value="dns">DNS</option>
              <option value="heartbeat">Heartbeat</option>
            </select>
            {editId && <p id="mon-type-help" class="form-help">The type of an existing monitor cannot be changed. Create a new monitor instead.</p>}
          </div>

          <TypeConfigFields form={form} setForm={setForm} errors={targetErrors}
            clearError={key => setTargetErrors(prev => { const next = { ...prev }; delete next[key]; return next; })} />

          <div class="form-group">
            <label for="mon-interval">Interval (seconds)</label>
            <input id="mon-interval" type="number" value={form.interval_seconds}
                   min={intervalFloor(editId ? originalInterval : 0)} step="1" required
                   aria-describedby="mon-interval-help"
                   onInput={e => setForm({ ...form, interval_seconds: e.target.value })} />
            <IntervalHelp originalInterval={editId ? originalInterval : 0} />
          </div>
          {/* Heartbeat is push-based: no outbound request, latency is always 0,
              and it never reports degraded. Timeout and latency thresholds don't apply. */}
          {form.type !== 'heartbeat' && (
            <>
              <div class="form-group">
                <label for="mon-timeout">Timeout (seconds)</label>
                <input id="mon-timeout" type="number" min="1" value={form.timeout_seconds} onInput={e => setForm({ ...form, timeout_seconds: e.target.value })} />
              </div>
              <div class="form-group">
                <label for="mon-latency-warning">Latency Warning (ms)</label>
                <input id="mon-latency-warning" type="number" min="0" value={form.latency_warning_ms} onInput={e => setForm({ ...form, latency_warning_ms: e.target.value })} placeholder="e.g. 500" />
              </div>
              <div class="form-group">
                <label for="mon-latency-critical">Latency Critical (ms)</label>
                <input id="mon-latency-critical" type="number" min="0" value={form.latency_critical_ms} onInput={e => setForm({ ...form, latency_critical_ms: e.target.value })} placeholder="e.g. 2000" />
              </div>
            </>
          )}
          <div class="form-group">
            <label for="mon-down-severity">Down Severity</label>
            <select id="mon-down-severity" value={form.down_severity} onChange={e => setForm({ ...form, down_severity: e.target.value })}>
              <option value="critical">Critical</option>
              <option value="warning">Warning</option>
              <option value="info">Info</option>
            </select>
          </div>
          {form.type !== 'heartbeat' && (
            <div class="form-group">
              <label for="mon-degraded-severity">Degraded Severity</label>
              <select id="mon-degraded-severity" value={form.degraded_severity} onChange={e => setForm({ ...form, degraded_severity: e.target.value })}>
                <option value="critical">Critical</option>
                <option value="warning">Warning</option>
                <option value="info">Info</option>
              </select>
            </div>
          )}
          <div class="form-group">
            <label htmlFor="mon-escalation-policy">Escalation Policy <span class="form-optional">(optional)</span></label>
            <select id="mon-escalation-policy" aria-describedby="mon-escalation-policy-help"
                    value={form.escalation_policy_id} onChange={e => setForm({ ...form, escalation_policy_id: e.target.value })}>
              <option value="">None</option>
              {policies.map(p => <option key={p.id} value={p.id}>{p.name}</option>)}
            </select>
            <p id="mon-escalation-policy-help" class="form-help">
              An alert opens as soon as a check fails, including the first check after you save.
            </p>
          </div>
          <div class="form-group">
            <label for="mon-group">Group <span class="form-optional">(optional)</span></label>
            <select id="mon-group" value={form.group_id} onChange={e => setForm({ ...form, group_id: e.target.value })}>
              <option value="">None</option>
              {groups.map(g => <option key={g.id} value={g.id}>{g.name}</option>)}
            </select>
          </div>
          <div class="form-group">
            <label for="mon-runbook">Runbook URL <span class="form-optional">(optional)</span></label>
            <input id="mon-runbook" type="url" value={form.runbook_url}
                   onInput={e => setForm({ ...form, runbook_url: e.target.value })}
                   placeholder="https://wiki.example.com/runbooks/..." />
          </div>
          {appMeta.value?.edition !== 'foss' && (
            <div class="form-group">
              <label for="mon-service">Service <span class="form-optional">(optional)</span></label>
              <select id="mon-service" value={form.service_id}
                      onChange={e => setForm({ ...form, service_id: e.target.value })}>
                <option value="">None</option>
                {services.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
              </select>
            </div>
          )}
          <div class="form-group">
            <label for="mon-description">Description <span class="form-optional">(optional)</span></label>
            <textarea id="mon-description" rows="3" value={form.description}
              onInput={e => setForm({ ...form, description: e.target.value })}
              placeholder="Optional description for this monitor" />
          </div>
          <div class="form-group">
            <label for="mon-tags">Tags <span class="form-optional">(optional)</span></label>
            <input id="mon-tags" type="text" aria-describedby="mon-tags-help" value={form.tags}
              onInput={e => setForm({ ...form, tags: e.target.value })}
              placeholder="env:prod, team:backend, region:us-east" />
            <p id="mon-tags-help" class="form-help">Comma-separated tags. Use key:value format for labeled tags.</p>
          </div>
          {/* Match rules evaluate HTTP response status/body/headers; the scheduler
              only applies them when a check produced a status code (HTTP only). */}
          {form.type === 'http' && (
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
                <div class="match-rule-grid match-rule-grid--3">
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-status-code`} style="font-size: 0.75rem;">Status Code</label>
                    <input id={`mr-${idx}-status-code`} type="number" placeholder="e.g. 200"
                      value={rule.status_code || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-body-match`} style="font-size: 0.75rem;">Body Match</label>
                    <input id={`mr-${idx}-body-match`} type="text" placeholder="e.g. ok"
                      value={rule.body_match || ''}
                      onInput={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, body_match: e.target.value } : r))} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-state`} style="font-size: 0.75rem;">Assign State</label>
                    <select id={`mr-${idx}-state`} value={rule.state_id || ''}
                      onChange={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, state_id: e.target.value } : r))}>
                      <option value="">-- select --</option>
                      {monitorStates.map(s => <option key={s.id} value={s.id}>{s.name}</option>)}
                    </select>
                  </div>
                </div>
                <div class="match-rule-grid match-rule-grid--4">
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-status-min`} style="font-size: 0.75rem;">Status Min</label>
                    <input id={`mr-${idx}-status-min`} type="number" placeholder="e.g. 200"
                      value={rule.status_code_min || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code_min: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-status-max`} style="font-size: 0.75rem;">Status Max</label>
                    <input id={`mr-${idx}-status-max`} type="number" placeholder="e.g. 299"
                      value={rule.status_code_max || ''}
                      onInput={e => {
                        const v = e.target.value ? +e.target.value : null;
                        setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, status_code_max: v } : r));
                      }} />
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-body-mode`} style="font-size: 0.75rem;">Body Mode</label>
                    <select id={`mr-${idx}-body-mode`} value={rule.body_match_mode || 'contains'}
                      onChange={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, body_match_mode: e.target.value } : r))}>
                      <option value="contains">Contains</option>
                      <option value="not_contains">Not Contains</option>
                      <option value="regex">Regex</option>
                    </select>
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-header-name`} style="font-size: 0.75rem;">Header Name</label>
                    <input id={`mr-${idx}-header-name`} type="text" placeholder="e.g. Location"
                      value={rule.header_match || ''}
                      onInput={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, header_match: e.target.value } : r))} />
                  </div>
                </div>
                <div class="match-rule-grid match-rule-grid--wide">
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-header-match`} style="font-size: 0.75rem;">Header Match</label>
                    <select id={`mr-${idx}-header-match`} value={rule.header_match_mode || 'equals'}
                      onChange={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, header_match_mode: e.target.value } : r))}>
                      <option value="equals">Equals</option>
                      <option value="contains">Contains</option>
                      <option value="regex">Regex</option>
                    </select>
                  </div>
                  <div class="form-group" style="margin-bottom: 0;">
                    <label for={`mr-${idx}-header-value`} style="font-size: 0.75rem;">Header Value <span class="form-optional">(blank = header present)</span></label>
                    <input id={`mr-${idx}-header-value`} type="text" placeholder="e.g. https://example.com/login"
                      value={rule.header_value || ''}
                      onInput={e => setMatchRules(matchRules.map((r, i) => i === idx ? { ...r, header_value: e.target.value } : r))} />
                  </div>
                </div>
                <div style="margin-top: 8px; display: flex; justify-content: flex-end;">
                  <button type="button" class="btn btn-xs btn-danger" aria-label={`Remove rule ${idx + 1}`}
                          onClick={() => setMatchRules(matchRules.filter((_, i) => i !== idx))}>
                    Remove
                  </button>
                </div>
              </div>
            ))}
            <button type="button" class="btn btn-sm" onClick={() => setMatchRules([...matchRules, { status_code: null, body_match: '', body_match_mode: 'contains', state_id: '', position: matchRules.length }])}>
              + Add Rule
            </button>
          </div>
          )}

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
            <label for="group-name" class="required">Name</label>
            <input id="group-name" type="text" value={groupForm.name} onInput={e => setGroupForm({ ...groupForm, name: e.target.value })} required placeholder="Group name" />
          </div>
          <div class="form-group">
            <label for="group-description">Description <span class="form-optional">(optional)</span></label>
            <textarea id="group-description" rows="3" value={groupForm.description}
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
