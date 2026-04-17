import { useState, useEffect, useCallback } from 'preact/hooks';
import { get, post, patch, del, put } from '../../api/client';
import { PageHeader, Card, Tabs, Modal, LoadingPage, ErrorMessage, formatTime, useToast, ToastContainer } from '../../components/ui';
import { currentOrg, currentUser, appMeta } from '../../state/auth';
import { route } from 'preact-router';
import { MFATab } from './mfa.jsx';
import { AccountTab } from './account.jsx';
import { IntegrationsTab } from './integrations';
import { MonitorStatesTab } from './monitor-states.jsx';
import { ImportTab } from './import.jsx';

const SETTING_TABS = [
  { key: 'account', label: 'Account' },
  { key: 'channels', label: 'Channels' },
  { key: 'escalation', label: 'Escalation' },
  { key: 'integrations', label: 'Integrations' },
  { key: 'teams', label: 'Teams' },
  { key: 'monitor-states', label: 'Monitor States' },
  { key: 'maintenance', label: 'Maintenance' },
  { key: 'status-page', label: 'Status Pages' },
  { key: 'api-keys', label: 'API Keys' },
  { key: 'sso', label: 'SSO' },
  { key: 'members', label: 'Members' },
  { key: 'billing', label: 'Billing' },
  { key: 'security', label: 'Security' },
  { key: 'import', label: 'Import' },
  { key: 'data', label: 'Data' },
  { key: 'org', label: 'Organization' },
];

export function SettingsPage({ tab: initialTab }) {
  const [tab, setTab] = useState(initialTab || 'channels');

  function handleTabChange(t) {
    setTab(t);
    route(`/settings/${t}`, true);
  }

  return (
    <div class="settings-page">
      <PageHeader title="Settings" />
      <Tabs tabs={SETTING_TABS.filter(t => {
        const isFoss = appMeta.value?.edition === 'foss';
        if (t.key === 'billing' && (isFoss || !appMeta.value?.billing_enabled)) return false;
        if (isFoss && ['teams', 'status-page', 'sso', 'integrations'].includes(t.key)) return false;
        if (t.key === 'security' && isFoss) return false;
        return true;
      })} active={tab} onChange={handleTabChange} />
      <div class="settings-content">
        {tab === 'account' && <AccountTab />}
        {tab === 'channels' && <ChannelsTab />}
        {tab === 'escalation' && <EscalationTab />}
        {tab === 'integrations' && <IntegrationsTab />}
        {tab === 'teams' && <TeamsTab />}
        {tab === 'monitor-states' && <MonitorStatesTab />}
        {tab === 'maintenance' && <MaintenanceTab />}
        {tab === 'status-page' && <StatusPageTab />}
        {tab === 'api-keys' && <APIKeysTab />}
        {tab === 'sso' && <SSOTab />}
        {tab === 'members' && <MembersTab />}
        {tab === 'billing' && <BillingTab />}
        {tab === 'security' && <MFATab />}
        {tab === 'import' && <ImportTab />}
        {tab === 'data' && <DataTab />}
        {tab === 'org' && <OrgTab />}
      </div>
    </div>
  );
}

/* ─── Notification Channels ─── */

const CHANNEL_TYPES = {
  webhook: { label: 'Webhook', fields: [
    { key: 'url', label: 'URL', type: 'url', placeholder: 'https://hooks.example.com/alerts', required: true },
    { key: 'format', label: 'Payload Format', type: 'webhook_format' },
    { key: 'headers', label: 'Custom Headers (JSON)', type: 'json', placeholder: '{"X-Api-Key": "..."}' },
  ]},
  slack:        { label: 'Slack', fields: [
    { key: 'bot_token', label: 'Bot Token', type: 'secret', placeholder: 'xoxb-...', required: true, help: 'OAuth & Permissions \u2192 Bot User OAuth Token' },
    { key: 'channel_id', label: 'Channel ID', type: 'text', placeholder: 'C01234ABC', required: true, help: 'Right-click channel \u2192 View channel details \u2192 Copy ID' },
  ]},
  discord:      { label: 'Discord', fields: [
    { key: 'bot_token', label: 'Bot Token', type: 'secret', placeholder: 'MTIz...', required: true, help: 'Developer Portal \u2192 Bot \u2192 Token' },
    { key: 'channel_id', label: 'Channel ID', type: 'text', placeholder: '1234567890', required: true, help: 'Enable Developer Mode \u2192 Right-click channel \u2192 Copy ID' },
  ]},
  telegram:     { label: 'Telegram', fields: [
    { key: 'bot_token', label: 'Bot Token', type: 'secret', placeholder: '123456:ABC-DEF...', required: true, help: 'Message @BotFather \u2192 /newbot' },
    { key: 'chat_id', label: 'Chat ID', type: 'text', placeholder: '-100123456789', required: true, help: 'Add bot to group, send message, check /getUpdates' },
  ]},
  smtp:         { label: 'Email (SMTP)', fields: [
    { key: 'host', label: 'SMTP Host', type: 'text', placeholder: 'smtp.gmail.com', required: true },
    { key: 'port', label: 'Port', type: 'number', placeholder: '587', required: true },
    { key: 'username', label: 'Username', type: 'text', placeholder: 'alerts@company.com' },
    { key: 'password', label: 'Password', type: 'secret', placeholder: 'App password or SMTP password' },
    { key: 'from', label: 'From Address', type: 'text', placeholder: 'alerts@company.com', required: true },
    { key: 'to', label: 'To Address', type: 'text', placeholder: 'oncall@company.com', required: true },
  ]},
  twilio_sms:   { label: 'SMS', fields: [
    { key: 'to', label: 'Phone Number', type: 'text', placeholder: '+1 212-555-4240', required: true, help: 'US and Canada numbers only (+1)' },
  ]},
  twilio_voice: { label: 'Voice Call', fields: [
    { key: 'to', label: 'Phone Number', type: 'text', placeholder: '+1 212-555-4240', required: true, help: 'US and Canada numbers only (+1)' },
  ]},
  ntfy: { label: 'ntfy.sh', fields: [
    { key: 'server_url', label: 'Server URL', type: 'url', placeholder: 'https://ntfy.sh', help: 'Leave blank for the public ntfy.sh server' },
    { key: 'topic', label: 'Topic', type: 'text', placeholder: 'yipyap-alerts', required: true },
    { key: 'token', label: 'Auth Token', type: 'secret', placeholder: 'Optional token for private topics' },
  ]},
  pushover: { label: 'Pushover', fields: [
    { key: 'api_token', label: 'API Token', type: 'secret', placeholder: 'Your Pushover application token', required: true },
    { key: 'user_key', label: 'User/Group Key', type: 'text', placeholder: 'Your Pushover user or group key', required: true },
    { key: 'device', label: 'Device', type: 'text', placeholder: 'Optional specific device name' },
    { key: 'sound', label: 'Sound', type: 'text', placeholder: 'Optional notification sound' },
  ]},
};

const WEBHOOK_FORMATS = {
  generic:     { label: 'Generic (JSON)',   auto: () => false },
  slack:       { label: 'Slack',            auto: url => /hooks\.slack\.com/.test(url) },
  discord:     { label: 'Discord',          auto: url => /discord(app)?\.com\/api\/webhooks\//.test(url) },
  teams:       { label: 'Microsoft Teams',  auto: url => /webhook\.office\.com|\.logic\.azure\.com/.test(url) },
  google_chat: { label: 'Google Chat',      auto: url => /chat\.googleapis\.com/.test(url) },
  lark:        { label: 'Lark / Feishu',    auto: url => /open\.(feishu\.cn|larksuite\.com)/.test(url) },
  dingtalk:    { label: 'DingTalk',         auto: url => /oapi\.dingtalk\.com/.test(url) },
  synology:    { label: 'Synology Chat',    auto: url => /SYNO\.Chat\.External/.test(url) },
};

function detectWebhookFormat(url) {
  if (!url) return 'generic';
  for (const [key, def] of Object.entries(WEBHOOK_FORMATS)) {
    if (def.auto(url)) return key;
  }
  return 'generic';
}

function truncate(s, n) { return s && s.length > n ? s.slice(0, n) + '\u2026' : s || ''; }

function getConfigSummary(type, config) {
  if (!config || typeof config !== 'object') return '';
  switch (type) {
    case 'webhook': return config.url ? truncate(config.url, 40) : '';
    case 'slack': return config.channel_id ? 'Channel: ' + config.channel_id : '';
    case 'discord': return config.channel_id ? 'Channel: ' + config.channel_id : '';
    case 'telegram': return config.chat_id ? 'Chat: ' + config.chat_id : '';
    case 'smtp': return config.host ? config.host + ' \u2192 ' + (config.to || '') : '';
    case 'twilio_sms': return config.to || '';
    case 'twilio_voice': return config.to || '';
    default: return '';
  }
}

function SecretInput({ value, onInput, placeholder }) {
  const [show, setShow] = useState(false);
  return (
    <div class="secret-field">
      <input type={show ? 'text' : 'password'} value={value} onInput={onInput}
        placeholder={placeholder} autocomplete="off" />
      <button type="button" class="secret-toggle" onClick={() => setShow(!show)}
        title={show ? 'Hide' : 'Show'}>
        {show ? 'Hide' : 'Show'}
      </button>
    </div>
  );
}

function ChannelsTab() {
  const [channels, setChannels] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [name, setName] = useState('');
  const [type, setType] = useState('webhook');
  const [config, setConfig] = useState({});
  const [enabled, setEnabled] = useState(true);
  const [errors, setErrors] = useState({});
  const [testing, setTesting] = useState(null); // channel id being tested
  const [testResult, setTestResult] = useState({}); // { [id]: 'ok' | 'fail' }
  const { toasts, toast } = useToast();

  async function load() {
    setLoading(true);
    try {
      const data = await get('/notification-channels');
      setChannels(data.channels || data || []);
    } catch (e) {
      toast('Failed to load channels', 'error');
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  function resetForm() {
    setName(''); setType('webhook'); setConfig({}); setEnabled(true);
    setEditId(null); setErrors({});
  }

  function openCreate() {
    resetForm();
    setShowModal(true);
  }

  function openEdit(ch) {
    setEditId(ch.id);
    setName(ch.name);
    setType(ch.type);
    const cfg = ch.config && typeof ch.config === 'object' ? { ...ch.config } : {};
    // Stringify object fields for textarea editing
    if (cfg.headers && typeof cfg.headers === 'object') {
      cfg.headers = JSON.stringify(cfg.headers, null, 2);
    }
    setConfig({ ...cfg, _formatManual: !!ch.config?.format });
    setEnabled(ch.enabled !== false);
    setErrors({});
    setShowModal(true);
  }

  function validate() {
    const errs = {};
    if (!name.trim()) errs.name = 'Required';
    const typeDef = CHANNEL_TYPES[type];
    if (typeDef) {
      for (const f of typeDef.fields) {
        if (f.required && !config[f.key]?.toString().trim()) {
          errs[f.key] = 'Required';
        }
        if (f.type === 'url' && config[f.key] && !/^https?:\/\/.+/.test(config[f.key])) {
          errs[f.key] = 'Must be a valid URL';
        }
      }
    }
    setErrors(errs);
    return Object.keys(errs).length === 0;
  }

  async function save() {
    if (!validate()) return;
    try {
      // Parse any JSON string fields back to objects for the API
      const cfgToSend = { ...config };
      if (type === 'webhook' && typeof cfgToSend.headers === 'string') {
        try { cfgToSend.headers = JSON.parse(cfgToSend.headers || '{}'); }
        catch (_) { cfgToSend.headers = {}; }
      }
      delete cfgToSend._formatManual;
      const body = { name, type, config: cfgToSend, enabled };
      if (editId) {
        await patch('/notification-channels/' + editId, body);
        toast('Channel updated');
      } else {
        await post('/notification-channels', body);
        toast('Channel created');
      }
      setShowModal(false);
      resetForm();
      load();
    } catch (e) {
      toast(e?.message || 'Failed to save channel', 'error');
    }
  }

  async function testChannel(id) {
    setTesting(id);
    setTestResult(r => ({ ...r, [id]: null }));
    try {
      await post('/notification-channels/' + id + '/test', {});
      setTestResult(r => ({ ...r, [id]: 'ok' }));
      toast('Test notification sent');
    } catch (e) {
      setTestResult(r => ({ ...r, [id]: 'fail' }));
      toast(e?.message || 'Test failed', 'error');
    }
    setTesting(null);
    setTimeout(() => setTestResult(r => ({ ...r, [id]: null })), 3000);
  }

  async function deleteChannel(id) {
    if (!confirm('Delete this channel?')) return;
    try {
      await del('/notification-channels/' + id);
      toast('Channel deleted');
      load();
    } catch (e) {
      toast(e?.message || 'Failed to delete', 'error');
    }
  }

  function updateConfig(key, value) {
    setConfig(c => {
      const next = { ...c, [key]: value };
      if (key === 'url' && type === 'webhook' && !c._formatManual) {
        next.format = detectWebhookFormat(value);
      }
      return next;
    });
  }

  function handleTypeChange(newType) {
    setType(newType);
    // Keep config values that overlap between types, clear the rest
    const newFields = (CHANNEL_TYPES[newType]?.fields || []).map(f => f.key);
    setConfig(c => {
      const next = {};
      for (const k of newFields) {
        if (c[k] !== undefined) next[k] = c[k];
      }
      return next;
    });
    setErrors({});
  }

  const typeDef = CHANNEL_TYPES[type];

  if (loading) return <LoadingPage />;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Notification Channels</h3>
        <button class="btn btn-sm btn-primary" onClick={openCreate}>Add Channel</button>
      </div>
      <div class="settings-list">
        {channels.map(ch => {
          const summary = getConfigSummary(ch.type, ch.config);
          const tState = testResult[ch.id];
          const isTesting = testing === ch.id;
          const testBtnClass = tState === 'ok' ? 'btn btn-xs btn-test-ok'
            : tState === 'fail' ? 'btn btn-xs btn-test-fail'
            : 'btn btn-xs';
          return (
            <div key={ch.id} class="settings-item" style="cursor: pointer" onClick={() => openEdit(ch)}>
              <div class="channel-info">
                <div class="channel-header">
                  <span class={'channel-dot ' + (ch.enabled ? 'channel-dot-on' : 'channel-dot-off')} />
                  <strong>{ch.name}</strong>
                  <span class="label-tag">{(CHANNEL_TYPES[ch.type]?.label) || ch.type}</span>
                </div>
                {summary && <span class="channel-summary">{summary}</span>}
              </div>
              <div class="btn-group">
                <button class={testBtnClass} onClick={e => { e.stopPropagation(); testChannel(ch.id); }} disabled={isTesting}>
                  {isTesting ? 'Testing\u2026' : tState === 'ok' ? '\u2713 Sent' : tState === 'fail' ? '\u2717 Failed' : 'Test'}
                </button>
              </div>
            </div>
          );
        })}
        {channels.length === 0 && (
          <div style="text-align: center; padding: 2rem; color: var(--color-text-muted);">
            <p style="margin-bottom: 1rem;">No notification channels configured yet.</p>
            <p style="margin-bottom: 1rem; font-size: 0.875rem;">
              Set up a channel to start receiving alerts when your monitors detect issues.
            </p>
            <button class="btn btn-primary" onClick={openCreate}>
              Create Your First Channel
            </button>
          </div>
        )}
      </div>

      <Modal open={showModal} onClose={() => { setShowModal(false); resetForm(); }} title={editId ? 'Edit Channel' : 'Add Channel'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class={'form-group' + (errors.name ? ' has-error' : '')}>
            <label class="required">Name</label>
            <input type="text" value={name} onInput={e => setName(e.target.value)} placeholder="e.g. Ops Slack" />
            {errors.name && <div class="field-error">{errors.name}</div>}
          </div>
          <div class="form-group">
            <label class="required">Type</label>
            <select value={type} onChange={e => handleTypeChange(e.target.value)}>
              {Object.entries(CHANNEL_TYPES)
                .filter(([k]) => appMeta.value?.edition !== 'foss' || !['twilio_sms', 'twilio_voice'].includes(k))
                .map(([k, v]) => (
                <option key={k} value={k}>{v.label}</option>
              ))}
            </select>
          </div>

          {typeDef && typeDef.fields.map(f => (
            <div key={f.key} class={'form-group' + (errors[f.key] ? ' has-error' : '')}>
              <label>{f.label}</label>
              {f.type === 'secret' ? (
                <SecretInput value={config[f.key] || ''} placeholder={f.placeholder}
                  onInput={e => updateConfig(f.key, e.target.value)} />
              ) : f.type === 'json' ? (
                <textarea rows="2" value={config[f.key] || ''} placeholder={f.placeholder}
                  onInput={e => updateConfig(f.key, e.target.value)} />
              ) : f.type === 'number' ? (
                <input type="number" value={config[f.key] || ''} placeholder={f.placeholder}
                  onInput={e => updateConfig(f.key, e.target.value ? +e.target.value : 0)} />
              ) : f.type === 'webhook_format' ? (
                <select value={config.format || 'generic'} onChange={e => {
                  setConfig(c => ({ ...c, format: e.target.value, _formatManual: true }));
                }}>
                  {Object.entries(WEBHOOK_FORMATS).map(([k, v]) => (
                    <option key={k} value={k}>{v.label}</option>
                  ))}
                </select>
              ) : (
                <input type={f.type === 'url' ? 'url' : 'text'} value={config[f.key] || ''} placeholder={f.placeholder}
                  onInput={e => updateConfig(f.key, e.target.value)} />
              )}
              {f.help && !errors[f.key] && <div class="form-help">{f.help}</div>}
              {errors[f.key] && <div class="field-error">{errors[f.key]}</div>}
            </div>
          ))}

          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} />
              Enabled
            </label>
          </div>
          <div style="display: flex; justify-content: space-between; align-items: center; margin-top: 8px">
            <button type="submit" class="btn btn-primary">{editId ? 'Update' : 'Create'}</button>
            {editId && (
              <button type="button" class="btn btn-danger btn-sm" onClick={() => { deleteChannel(editId); setShowModal(false); resetForm(); }}>
                Delete Channel
              </button>
            )}
          </div>
        </form>
      </Modal>
    </div>
  );
}

/* ─── Escalation Policies ─── */
function EscalationTab() {
  const [policies, setPolicies] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ name: '', description: '', loop: false, max_loops: '' });
  const [editId, setEditId] = useState(null);
  const [steps, setSteps] = useState([]);
  const [editingSteps, setEditingSteps] = useState(null);

  const [channels, setChannels] = useState([]);
  const [users, setUsers] = useState([]);
  const [teams, setTeams] = useState([]);
  const defaultTarget = () => ({ target_type: 'on_call_primary', target_id: '', channel_id: '', simultaneous: true });

  async function load() {
    setLoading(true);
    try {
      const data = await get('/escalation-policies');
      setPolicies(data.policies || data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  useEffect(() => {
    async function loadChannels() {
      try {
        const data = await get('/notification-channels');
        setChannels(data.channels || data || []);
      } catch (_) {}
    }
    loadChannels();
    get('/users').then(d => setUsers(d.users || d || [])).catch(() => {});
    get('/teams').then(d => setTeams(d.teams || d || [])).catch(() => {});
  }, []);

  async function save() {
    const body = {
      name: form.name,
      description: form.description,
      loop: form.loop,
      max_loops: form.loop && form.max_loops !== '' ? +form.max_loops : null,
    };
    if (editId) await patch(`/escalation-policies/${editId}`, body);
    else await post('/escalation-policies', body);
    setShowModal(false);
    setEditId(null);
    load();
  }

  async function deletePolicy(id) {
    if (confirm('Delete this policy?')) {
      await del(`/escalation-policies/${id}`);
      load();
    }
  }

  async function openSteps(policy) {
    setEditingSteps(policy.id);
    try {
      const detail = await get(`/escalation-policies/${policy.id}`);
      const loaded = (detail.steps || []).map(s => ({
        delay_minutes: s.delay_minutes || 0,
        repeat_count: s.repeat_count || 0,
        repeat_interval_seconds: s.repeat_interval_seconds || 0,
        is_terminal: s.is_terminal || false,
        targets: (s.targets || []).map(t => ({
          target_type: t.target_type || 'on_call_primary',
          target_id: t.target_id || '',
          channel_id: t.channel_id || '',
          simultaneous: t.simultaneous || false,
        })),
      }));
      setSteps(loaded);
    } catch (_) {
      setSteps([]);
    }
  }

  async function saveSteps() {
    await put(`/escalation-policies/${editingSteps}/steps`, { steps });
    setEditingSteps(null);
    load();
  }

  function addStep() {
    setSteps([...steps, {
      delay_minutes: 5,
      repeat_count: 0,
      repeat_interval_seconds: 0,
      is_terminal: false,
      targets: [defaultTarget()],
    }]);
  }

  function updateStep(i, patch) {
    const s = [...steps];
    s[i] = { ...s[i], ...patch };
    setSteps(s);
  }

  function updateTarget(stepIdx, targetIdx, patch) {
    const s = [...steps];
    const targets = [...s[stepIdx].targets];
    targets[targetIdx] = { ...targets[targetIdx], ...patch };
    s[stepIdx] = { ...s[stepIdx], targets };
    setSteps(s);
  }

  function addTarget(stepIdx) {
    const s = [...steps];
    s[stepIdx] = { ...s[stepIdx], targets: [...s[stepIdx].targets, defaultTarget()] };
    setSteps(s);
  }

  function removeTarget(stepIdx, targetIdx) {
    const s = [...steps];
    s[stepIdx] = { ...s[stepIdx], targets: s[stepIdx].targets.filter((_, j) => j !== targetIdx) };
    setSteps(s);
  }

  const needsTargetId = (type) => type === 'user' || type === 'team';

  if (loading) return <LoadingPage />;

  return (
    <div>
      <div class="section-header">
        <h3>Escalation Policies</h3>
        <button class="btn btn-sm btn-primary" onClick={() => {
          setEditId(null); setForm({ name: '', description: '', loop: false, max_loops: '' }); setShowModal(true);
        }}>Add Policy</button>
      </div>
      <div class="settings-list">
        {policies.map(p => (
          <div key={p.id} class="settings-item">
            <div>
              <strong>{p.name}</strong>
              {p.description && <span class="text-muted"> - {p.description}</span>}
              {p.loop && <span class="label-tag">Loop{p.max_loops != null ? ` (max ${p.max_loops})` : ''}</span>}
            </div>
            <div class="btn-group">
              <button class="btn btn-xs" onClick={() => openSteps(p)}>Steps</button>
              <button class="btn btn-xs" onClick={() => {
                setEditId(p.id);
                setForm({
                  name: p.name,
                  description: p.description || '',
                  loop: p.loop || false,
                  max_loops: p.max_loops != null ? String(p.max_loops) : '',
                });
                setShowModal(true);
              }}>Edit</button>
              <button class="btn btn-xs btn-danger" onClick={() => deletePolicy(p.id)}>Delete</button>
            </div>
          </div>
        ))}
        {policies.length === 0 && <p class="text-muted">No escalation policies</p>}
      </div>

      <Modal open={showModal} onClose={() => setShowModal(false)} title={editId ? 'Edit Policy' : 'Add Policy'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label class="required">Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Description <span class="form-optional">(optional)</span></label>
            <textarea value={form.description} onInput={e => setForm({ ...form, description: e.target.value })} />
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.loop}
                onChange={e => setForm({ ...form, loop: e.target.checked })} />
              After the last step, restart from step 1
            </label>
          </div>
          {form.loop && (
            <div class="form-group">
              <label>Max Loops</label>
              <input type="number" min="1" value={form.max_loops}
                onInput={e => setForm({ ...form, max_loops: e.target.value })}
                placeholder="Leave blank for infinite" />
            </div>
          )}
          <button type="submit" class="btn btn-primary">Save</button>
        </form>
      </Modal>

      <Modal open={!!editingSteps} onClose={() => setEditingSteps(null)} title="Escalation Steps">
        <div class="steps-builder">
          {steps.map((step, i) => (
            <div key={i} class="step-item">
              <div class="step-item-header">
                <span class="step-number">Step {i + 1}</span>
                <button class="btn btn-xs btn-danger" onClick={() => setSteps(steps.filter((_, j) => j !== i))}>Remove</button>
              </div>
              <div class="form-group">
                <label>Delay (minutes)</label>
                <input type="number" min="0" value={step.delay_minutes}
                  onInput={e => updateStep(i, { delay_minutes: +e.target.value })} />
              </div>
              <div class="form-group">
                <label>Repeat Count</label>
                <input type="number" min="0" value={step.repeat_count}
                  onInput={e => updateStep(i, { repeat_count: +e.target.value })} />
                <p class="text-muted text-xs">How many times to retry this step</p>
              </div>
              {step.repeat_count > 0 && (
                <div class="form-group">
                  <label>Repeat Interval (seconds)</label>
                  <input type="number" min="0" value={step.repeat_interval_seconds}
                    onInput={e => updateStep(i, { repeat_interval_seconds: +e.target.value })} />
                </div>
              )}
              <div class="form-group form-check">
                <label>
                  <input type="checkbox" checked={step.is_terminal}
                    onChange={e => updateStep(i, { is_terminal: e.target.checked })} />
                  Stop escalation at this step
                </label>
              </div>
              <div class="step-targets">
                <label><strong>Targets</strong></label>
                {(step.targets || []).map((target, ti) => (
                  <div key={ti} class="target-item">
                    <div class="form-group">
                      <label>Target Type</label>
                      <select value={target.target_type}
                        onChange={e => updateTarget(i, ti, { target_type: e.target.value, target_id: '' })}>
                        <option value="on_call_primary">On-Call Primary</option>
                        <option value="on_call_secondary">On-Call Secondary</option>
                        <option value="user">User</option>
                        <option value="team">Team</option>
                      </select>
                    </div>
                    {needsTargetId(target.target_type) && (
                      <div class="form-group">
                        <label>{target.target_type === 'user' ? 'User' : 'Team'}</label>
                        <select value={target.target_id}
                          onChange={e => updateTarget(i, ti, { target_id: e.target.value })}
                          required>
                          <option value="">Select {target.target_type}...</option>
                          {target.target_type === 'user'
                            ? users.map(u => <option key={u.id} value={u.id}>{u.email}</option>)
                            : teams.map(t => <option key={t.id} value={t.id}>{t.name}</option>)
                          }
                        </select>
                      </div>
                    )}
                    <div class="form-group">
                      <label>Notification Channel</label>
                      <select value={target.channel_id}
                        onChange={e => updateTarget(i, ti, { channel_id: e.target.value })}
                        required>
                        <option value="">Select channel...</option>
                        {channels.map(ch => (
                          <option key={ch.id} value={ch.id}>{ch.name} ({ch.type})</option>
                        ))}
                      </select>
                    </div>
                    <div class="form-group form-check">
                      <label>
                        <input type="checkbox" checked={target.simultaneous}
                          onChange={e => updateTarget(i, ti, { simultaneous: e.target.checked })} />
                        Notify all targets at once
                      </label>
                    </div>
                    <button class="btn btn-xs btn-danger" onClick={() => removeTarget(i, ti)}>Remove Target</button>
                  </div>
                ))}
                <button class="btn btn-xs" onClick={() => addTarget(i)}>Add Target</button>
              </div>
            </div>
          ))}
          <div class="steps-builder-actions">
            <button class="btn btn-sm" onClick={addStep}>Add Step</button>
            <button class="btn btn-sm btn-primary" onClick={saveSteps}>Save Steps</button>
          </div>
        </div>
      </Modal>
    </div>
  );
}

/* ─── Teams ─── */
function TeamsTab() {
  const [teams, setTeams] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ name: '', description: '' });
  const [editId, setEditId] = useState(null);
  const [membersTeamId, setMembersTeamId] = useState(null);
  const [members, setMembers] = useState([]);
  const [users, setUsers] = useState([]);
  const [memberForm, setMemberForm] = useState({ user_id: '', position: 0 });

  async function load() {
    setLoading(true);
    try {
      const data = await get('/teams');
      setTeams(data.teams || data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function save() {
    if (editId) await patch(`/teams/${editId}`, form);
    else await post('/teams', form);
    setShowModal(false);
    setEditId(null);
    load();
  }

  async function deleteTeam(id) {
    if (confirm('Delete this team?')) {
      await del(`/teams/${id}`);
      load();
    }
  }

  async function openMembers(teamId) {
    setMembersTeamId(teamId);
    try {
      const [detail, userData] = await Promise.all([
        get(`/teams/${teamId}`),
        users.length ? Promise.resolve(null) : get('/users'),
      ]);
      setMembers(detail.members || []);
      if (userData) setUsers(userData.users || userData || []);
      const nextPos = (detail.members || []).reduce((max, m) => Math.max(max, m.position || 0), 0) + 1;
      setMemberForm({ user_id: '', position: nextPos });
    } catch (_) {
      setMembers([]);
    }
  }

  async function addMember(e) {
    e.preventDefault();
    await post(`/teams/${membersTeamId}/members`, { user_id: memberForm.user_id, position: memberForm.position });
    const detail = await get(`/teams/${membersTeamId}`);
    setMembers(detail.members || []);
    const nextPos = (detail.members || []).reduce((max, m) => Math.max(max, m.position || 0), 0) + 1;
    setMemberForm({ user_id: '', position: nextPos });
  }

  async function removeMember(userId) {
    if (!confirm('Remove this member?')) return;
    await del(`/teams/${membersTeamId}/members/${userId}`);
    const detail = await get(`/teams/${membersTeamId}`);
    setMembers(detail.members || []);
  }

  function closeMembers() {
    setMembersTeamId(null);
    setMembers([]);
  }

  function userEmail(userId) {
    const u = users.find(u => u.id === userId);
    return u ? u.email : userId;
  }

  if (loading) return <LoadingPage />;

  return (
    <div>
      <div class="section-header">
        <h3>Teams</h3>
        <button class="btn btn-sm btn-primary" onClick={() => { setEditId(null); setForm({ name: '', description: '' }); setShowModal(true); }}>
          Add Team
        </button>
      </div>
      <div class="settings-list">
        {teams.map(t => (
          <div key={t.id} class="settings-item">
            <div><strong>{t.name}</strong></div>
            <div class="btn-group">
              <button class="btn btn-xs" onClick={() => openMembers(t.id)}>Members</button>
              <button class="btn btn-xs" onClick={() => { setEditId(t.id); setForm({ name: t.name, description: t.description || '' }); setShowModal(true); }}>Edit</button>
              <button class="btn btn-xs btn-danger" onClick={() => deleteTeam(t.id)}>Delete</button>
            </div>
          </div>
        ))}
        {teams.length === 0 && <p class="text-muted">No teams</p>}
      </div>
      <Modal open={showModal} onClose={() => setShowModal(false)} title={editId ? 'Edit Team' : 'Add Team'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Description</label>
            <textarea value={form.description} onInput={e => setForm({ ...form, description: e.target.value })} />
          </div>
          <button type="submit" class="btn btn-primary">Save</button>
        </form>
      </Modal>
      <Modal open={!!membersTeamId} onClose={closeMembers} title="Team Members">
        {members.length > 0 ? (
          <table class="data-table" style="width:100%; margin-bottom: 16px;">
            <thead>
              <tr><th>User</th><th>Position</th><th></th></tr>
            </thead>
            <tbody>
              {members.map(m => (
                <tr key={m.user_id}>
                  <td><strong>{userEmail(m.user_id)}</strong></td>
                  <td>{m.position}</td>
                  <td><button class="btn btn-xs btn-danger" onClick={() => removeMember(m.user_id)}>Remove</button></td>
                </tr>
              ))}
            </tbody>
          </table>
        ) : (
          <p class="text-muted" style="margin-bottom: 16px;">No members yet. Add one below.</p>
        )}
        <div style="border-top: 1px solid var(--color-border); padding-top: 16px;">
          <h4 style="margin-bottom: 12px;">Add Member</h4>
          <form onSubmit={addMember}>
            <div class="form-group">
              <label>User</label>
              <select value={memberForm.user_id} onChange={e => setMemberForm({ ...memberForm, user_id: e.target.value })} required>
                <option value="">Select user...</option>
                {users.filter(u => !members.some(m => m.user_id === u.id)).map(u => (
                  <option key={u.id} value={u.id}>{u.email}</option>
                ))}
              </select>
            </div>
            <div class="form-group">
              <label>Rotation Position</label>
              <input type="number" min="0" value={memberForm.position}
                onInput={e => setMemberForm({ ...memberForm, position: +e.target.value })} />
              <p class="form-help">Lower numbers are earlier in the on-call rotation.</p>
            </div>
            <button type="submit" class="btn btn-primary">Add Member</button>
          </form>
        </div>
      </Modal>
    </div>
  );
}

/* ─── Maintenance Windows ─── */
function MaintenanceTab() {
  const [windows, setWindows] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [form, setForm] = useState({ name: '', description: '', start_at: '', end_at: '', monitor_id: '', public: false, suppress_alerts: true, recurrence_type: 'none', recurrence_end_at: '', days_of_week: [], day_of_month: 1 });
  const [editId, setEditId] = useState(null);

  async function load() {
    setLoading(true);
    try {
      const data = await get('/maintenance-windows');
      setWindows(data.windows || data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function save() {
    const body = {
      name: form.name,
      description: form.description,
      start_at: new Date(form.start_at).toISOString(),
      end_at: new Date(form.end_at).toISOString(),
      monitor_id: form.monitor_id || '',
      public: form.public,
      suppress_alerts: form.suppress_alerts,
      recurrence_type: form.recurrence_type || 'none',
      days_of_week: form.recurrence_type === 'weekly' ? JSON.stringify(form.days_of_week || []) : '[]',
      day_of_month: form.recurrence_type === 'monthly' ? (form.day_of_month || 1) : 0,
    };
    if (form.recurrence_end_at) body.recurrence_end_at = new Date(form.recurrence_end_at).toISOString();
    else body.recurrence_end_at = '';
    if (editId) await patch(`/maintenance-windows/${editId}`, body);
    else await post('/maintenance-windows', body);
    setShowModal(false);
    setEditId(null);
    load();
  }

  async function deleteWindow(id) {
    if (confirm('Delete this window?')) { await del(`/maintenance-windows/${id}`); load(); }
  }

  if (loading) return <LoadingPage />;

  return (
    <div>
      <div class="section-header">
        <h3>Maintenance Windows</h3>
        <button class="btn btn-sm btn-primary" onClick={() => { setEditId(null); setForm({ name: '', description: '', start_at: '', end_at: '', monitor_id: '', public: false, suppress_alerts: true, recurrence_type: 'none', recurrence_end_at: '', days_of_week: [], day_of_month: 1 }); setShowModal(true); }}>
          Schedule Maintenance
        </button>
      </div>
      <div class="settings-list">
        {windows.map(w => (
          <div key={w.id} class="settings-item">
            <div>
              <strong>{w.name}</strong>
              {w.description && <span class="text-muted"> - {w.description}</span>}
              <span class="text-muted"> {formatTime(w.start_at)} - {formatTime(w.end_at)}</span>
              {w.recurrence_type && w.recurrence_type !== 'none' && <span class="badge badge-info" style={{ marginLeft: 6 }}>{w.recurrence_type}</span>}
            </div>
            <div class="btn-group">
              <button class="btn btn-xs btn-danger" onClick={() => deleteWindow(w.id)}>Delete</button>
            </div>
          </div>
        ))}
        {windows.length === 0 && <p class="text-muted">No maintenance windows</p>}
      </div>
      <Modal open={showModal} onClose={() => setShowModal(false)} title="Schedule Maintenance">
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Description</label>
            <textarea value={form.description} onInput={e => setForm({ ...form, description: e.target.value })} />
          </div>
          <div class="form-group">
            <label>Start</label>
            <input type="datetime-local" value={form.start_at} onInput={e => setForm({ ...form, start_at: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>End</label>
            <input type="datetime-local" value={form.end_at} onInput={e => setForm({ ...form, end_at: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Monitor ID</label>
            <input type="text" value={form.monitor_id} onInput={e => setForm({ ...form, monitor_id: e.target.value })} placeholder="Single monitor ID" />
          </div>
          <div class="form-group">
            <label>Recurrence</label>
            <select value={form.recurrence_type} onChange={e => setForm({ ...form, recurrence_type: e.target.value })}>
              <option value="none">None (one-time)</option>
              <option value="daily">Daily</option>
              <option value="weekly">Weekly</option>
              <option value="monthly">Monthly</option>
            </select>
          </div>
          {form.recurrence_type === 'weekly' && (
            <div class="form-group">
              <label>Days of Week</label>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap' }}>
                {['Sun','Mon','Tue','Wed','Thu','Fri','Sat'].map((day, i) => (
                  <label key={i} style={{ display: 'flex', alignItems: 'center', gap: 4 }}>
                    <input type="checkbox" checked={(form.days_of_week || []).includes(i)}
                      onChange={e => {
                        const days = [...(form.days_of_week || [])];
                        if (e.target.checked) { if (!days.includes(i)) days.push(i); }
                        else { const idx = days.indexOf(i); if (idx >= 0) days.splice(idx, 1); }
                        setForm({ ...form, days_of_week: days });
                      }} />
                    {day}
                  </label>
                ))}
              </div>
            </div>
          )}
          {form.recurrence_type === 'monthly' && (
            <div class="form-group">
              <label>Day of Month</label>
              <input type="number" min="1" max="31" value={form.day_of_month} onInput={e => setForm({ ...form, day_of_month: parseInt(e.target.value) || 1 })} />
            </div>
          )}
          {form.recurrence_type !== 'none' && (
            <div class="form-group">
              <label>Recurrence End Date (optional)</label>
              <input type="datetime-local" value={form.recurrence_end_at} onInput={e => setForm({ ...form, recurrence_end_at: e.target.value })} />
            </div>
          )}
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.public}
                onChange={e => setForm({ ...form, public: e.target.checked })} />
              Show on public status page
            </label>
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.suppress_alerts}
                onChange={e => setForm({ ...form, suppress_alerts: e.target.checked })} />
              Suppress alert notifications
            </label>
          </div>
          <button type="submit" class="btn btn-primary">Save</button>
        </form>
      </Modal>
    </div>
  );
}

/* ─── Status Pages ─── */
function MonitorConfig({ monitor, orgMonitors, onUpdate, onRemove }) {
  return (
    <div class="sp-layout-monitor" data-monitor-id={monitor.id}>
      <div class="form-group">
        <label>Monitor</label>
        <select value={monitor.monitor_id} onChange={e => onUpdate({ monitor_id: e.target.value })}>
          <option value="">Select monitor...</option>
          {orgMonitors.map(m => <option key={m.id} value={m.id}>{m.name} ({m.type})</option>)}
        </select>
      </div>
      <div class="form-group">
        <label>Display Name</label>
        <input type="text" value={monitor.display_name}
          onInput={e => onUpdate({ display_name: e.target.value })}
          placeholder={orgMonitors.find(m => m.id === monitor.monitor_id)?.name || 'Monitor name'} />
      </div>
      <div class="form-group">
        <label>Display Options</label>
        <div style="display: flex; flex-wrap: wrap; gap: 12px;">
          <label><input type="checkbox" checked={monitor.show_status} onChange={e => onUpdate({ show_status: e.target.checked })} /> Status badge</label>
          <label><input type="checkbox" checked={monitor.show_uptime_bar} onChange={e => onUpdate({ show_uptime_bar: e.target.checked })} /> Uptime bar</label>
          <label><input type="checkbox" checked={monitor.show_latency} onChange={e => onUpdate({ show_latency: e.target.checked })} /> Latency</label>
          <label><input type="checkbox" checked={monitor.show_checks} onChange={e => onUpdate({ show_checks: e.target.checked })} /> Recent checks</label>
          <label><input type="checkbox" checked={monitor.show_incidents} onChange={e => onUpdate({ show_incidents: e.target.checked })} /> Incidents</label>
        </div>
      </div>
      {monitor.show_uptime_bar && (
        <div class="form-group">
          <label>Uptime Periods</label>
          {(() => {
            const plan = currentOrg.value?.plan || 'free';
            const allPeriods = ['24h', '7d', '30d', '90d'];
            const allowedPeriods = plan === 'free' ? ['24h'] : allPeriods;
            return (
              <select multiple
                value={(monitor.uptime_periods || '').split(',').filter(Boolean)}
                style="min-height: 80px;"
                onChange={e => {
                  const selected = Array.from(e.target.selectedOptions, o => o.value);
                  onUpdate({ uptime_periods: selected.join(',') });
                }}>
                {allowedPeriods.map(p => (
                  <option key={p} value={p}>{p}</option>
                ))}
              </select>
            );
          })()}
          {(currentOrg.value?.plan || 'free') === 'free' && (
            <p class="form-help">Free plan supports 24h only. <a href="/settings/billing">Upgrade</a> for more periods.</p>
          )}
        </div>
      )}
      <button class="btn btn-xs btn-danger" onClick={onRemove}>Remove Monitor</button>
    </div>
  );
}

function StatusPageTab() {
  const [pages, setPages] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({ name: '', slug: '', description: '', custom_css: '', enabled: true });
  const [layoutPageId, setLayoutPageId] = useState(null);
  const [groups, setGroups] = useState([]);
  const [monitors, setMonitors] = useState([]);
  const [orgMonitors, setOrgMonitors] = useState([]);
  const { toasts, toast } = useToast();
  const org = currentOrg.value;

  async function load() {
    setLoading(true);
    try {
      setPages(await get('/status-pages'));
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  function closeModal() {
    setShowModal(false);
    setEditId(null);
    setForm({ name: '', slug: '', description: '', custom_css: '', enabled: true });
  }

  function openEdit(page) {
    setEditId(page.id);
    setForm({
      name: page.name || '',
      slug: page.slug || '',
      description: page.description || '',
      custom_css: page.custom_css || '',
      enabled: page.enabled ?? true,
    });
    setShowModal(true);
  }

  async function save(e) {
    e.preventDefault();
    try {
      if (editId) {
        await patch(`/status-pages/${editId}`, form);
        toast('Status page updated');
      } else {
        await post('/status-pages', form);
        toast('Status page created');
      }
      setShowModal(false);
      load();
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  async function deletePage(id) {
    if (!confirm('Delete this status page? This cannot be undone.')) return;
    try {
      await del(`/status-pages/${id}`);
      toast('Status page deleted');
      load();
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  // ── Layout builder ──

  async function openLayout(pageId) {
    setLayoutPageId(pageId);
    try {
      const detail = await get(`/status-pages/${pageId}`);
      setGroups(detail.groups || []);
      setMonitors(detail.monitors || []);
      const allMonitors = await get('/monitors');
      setOrgMonitors(allMonitors.monitors || allMonitors || []);
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  async function saveLayout() {
    // Validate that at least one monitor exists in any group (or ungrouped)
    const hasMonitors = monitors.some(m => m.monitor_id);
    if (!hasMonitors) {
      toast('Add at least one monitor to a group before saving.', 'error');
      return;
    }
    try {
      await put(`/status-pages/${layoutPageId}/layout`, { groups, monitors });
      toast('Layout saved');
      setLayoutPageId(null);
    } catch (err) {
      toast(err.message, 'error');
    }
  }

  function addGroup() {
    const name = prompt('Group name:');
    if (!name) return;
    setGroups([...groups, {
      id: crypto.randomUUID(),
      status_page_id: layoutPageId,
      name,
      position: groups.length,
      default_expanded: true,
    }]);
  }

  function removeGroup(index) {
    const groupId = groups[index].id;
    setGroups(groups.filter((_, i) => i !== index).map((g, i) => ({ ...g, position: i })));
    setMonitors(monitors.filter(m => m.group_id !== groupId));
  }

  function updateGroup(index, updates) {
    setGroups(groups.map((g, i) => i === index ? { ...g, ...updates } : g));
  }

  function moveGroup(index, direction) {
    const newGroups = [...groups];
    const [item] = newGroups.splice(index, 1);
    newGroups.splice(index + direction, 0, item);
    setGroups(newGroups.map((g, i) => ({ ...g, position: i })));
  }

  function addMonitorToGroup(groupId) {
    const newId = crypto.randomUUID();
    setMonitors([...monitors, {
      id: newId,
      group_id: groupId || '',
      status_page_id: layoutPageId,
      monitor_id: '',
      display_name: '',
      position: monitors.filter(m => m.group_id === (groupId || '')).length,
      show_status: true,
      show_uptime_bar: true,
      uptime_periods: '24h,90d',
      show_latency: false,
      show_checks: false,
      show_incidents: true,
    }]);
    // Scroll to the new monitor config after render.
    setTimeout(() => {
      const el = document.querySelector(`[data-monitor-id="${newId}"]`);
      if (el) el.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }, 50);
  }

  function updateMonitor(monId, updates) {
    setMonitors(monitors.map(m => m.id === monId ? { ...m, ...updates } : m));
  }

  function removeMonitor(monId) {
    setMonitors(monitors.filter(m => m.id !== monId));
  }

  function moveMonitor(monId, direction) {
    const mon = monitors.find(m => m.id === monId);
    if (!mon) return;
    const siblings = monitors
      .filter(m => m.group_id === mon.group_id)
      .sort((a, b) => a.position - b.position);
    const idx = siblings.findIndex(m => m.id === monId);
    const newIdx = idx + direction;
    if (newIdx < 0 || newIdx >= siblings.length) return;
    const reordered = [...siblings];
    const [item] = reordered.splice(idx, 1);
    reordered.splice(newIdx, 0, item);
    const reorderedIds = new Set(reordered.map(m => m.id));
    setMonitors([
      ...monitors.filter(m => !reorderedIds.has(m.id)),
      ...reordered.map((m, i) => ({ ...m, position: i })),
    ]);
  }

  // ── Tier limits ──

  const plan = org?.plan || 'free';
  const atLimit = plan === 'free' || (plan === 'pro' && pages.length >= 2);
  const limitMessage = plan === 'free'
    ? 'Upgrade to create status pages'
    : plan === 'pro' && pages.length >= 2
      ? 'Pro plan limited to 2 status pages'
      : null;

  if (loading) return <LoadingPage />;

  // ── Layout modal ──

  if (layoutPageId) {
    const layoutPage = pages.find(p => p.id === layoutPageId);
    const ungroupedMonitors = monitors.filter(m => !m.group_id).sort((a, b) => a.position - b.position);

    return (
      <div>
        <ToastContainer toasts={toasts} />
        <div class="section-header">
          <h3>Layout: {layoutPage?.name || 'Status Page'}</h3>
          <div class="btn-group">
            <button class="btn btn-sm" onClick={() => setLayoutPageId(null)}>Cancel</button>
            <button class="btn btn-sm btn-primary" onClick={saveLayout}>Save Layout</button>
          </div>
        </div>

        {monitors.length > 20 && (
          <div class="alert alert-info" style="margin-bottom: 12px;">
            This page has {monitors.length} monitors. Pages with 20+ monitors may load slowly for visitors. Consider splitting into multiple status pages.
          </div>
        )}

        <div style="margin-bottom: 16px;">
          <strong>Ungrouped Monitors</strong>
          {ungroupedMonitors.map((mon) => (
            <MonitorConfig key={mon.id} monitor={mon} orgMonitors={orgMonitors}
              onUpdate={updates => updateMonitor(mon.id, updates)}
              onRemove={() => removeMonitor(mon.id)} />
          ))}
          <button class="btn btn-xs" onClick={() => addMonitorToGroup('')}>Add Monitor</button>
        </div>

        {groups.sort((a, b) => a.position - b.position).map((group, i) => {
          const groupMonitors = monitors.filter(m => m.group_id === group.id).sort((a, b) => a.position - b.position);
          return (
            <div key={group.id} class="sp-layout-group">
              <div class="sp-layout-group-header">
                <strong>{group.name}</strong>
                <div class="btn-group">
                  <button class="btn btn-xs" onClick={() => moveGroup(i, -1)} disabled={i === 0}>↑</button>
                  <button class="btn btn-xs" onClick={() => moveGroup(i, 1)} disabled={i === groups.length - 1}>↓</button>
                  <button class="btn btn-xs btn-danger" onClick={() => removeGroup(i)}>Remove</button>
                </div>
              </div>
              <div class="form-group form-check">
                <label>
                  <input type="checkbox" checked={group.default_expanded}
                    onChange={e => updateGroup(i, { default_expanded: e.target.checked })} />
                  Expanded by default
                </label>
              </div>
              {groupMonitors.map((mon) => (
                <MonitorConfig key={mon.id} monitor={mon} orgMonitors={orgMonitors}
                  onUpdate={updates => updateMonitor(mon.id, updates)}
                  onRemove={() => removeMonitor(mon.id)} />
              ))}
              <button class="btn btn-xs" onClick={() => addMonitorToGroup(group.id)}>Add Monitor</button>
            </div>
          );
        })}

        <button class="btn btn-sm" onClick={addGroup} style="margin-top: 8px;">Add Group</button>
      </div>
    );
  }

  // ── List view ──

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Status Pages</h3>
        {atLimit
          ? <span class="text-muted" style="font-size: 0.875rem;">{limitMessage}</span>
          : <button class="btn btn-sm btn-primary" onClick={() => { closeModal(); setShowModal(true); }}>Create Status Page</button>
        }
      </div>
      <div class="settings-list">
        {pages.map(page => (
          <div key={page.id} class="settings-item" style="cursor: pointer;" onClick={() => openEdit(page)}>
            <div>
              <strong>{page.name}</strong>
              <span class="text-muted"> /{page.slug}</span>
              {page.enabled
                ? <span class="label-tag" style="margin-left: 8px;">Enabled</span>
                : <span class="label-tag label-muted" style="margin-left: 8px;">Disabled</span>
              }
            </div>
            <div class="btn-group" onClick={e => e.stopPropagation()}>
              <a class="btn btn-xs" href={`/${org?.slug}/status/${page.slug}`} target="_blank" rel="noopener">View →</a>
              <button class="btn btn-xs" onClick={() => openLayout(page.id)}>Layout</button>
              <button class="btn btn-xs btn-danger" onClick={() => deletePage(page.id)}>Delete</button>
            </div>
          </div>
        ))}
        {pages.length === 0 && <p class="text-muted">No status pages yet</p>}
      </div>

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit Status Page' : 'Create Status Page'}>
        <form onSubmit={save}>
          <div class="form-group">
            <label>Name</label>
            <input type="text" value={form.name} onInput={e => setForm({...form, name: e.target.value})} required />
          </div>
          <div class="form-group">
            <label>Slug</label>
            <input type="text" value={form.slug} onInput={e => setForm({...form, slug: e.target.value})} required
                   pattern="[a-z0-9-]+" title="Lowercase letters, numbers, and hyphens only" />
            {form.slug && <p class="form-help">Public URL: /{org?.slug}/status/{form.slug}</p>}
          </div>
          <div class="form-group">
            <label>Description</label>
            <textarea value={form.description} onInput={e => setForm({...form, description: e.target.value})} />
          </div>
          <div class="form-group">
            <label>Custom CSS</label>
            <textarea value={form.custom_css} onInput={e => setForm({...form, custom_css: e.target.value})}
                      style="font-family: var(--font-mono); font-size: 0.8125rem;" rows="6"
                      placeholder=".sp-monitor { border-color: #your-brand; }" />
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.enabled} onChange={e => setForm({...form, enabled: e.target.checked})} />
              Enable public status page
            </label>
          </div>
          <button type="submit" class="btn btn-primary">{editId ? 'Save' : 'Create'}</button>
        </form>
      </Modal>
    </div>
  );
}

/* ─── API Keys ─── */
function APIKeysTab() {
  const [keys, setKeys] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [newKeyName, setNewKeyName] = useState('');
  const [newKeyScopes, setNewKeyScopes] = useState('');
  const [newKeyExpires, setNewKeyExpires] = useState('');
  const [createdKey, setCreatedKey] = useState(null);

  async function load() {
    setLoading(true);
    try {
      const data = await get('/org/api-keys');
      setKeys(data.api_keys || data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function create() {
    const body = { name: newKeyName };
    if (newKeyScopes.trim()) {
      body.scopes = newKeyScopes.split(',').map(s => s.trim()).filter(Boolean);
    }
    if (newKeyExpires) {
      body.expires_at = new Date(newKeyExpires).toISOString();
    }
    const res = await post('/org/api-keys', body);
    setCreatedKey(res.key || res.api_key || res);
    setNewKeyName('');
    setNewKeyScopes('');
    setNewKeyExpires('');
    load();
  }

  async function revoke(id) {
    if (confirm('Revoke this API key?')) {
      await del(`/org/api-keys/${id}`);
      load();
    }
  }

  if (loading) return <LoadingPage />;

  return (
    <div>
      <div class="section-header">
        <h3>API Keys</h3>
        <button class="btn btn-sm btn-primary" onClick={() => { setCreatedKey(null); setNewKeyName(''); setNewKeyScopes(''); setNewKeyExpires(''); setShowModal(true); }}>
          Create Key
        </button>
      </div>
      <div class="settings-list">
        {keys.map(k => (
          <div key={k.id} class="settings-item">
            <div>
              <strong>{k.name}</strong>
              <span class="text-muted"> - Created {formatTime(k.created_at)}</span>
              {k.last_used_at && <span class="text-muted"> - Last used: {formatTime(k.last_used_at)}</span>}
            </div>
            <button class="btn btn-xs btn-danger" onClick={() => revoke(k.id)}>Revoke</button>
          </div>
        ))}
        {keys.length === 0 && <p class="text-muted">No API keys</p>}
      </div>
      <Modal open={showModal} onClose={() => setShowModal(false)} title="Create API Key">
        {createdKey ? (
          <div>
            <p>Your new API key (copy it now, it won't be shown again):</p>
            <code class="api-key-display">{typeof createdKey === 'string' ? createdKey : createdKey.key || JSON.stringify(createdKey)}</code>
            <button class="btn btn-sm" onClick={() => setShowModal(false)}>Done</button>
          </div>
        ) : (
          <form onSubmit={e => { e.preventDefault(); create(); }}>
            <div class="form-group">
              <label>Key Name</label>
              <input type="text" value={newKeyName} onInput={e => setNewKeyName(e.target.value)} required placeholder="e.g. CI Pipeline" />
            </div>
            <div class="form-group">
              <label>Scopes</label>
              <input type="text" value={newKeyScopes} onInput={e => setNewKeyScopes(e.target.value)} placeholder="e.g. monitors:read, alerts:write" />
            </div>
            <div class="form-group">
              <label>Expires At</label>
              <input type="date" value={newKeyExpires} onInput={e => setNewKeyExpires(e.target.value)} />
            </div>
            <button type="submit" class="btn btn-primary">Create</button>
          </form>
        )}
      </Modal>
    </div>
  );
}

/* ─── SSO / OIDC ─── */
function SSOTab() {
  const [connections, setConnections] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({
    provider: '', client_id: '', client_secret: '',
    issuer_url: '', authorize_url: '', token_url: '', userinfo_url: '',
    scopes: 'openid,email,profile', auto_provision: true, default_role: 'member',
    enabled: true,
  });

  async function load() {
    setLoading(true);
    try {
      const data = await get('/org/oidc-connections');
      setConnections(data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  function resetForm() {
    setForm({
      provider: '', client_id: '', client_secret: '',
      issuer_url: '', authorize_url: '', token_url: '', userinfo_url: '',
      scopes: 'openid,email,profile', auto_provision: true, default_role: 'member',
      enabled: true,
    });
  }

  async function save() {
    const body = {
      ...form,
      scopes: form.scopes.split(',').map(s => s.trim()).filter(Boolean),
    };
    if (editId) await patch(`/org/oidc-connections/${editId}`, body);
    else await post('/org/oidc-connections', body);
    setShowModal(false);
    setEditId(null);
    resetForm();
    load();
  }

  async function deleteConn(id) {
    if (confirm('Delete this SSO connection?')) {
      await del(`/org/oidc-connections/${id}`);
      load();
    }
  }

  function edit(c) {
    setEditId(c.id);
    setForm({
      provider: c.provider, client_id: c.client_id, client_secret: '',
      issuer_url: c.issuer_url || '', authorize_url: c.authorize_url || '',
      token_url: c.token_url || '', userinfo_url: c.userinfo_url || '',
      scopes: (c.scopes || []).join(','),
      auto_provision: c.auto_provision, default_role: c.default_role || 'member',
      enabled: c.enabled,
    });
    setShowModal(true);
  }

  const isOIDCMode = !!form.issuer_url;

  if (loading) return <LoadingPage />;

  return (
    <div>
      <div class="section-header">
        <h3>SSO Connections</h3>
        <button class="btn btn-sm btn-primary" onClick={() => { setEditId(null); resetForm(); setShowModal(true); }}>
          Add Connection
        </button>
      </div>
      <div class="settings-list">
        {connections.map(c => (
          <div key={c.id} class="settings-item">
            <div>
              <strong>{c.provider}</strong>
              <span class="label-tag">{c.issuer_url ? 'OIDC' : 'OAuth2'}</span>
              {!c.enabled && <span class="label-tag label-muted">Disabled</span>}
            </div>
            <div class="btn-group">
              <button class="btn btn-xs" onClick={() => edit(c)}>Edit</button>
              <button class="btn btn-xs btn-danger" onClick={() => deleteConn(c.id)}>Delete</button>
            </div>
          </div>
        ))}
        {connections.length === 0 && <p class="text-muted">No SSO connections configured</p>}
      </div>
      <Modal open={showModal} onClose={() => setShowModal(false)} title={editId ? 'Edit SSO Connection' : 'Add SSO Connection'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          <div class="form-group">
            <label>Provider Name</label>
            <input type="text" value={form.provider} onInput={e => setForm({ ...form, provider: e.target.value })}
                   required placeholder="e.g. Google, GitHub, Okta" />
          </div>
          <div class="form-group">
            <label>Client ID</label>
            <input type="text" value={form.client_id} onInput={e => setForm({ ...form, client_id: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Client Secret</label>
            <input type="password" value={form.client_secret}
                   onInput={e => setForm({ ...form, client_secret: e.target.value })}
                   placeholder={editId ? '(leave blank to keep current)' : ''} required={!editId} />
          </div>
          <div class="form-group">
            <label>Issuer URL (OIDC auto-discovery)</label>
            <input type="url" value={form.issuer_url}
                   onInput={e => setForm({ ...form, issuer_url: e.target.value })}
                   placeholder="https://accounts.google.com" />
            <p class="text-muted text-xs">Set this for OIDC providers. Leave blank for plain OAuth2 and fill in the URLs below.</p>
          </div>
          {!isOIDCMode && (
            <>
              <div class="form-group">
                <label>Authorize URL</label>
                <input type="url" value={form.authorize_url}
                       onInput={e => setForm({ ...form, authorize_url: e.target.value })}
                       placeholder="https://github.com/login/oauth/authorize" required={!isOIDCMode} />
              </div>
              <div class="form-group">
                <label>Token URL</label>
                <input type="url" value={form.token_url}
                       onInput={e => setForm({ ...form, token_url: e.target.value })}
                       placeholder="https://github.com/login/oauth/access_token" required={!isOIDCMode} />
              </div>
              <div class="form-group">
                <label>Userinfo URL</label>
                <input type="url" value={form.userinfo_url}
                       onInput={e => setForm({ ...form, userinfo_url: e.target.value })}
                       placeholder="https://api.github.com/user" required={!isOIDCMode} />
              </div>
            </>
          )}
          <div class="form-group">
            <label>Scopes</label>
            <input type="text" value={form.scopes} onInput={e => setForm({ ...form, scopes: e.target.value })}
                   placeholder="openid,email,profile" />
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.auto_provision}
                     onChange={e => setForm({ ...form, auto_provision: e.target.checked })} />
              Auto-provision new users on first SSO login
            </label>
          </div>
          <div class="form-group">
            <label>Default Role (for auto-provisioned users)</label>
            <select value={form.default_role} onChange={e => setForm({ ...form, default_role: e.target.value })}>
              <option value="viewer">Viewer</option>
              <option value="member">Member</option>
              <option value="admin">Admin</option>
            </select>
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.enabled}
                     onChange={e => setForm({ ...form, enabled: e.target.checked })} />
              Enabled
            </label>
          </div>
          <button type="submit" class="btn btn-primary">Save</button>
        </form>
      </Modal>
    </div>
  );
}

/* ─── Members ─── */
function MembersTab() {
  const [members, setMembers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [showInvite, setShowInvite] = useState(false);
  const [showTransfer, setShowTransfer] = useState(false);
  const [inviteForm, setInviteForm] = useState({ email: '', password: '', role: 'member' });
  const [saving, setSaving] = useState(false);
  const { toasts, toast } = useToast();
  const role = currentUser.value?.role;
  const isOwnerOrAdmin = role === 'owner' || role === 'admin';

  async function load() {
    setLoading(true);
    try {
      const data = await get('/users');
      setMembers(data.users || data || []);
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function invite(e) {
    e.preventDefault();
    setSaving(true);
    try {
      await post('/users', inviteForm);
      toast('Member invited');
      setShowInvite(false);
      setInviteForm({ email: '', password: '', role: 'member' });
      load();
    } catch (err) {
      toast(err.message || 'Failed to invite', 'error');
    }
    setSaving(false);
  }

  async function changeRole(userId, newRole) {
    try {
      await patch(`/users/${userId}`, { role: newRole });
      toast('Role updated');
      load();
    } catch (err) {
      toast(err.message || 'Failed to update role', 'error');
    }
  }

  async function removeMember(userId) {
    if (!confirm('Remove this member?')) return;
    try {
      await del(`/users/${userId}`);
      toast('Member removed');
      load();
    } catch (err) {
      toast(err.message || 'Failed to remove', 'error');
    }
  }

  async function transferOwnership(e) {
    e.preventDefault();
    const targetId = e.target.elements.target.value;
    if (!targetId) return;
    setSaving(true);
    try {
      await post('/org/transfer-ownership', { target_user_id: targetId });
      toast('Ownership transferred');
      setShowTransfer(false);
      load();
    } catch (err) {
      toast(err.message || 'Failed to transfer', 'error');
    }
    setSaving(false);
  }

  if (loading) return <LoadingPage />;

  const owner = members.find(m => m.role === 'owner');
  const admins = members.filter(m => m.role === 'admin');
  const freeLimited = appMeta.value?.billing_enabled && currentOrg.value?.plan === 'free' && members.length >= 5;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>Members</h3>
        {isOwnerOrAdmin && (
          freeLimited
            ? <a href="/settings/billing" class="btn btn-sm btn-primary">Upgrade to add more members</a>
            : <button class="btn btn-sm btn-primary" onClick={() => setShowInvite(true)}>Invite Member</button>
        )}
      </div>
      <div class="settings-list">
        <table class="data-table" style="width:100%">
          <thead>
            <tr><th>Email</th><th>Name</th><th>Role</th><th>Joined</th><th>Actions</th></tr>
          </thead>
          <tbody>
            {members.map(m => (
              <tr key={m.id}>
                <td>{m.email}</td>
                <td>{m.name || ''}</td>
                <td>
                  {isOwnerOrAdmin && m.id !== currentUser.value?.id && m.role !== 'owner' ? (
                    <select value={m.role} onChange={e => changeRole(m.id, e.target.value)}>
                      <option value="member">member</option>
                      <option value="admin">admin</option>
                      {role === 'owner' && <option value="owner">owner</option>}
                    </select>
                  ) : (
                    m.role
                  )}
                </td>
                <td>{formatTime(m.created_at)}</td>
                <td>
                  <div class="btn-group">
                    {isOwnerOrAdmin && m.id !== currentUser.value?.id && m.role !== 'owner' && (
                      <button class="btn btn-xs btn-danger" onClick={() => removeMember(m.id)}>Remove</button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        {members.length === 0 && <p class="text-muted">No members</p>}
      </div>
      {role === 'owner' && admins.length > 0 && (
        <div style="margin-top: 1rem;">
          <button class="btn btn-sm" onClick={() => setShowTransfer(true)}>Transfer Ownership</button>
        </div>
      )}
      <Modal open={showInvite} onClose={() => setShowInvite(false)} title="Invite Member">
        <form onSubmit={invite}>
          <div class="form-group">
            <label>Email</label>
            <input type="email" value={inviteForm.email} onInput={e => setInviteForm({ ...inviteForm, email: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Password</label>
            <input type="password" value={inviteForm.password} onInput={e => setInviteForm({ ...inviteForm, password: e.target.value })} required />
          </div>
          <div class="form-group">
            <label>Role</label>
            <select value={inviteForm.role} onChange={e => setInviteForm({ ...inviteForm, role: e.target.value })}>
              <option value="member">member</option>
              <option value="admin">admin</option>
            </select>
          </div>
          <button type="submit" class="btn btn-primary" disabled={saving}>{saving ? 'Inviting...' : 'Invite'}</button>
        </form>
      </Modal>
      <Modal open={showTransfer} onClose={() => setShowTransfer(false)} title="Transfer Ownership">
        <p class="text-muted" style="margin-bottom: 1rem;">Select an admin to become the new owner. You will be demoted to admin.</p>
        <form onSubmit={transferOwnership}>
          <div class="form-group">
            <label>New Owner</label>
            <select name="target" required>
              <option value="">Select admin...</option>
              {admins.map(a => <option key={a.id} value={a.id}>{a.email}</option>)}
            </select>
          </div>
          <button type="submit" class="btn btn-primary btn-danger" disabled={saving}>{saving ? 'Transferring...' : 'Transfer Ownership'}</button>
        </form>
      </Modal>
    </div>
  );
}

/* ─── Billing ─── */
function BillingTab() {
  const [billing, setBilling] = useState(null);
  const [loading, setLoading] = useState(true);
  const [billingEmail, setBillingEmail] = useState('');
  const [saving, setSaving] = useState(false);
  const { toasts, toast } = useToast();
  const role = currentUser.value?.role;
  const isOwnerOrAdmin = role === 'owner' || role === 'admin';

  async function load() {
    setLoading(true);
    try {
      const data = await get('/billing');
      setBilling(data);
      setBillingEmail(data.billing_email || '');
    } catch (_) {}
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function upgrade(plan) {
    setSaving(true);
    try {
      const res = await post('/billing/checkout', plan ? { plan } : {});
      if (res.url && (res.url.startsWith('https://') || res.url.startsWith('http://'))) {
        window.location.href = res.url;
      }
    } catch (err) {
      toast(err.message || 'Failed to start checkout', 'error');
    }
    setSaving(false);
  }

  async function openPortal() {
    setSaving(true);
    try {
      const res = await post('/billing/portal', {});
      if (res.url) window.location.href = res.url;
    } catch (err) {
      toast(err.message || 'Failed to open portal', 'error');
    }
    setSaving(false);
  }

  async function toggleEnterprise() {
    setSaving(true);
    try {
      if (billing.has_enterprise) {
        await del('/billing/enterprise');
        toast('Enterprise removed');
      } else {
        await post('/billing/enterprise', {});
        toast('Enterprise added');
      }
      load();
    } catch (err) {
      toast(err.message || 'Failed to update enterprise', 'error');
    }
    setSaving(false);
  }

  async function saveBillingEmail(e) {
    e.preventDefault();
    setSaving(true);
    try {
      await patch('/billing', { billing_email: billingEmail });
      toast('Billing email updated');
    } catch (err) {
      toast(err.message || 'Failed to update', 'error');
    }
    setSaving(false);
  }

  if (currentOrg.value?.plan === 'promo') {
    return (
      <div>
        <ToastContainer toasts={toasts} />
        <div class="card">
          <div class="section-header">
            <h3>Promotional Account</h3>
            <span class="badge" style="background: var(--color-info); color: white;">PROMO</span>
          </div>
          <p style="color: var(--color-text-secondary); margin: 8px 0 16px">
            Your organization has a promotional account with full Enterprise features.
          </p>
          {appMeta.value?.promo_expires_at && (
            <div class="form-group">
              <label>Expires</label>
              <p>{new Date(appMeta.value.promo_expires_at).toLocaleDateString()}</p>
            </div>
          )}
          {appMeta.value?.promo_days_remaining != null && (
            <div class="form-group">
              <label>Days Remaining</label>
              <p>{appMeta.value.promo_expiring ?
                <span style="color: var(--color-warning); font-weight: 600">{appMeta.value.promo_days_remaining} days (grace period)</span> :
                <span>{appMeta.value.promo_days_remaining} days</span>
              }</p>
            </div>
          )}
          <div style="margin-top: 16px; border-top: 1px solid var(--color-border); padding-top: 16px">
            <p style="margin-bottom: 12px">Ready to upgrade to a paid plan?</p>
            <div class="btn-group">
              <button class="btn btn-primary" onClick={() => upgrade('pro')} disabled={saving}>Upgrade to Pro</button>
              <button class="btn btn-outline" onClick={() => upgrade('enterprise')} disabled={saving}>Upgrade to Enterprise</button>
            </div>
          </div>
        </div>
      </div>
    )
  }

  if (loading) return <LoadingPage />;
  if (!billing) return <ErrorMessage error="Failed to load billing" onRetry={load} />;

  const isFree = billing.plan === 'free';

  // Format price from cents (e.g. 1500 -> "$15")
  function fmtPrice(cents, currency) {
    if (!cents) return '';
    const amt = (cents / 100).toFixed(cents % 100 === 0 ? 0 : 2);
    const sym = (currency || 'usd') === 'usd' ? '$' : currency?.toUpperCase() + ' ';
    return `${sym}${amt}`;
  }
  const seatPrice = fmtPrice(billing.seat_price_cents, billing.seat_currency);
  const entPrice = fmtPrice(billing.ent_price_cents, billing.ent_currency);
  const seatLabel = seatPrice ? `${seatPrice}/seat/mo` : 'paid';
  const entLabel = entPrice ? `${entPrice}/mo` : 'enterprise';

  return (
    <div>
      <ToastContainer toasts={toasts} />
      {billing.status === 'past_due' && (
        <div class="payment-banner" style="margin-bottom: 1rem; border-radius: var(--radius-sm);">
          Payment failed. Update your payment method to avoid service interruption.
        </div>
      )}
      <h3>Billing</h3>
      <Card>
        <div class="form-group">
          <label>Current Plan</label>
          <p><strong>{isFree ? 'Free' : billing.has_enterprise ? 'Paid + Enterprise' : 'Paid'}</strong> {!isFree && seatPrice && <span class="text-muted">{billing.has_enterprise ? `(${seatLabel} + ${entLabel})` : `(${seatLabel})`}</span>}</p>
        </div>
        {isFree ? (
          isOwnerOrAdmin && (
            <button class="btn btn-primary" onClick={upgrade} disabled={saving}>
              {saving ? 'Redirecting...' : `Upgrade to Paid${seatPrice ? ` (${seatLabel})` : ''}`}
            </button>
          )
        ) : (
          <>
            <div class="form-group">
              <label>Seats</label>
              <p>{billing.seat_count || 0}</p>
            </div>
            {billing.has_enterprise && (
              <div class="form-group">
                <label>Enterprise</label>
                <p>Enabled</p>
              </div>
            )}
            {billing.period_end && !billing.period_end.startsWith('0001') && (
              <div class="form-group">
                <label>Current Period Ends</label>
                <p>{formatTime(billing.period_end)}</p>
              </div>
            )}
            {(billing.sms_used != null || billing.voice_used != null) && (
              <div class="form-group">
                <label>Usage This Period</label>
                <p>
                  {billing.sms_used != null && <span>SMS: {billing.sms_used} </span>}
                  {billing.voice_used != null && <span>Voice: {billing.voice_used}</span>}
                </p>
              </div>
            )}
            {isOwnerOrAdmin && (
              <div class="btn-group" style="margin-top: 0.5rem;">
                <button class="btn btn-primary" onClick={openPortal} disabled={saving}>Manage Subscription</button>
                <button class="btn btn-sm" onClick={toggleEnterprise} disabled={saving}>
                  {billing.has_enterprise ? 'Remove Enterprise' : `Add Enterprise${entPrice ? ` (${entLabel})` : ''}`}
                </button>
              </div>
            )}
          </>
        )}
      </Card>
      {!isFree && (
        <Card class="mt-1" style="margin-top: 1rem;">
          <h4>Billing Email</h4>
          <form onSubmit={saveBillingEmail}>
            <div class="form-group">
              <label>Email</label>
              <input type="email" value={billingEmail} onInput={e => setBillingEmail(e.target.value)} placeholder="billing@company.com" />
            </div>
            <button type="submit" class="btn btn-primary" disabled={saving}>{saving ? 'Saving...' : 'Save'}</button>
          </form>
        </Card>
      )}
    </div>
  );
}

/* ─── Data Retention ─── */
function DataTab() {
  const isFoss = appMeta.value?.edition === 'foss';
  const retentionDays = appMeta.value?.retention_days || (isFoss ? 30 : null);
  const [stats, setStats] = useState(null);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    async function load() {
      setLoading(true);
      try {
        const data = await get('/monitors/check-stats');
        setStats({
          totalChecks: data.total_checks || 0,
          oldestCheck: data.oldest ? new Date(data.oldest) : null,
          newestCheck: data.newest ? new Date(data.newest) : null,
          monitorCount: data.monitor_count || 0,
        });
      } catch (_) {
        setStats(null);
      } finally {
        setLoading(false);
      }
    }
    load();
  }, []);

  function formatAge(date) {
    if (!date) return '-';
    const days = Math.floor((Date.now() - date.getTime()) / 86400000);
    if (days === 0) return 'Today';
    if (days === 1) return '1 day ago';
    return `${days} days ago`;
  }

  return (
    <div>
      <h3>Data Retention</h3>
      <Card>
        <div class="section-header">
          <h3>Retention Policy</h3>
        </div>
        {retentionDays ? (
          <>
            <p>
              Check data is automatically retained for <strong>{retentionDays} days</strong>.
              Data older than this window is pruned hourly by a background process.
            </p>
            {isFoss && (
              <p class="text-muted" style="font-size: 0.8125rem; margin-top: 8px">
                The FOSS edition has a fixed 30-day retention window. Rollup aggregates
                (hourly and daily uptime/latency summaries) are retained indefinitely.
              </p>
            )}
          </>
        ) : (
          <p>
            Check data retention is determined by your plan. Rollup aggregates
            are retained indefinitely.
          </p>
        )}
      </Card>

      <Card style="margin-top: 16px">
        <div class="section-header">
          <h3>Storage Overview</h3>
        </div>
        {loading ? (
          <p class="text-muted">Loading storage stats...</p>
        ) : stats ? (
          <dl class="retention-stats">
            <div class="retention-stat">
              <dt>Monitors</dt>
              <dd>{stats.monitorCount}</dd>
            </div>
            <div class="retention-stat">
              <dt>Total Checks</dt>
              <dd>{stats.totalChecks.toLocaleString()}</dd>
            </div>
            <div class="retention-stat">
              <dt>Oldest Check</dt>
              <dd>{formatAge(stats.oldestCheck)}</dd>
            </div>
            <div class="retention-stat">
              <dt>Latest Check</dt>
              <dd>{stats.newestCheck ? formatTime(stats.newestCheck) : '-'}</dd>
            </div>
          </dl>
        ) : (
          <p class="text-muted">Unable to load storage stats.</p>
        )}
      </Card>

      {retentionDays && (
        <Card style="margin-top: 16px">
          <div class="section-header">
            <h3>Data Lifecycle</h3>
          </div>
          <table class="data-table">
            <thead>
              <tr>
                <th>Data Type</th>
                <th>Retention</th>
                <th>Granularity</th>
              </tr>
            </thead>
            <tbody>
              <tr>
                <td>Raw checks</td>
                <td>{retentionDays} days</td>
                <td>Per-check (every interval)</td>
              </tr>
              <tr>
                <td>Hourly rollups</td>
                <td>Indefinite</td>
                <td>1 hour</td>
              </tr>
              <tr>
                <td>Daily rollups</td>
                <td>Indefinite</td>
                <td>1 day</td>
              </tr>
              <tr>
                <td>Alerts &amp; events</td>
                <td>Indefinite</td>
                <td>Per-event</td>
              </tr>
            </tbody>
          </table>
        </Card>
      )}
    </div>
  );
}

/* ─── Org Profile ─── */
function OrgTab() {
  const org = currentOrg.value;
  const [form, setForm] = useState({ name: org?.name || '', slug: org?.slug || '', oncall_display: org?.oncall_display || 'email' });
  const [saving, setSaving] = useState(false);
  const [orgSettings, setOrgSettings] = useState({});
  const [settingsLoading, setSettingsLoading] = useState(true);

  useEffect(() => {
    get('/org/settings').then(data => {
      setOrgSettings(data || {});
    }).catch(() => {}).finally(() => setSettingsLoading(false));
  }, []);

  async function save(e) {
    e.preventDefault();
    setSaving(true);
    try {
      await patch('/org', form);
      currentOrg.value = { ...currentOrg.value, ...form };
    } catch (_) {}
    setSaving(false);
  }

  async function toggleSetting(key) {
    const current = orgSettings[key] === 'true';
    const newVal = current ? 'false' : 'true';
    try {
      const updated = await put('/org/settings', { ...orgSettings, [key]: newVal });
      setOrgSettings(updated || { ...orgSettings, [key]: newVal });
    } catch (_) {}
  }

  return (
    <div>
      <h3>Organization</h3>
      <Card>
        <form onSubmit={save}>
          <div class="form-group">
            <label>Organization Name</label>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} />
          </div>
          <div class="form-group">
            <label>Slug</label>
            <input type="text" value={form.slug} onInput={e => setForm({ ...form, slug: e.target.value })} />
          </div>
          <div class="form-group">
            <label>On-Call Display</label>
            <select value={form.oncall_display} onChange={e => setForm({ ...form, oncall_display: e.target.value })}>
              <option value="email">Email</option>
              <option value="name">Name</option>
            </select>
          </div>
          <button type="submit" class="btn btn-primary" disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </form>
      </Card>

      <h3 style="margin-top: 24px">Alert & Monitor Settings</h3>
      <Card>
        {settingsLoading ? (
          <p class="text-muted">Loading settings...</p>
        ) : (
          <>
            <div class="form-group" style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px">
              <label style="display: flex; align-items: center; gap: 8px; cursor: pointer; margin: 0">
                <input type="checkbox" checked={orgSettings.mute_new_monitors === 'true'} onChange={() => toggleSetting('mute_new_monitors')} />
                Auto-mute new monitors
              </label>
              <span class="text-muted" style="font-size: 0.8rem">New monitors start muted. Unmute them when ready to receive alerts.</span>
            </div>
            <div class="form-group" style="display: flex; align-items: center; gap: 12px">
              <label style="display: flex; align-items: center; gap: 8px; cursor: pointer; margin: 0">
                <input type="checkbox" checked={orgSettings.incident_auto_resolve === 'true'} onChange={() => toggleSetting('incident_auto_resolve')} />
                Auto-resolve incidents when all linked alerts recover
              </label>
            </div>
          </>
        )}
      </Card>
    </div>
  );
}
