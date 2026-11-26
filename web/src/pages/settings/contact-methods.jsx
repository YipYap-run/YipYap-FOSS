import { useState, useEffect, useId, useRef } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, Spinner, useToast, ToastContainer } from '../../components/ui';
import { appMeta } from '../../state/auth';
import { TelegramContactConnect } from './telegram-link.jsx';
import { telegramContactMode, ownBotIntegrations, isPlatformTelegramMethod } from './contact-telegram-state.mjs';
import {
  viaOptions, viaValue, viaSource, applyVia, defaultVia, viaIntegration, fieldApplies,
  slackLookupResult, slackLookupError,
} from './contact-chat-app-state.mjs';

/*
 * Personal contact methods and notification rules (Settings, Contact methods).
 *
 * API contract (all under /api/v1):
 *  - GET  users/me/contact-method-options: {types:[{type, supported,
 *    paid_only, available, integration}], integrations:[{id, name, type,
 *    enabled, own_bot, source}], telegram_linking, limits:{max_methods,
 *    max_rules, max_delay_minutes, max_label_length, code_ttl_minutes}}.
 *  - Slack and Discord DMs send through a YipYap app install (source "app",
 *    config.via_install_id) or an org channel's own bot (source "own_bot",
 *    config.via_channel_id). With a Slack app install, Find me in Slack
 *    looks up the caller's own email (see contact-chat-app-state.mjs).
 *  - Telegram on the YipYap bot is added with Connect Telegram: POST
 *    telegram/link-codes {purpose: "contact_method"}, then the user sends
 *    the code to the bot in a private chat and the bot adds the method,
 *    verified (see contact-telegram-state.mjs). A typed chat ID is only for
 *    a channel on the org's own bot, which YipYap cannot read.
 *  - users/me/contact-methods: GET/POST, {id} GET/PATCH/DELETE, and POST
 *    {id}/send-code, {id}/verify {code}, {id}/test. Secret config fields come
 *    back as SENTINEL; sending SENTINEL back keeps the stored value. A
 *    config change resets verification.
 *  - users/me/notification-rules: GET/POST, {id} PATCH/DELETE. Rules are
 *    {contact_method_id, delay_minutes, urgency: any|high|low}. Deleting a
 *    method deletes its rules.
 *  - GET users/{id}/contact-methods (owner/admin): masked, read-only view
 *    {contact_methods:[{id,type,label,summary,verified}], notification_rules}.
 *  - Only verified methods are ever paged. Errors are {error}; plan gates
 *    are 403 {upgrade_required} and the API client shows the upgrade banner.
 */

export const SENTINEL = '••••••••';

const PHONE_HELP = 'Include the country code, for example +1 212 555 4240. US and Canada (+1) numbers only.';

const SLACK_MEMBER_HELP = 'In Slack, open your profile, choose the more menu, then Copy member ID.';
const DISCORD_USER_HELP = 'In Discord, open User Settings, Advanced, and turn on Developer Mode. Then right-click your name and choose Copy User ID. The YipYap bot can only message you if you share a server with it and allow direct messages from that server\'s members.';

// Form fields per contact method type. "integration" fields pick where a DM
// is sent from: for Slack and Discord (key "via") a YipYap app install or an
// org channel with its own bot, for Telegram an org channel. A field with
// "via" only applies to that sender ("app" or "own_bot").
export const METHOD_TYPES = {
  email: {
    label: 'Email',
    fields: [
      { key: 'address', label: 'Email address', input: 'email', required: true, autocomplete: 'email', placeholder: 'you@example.com' },
    ],
  },
  sms: {
    label: 'SMS',
    fields: [
      { key: 'phone', label: 'Mobile number', input: 'tel', required: true, autocomplete: 'tel', placeholder: '+1 212 555 4240', help: PHONE_HELP },
    ],
  },
  voice: {
    label: 'Phone call',
    note: 'The verification code is sent to this number by text message.',
    fields: [
      { key: 'phone', label: 'Phone number', input: 'tel', required: true, autocomplete: 'tel', placeholder: '+1 212 555 4240', help: PHONE_HELP },
    ],
  },
  ntfy: {
    label: 'ntfy push',
    fields: [
      { key: 'server_url', label: 'Server URL', input: 'url', placeholder: 'https://ntfy.sh', help: 'Leave blank to use ntfy.sh. Self-hosted servers must use https.' },
      { key: 'topic', label: 'Topic', input: 'text', required: true, placeholder: 'alex-pager-7f3k', help: 'Subscribe to this topic in the ntfy app. Anyone who knows a public topic name can read it, so pick one that is hard to guess or use an access token.' },
      { key: 'token', label: 'Access token', input: 'secret', help: 'Only needed for a protected topic.' },
    ],
  },
  pushover: {
    label: 'Pushover',
    fields: [
      { key: 'user_key', label: 'User key', input: 'text', required: true, help: 'Shown on your Pushover dashboard. 30 letters and digits.' },
      { key: 'api_token', label: 'Application token', input: 'secret', required: true, help: 'Create an application at pushover.net and paste its API token.' },
      { key: 'device', label: 'Device name', input: 'text', help: 'Leave blank to send to all your devices.' },
    ],
  },
  pagerduty: {
    label: 'PagerDuty',
    fields: [
      { key: 'routing_key', label: 'Routing key', input: 'secret', required: true, help: 'In PagerDuty, open a service, then Integrations, and copy the Events API v2 routing key.' },
    ],
  },
  webhook: {
    label: 'Webhook',
    fields: [
      { key: 'url', label: 'URL', input: 'url', required: true, placeholder: 'https://example.com/hooks/yipyap', help: 'YipYap posts a JSON body to this URL. Private and local addresses are refused.' },
      { key: 'secret', label: 'Signing secret', input: 'secret', help: 'Optional. When set, each request carries an X-YipYap-Signature HMAC-SHA256 header.' },
    ],
  },
  slack_dm: {
    label: 'Slack DM',
    fields: [
      { key: 'via', label: 'Send through', input: 'integration', integration: 'slack', required: true },
      { key: 'slack_user_id', label: 'Slack member ID', input: 'text', required: true, placeholder: 'U0123ABCD', help: SLACK_MEMBER_HELP, findMe: true },
    ],
  },
  discord_dm: {
    label: 'Discord DM',
    fields: [
      { key: 'via', label: 'Send through', input: 'integration', integration: 'discord', required: true },
      { key: 'user_id', label: 'Discord user ID', input: 'text', required: true, via: 'app', placeholder: '123456789012345678', help: DISCORD_USER_HELP },
      { key: 'dm_channel_id', label: 'DM channel ID', input: 'text', required: true, via: 'own_bot', placeholder: '123456789012345678', help: 'Open a direct message with your organization\'s bot, turn on Developer Mode in Discord settings, then right-click the conversation and choose Copy Channel ID.' },
    ],
  },
  // On the YipYap bot, Telegram is added with Connect Telegram instead of
  // these fields. They are for a channel on the org's own bot only.
  telegram: {
    label: 'Telegram',
    fields: [
      { key: 'via_channel_id', label: 'Send through', input: 'integration', integration: 'telegram', required: true },
      { key: 'chat_id', label: 'Your Telegram user ID', input: 'text', required: true, placeholder: '123456789', help: 'A number. In a private chat it is also the chat ID. YipYap only reads messages sent to its own bot, so it cannot look this up for you through your organization\'s bot. Send /start to that bot first so it is allowed to message you.' },
    ],
  },
};

const URGENCY_OPTIONS = [
  { value: 'any', label: 'All alerts' },
  { value: 'high', label: 'Critical alerts only' },
  { value: 'low', label: 'Warning and info alerts only' },
];

export function typeLabel(t) {
  return METHOD_TYPES[t]?.label || t;
}

export function urgencyLabel(u) {
  return URGENCY_OPTIONS.find(o => o.value === u)?.label || u;
}

export function delayLabel(minutes) {
  const m = Number(minutes) || 0;
  if (m === 0) return 'Immediately';
  if (m < 60) return `After ${m} minute${m === 1 ? '' : 's'}`;
  const h = Math.floor(m / 60);
  const rest = m % 60;
  const hours = `${h} hour${h === 1 ? '' : 's'}`;
  return rest ? `After ${hours} ${rest} min` : `After ${hours}`;
}

// The owner's own view of where a method delivers. Secrets are never shown
// (the server already replaced them with SENTINEL).
export function methodDestination(m, integrations = []) {
  const c = m.config || {};
  const via = id => integrations.find(i => i.id === id && i.source !== 'app')?.name;
  const sender = () => viaIntegration(integrations, c)?.name;
  switch (m.type) {
    case 'email': return c.address || '';
    case 'sms':
    case 'voice': return c.phone || '';
    case 'ntfy': return `${c.topic || ''} on ${(c.server_url || 'https://ntfy.sh').replace(/^https:\/\//, '')}`;
    case 'pushover': return c.device ? `Device ${c.device}` : 'All devices';
    case 'pagerduty': return 'PagerDuty service';
    case 'webhook': return (c.url || '').replace(/^https?:\/\//, '');
    case 'slack_dm': return `${c.slack_user_id || ''}${sender() ? ` via ${sender()}` : ''}`;
    case 'discord_dm': return `${c.user_id ? `User ${c.user_id}` : `DM ${c.dm_channel_id || ''}`}${sender() ? ` via ${sender()}` : ''}`;
    case 'telegram': return `Chat ${c.chat_id || ''}${via(c.via_channel_id) ? ` via ${via(c.via_channel_id)}` : ''}`;
    default: return m.summary || '';
  }
}

// Which alert urgencies reach the user through at least one verified method.
export function coverage(methods, rules) {
  const verified = new Set(methods.filter(m => m.verified).map(m => m.id));
  const live = rules.filter(r => verified.has(r.contact_method_id));
  return {
    high: live.some(r => r.urgency === 'any' || r.urgency === 'high'),
    low: live.some(r => r.urgency === 'any' || r.urgency === 'low'),
  };
}

function sortRules(rules) {
  return [...rules].sort((a, b) => a.delay_minutes - b.delay_minutes || String(a.urgency).localeCompare(String(b.urgency)));
}

function VerifiedBadge({ method }) {
  if (method.verified) return <span class="badge badge-sm badge-up">Verified</span>;
  if (method.verification_pending) return <span class="badge badge-sm badge-info">Code sent</span>;
  return <span class="badge badge-sm badge-warning">Not verified</span>;
}

/* ─── Tab ─── */

export function ContactMethodsTab() {
  const [options, setOptions] = useState(null);
  const [methods, setMethods] = useState([]);
  const [rules, setRules] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [editing, setEditing] = useState(null); // null, {} for new, or a method
  const [verifying, setVerifying] = useState(null);
  const [deleting, setDeleting] = useState(null);
  const [ruleEditing, setRuleEditing] = useState(null); // null, {} for new, or a rule
  const [busy, setBusy] = useState('');
  const { toasts, toast } = useToast();

  async function load() {
    setError(null);
    try {
      const [o, m, r] = await Promise.all([
        get('/users/me/contact-method-options'),
        get('/users/me/contact-methods'),
        get('/users/me/notification-rules'),
      ]);
      setOptions(o);
      setMethods(m || []);
      setRules(r || []);
    } catch (err) {
      setError(err);
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function sendTest(m) {
    setBusy(`test:${m.id}`);
    try {
      await post(`/users/me/contact-methods/${m.id}/test`, {});
      toast(`Test sent to ${m.label}`);
    } catch (err) {
      toast(err.message || 'Could not send the test', 'error');
    }
    setBusy('');
  }

  async function confirmDelete() {
    const m = deleting;
    setBusy(`delete:${m.id}`);
    try {
      await del(`/users/me/contact-methods/${m.id}`);
      toast(`${m.label} removed`);
      setDeleting(null);
      await load();
    } catch (err) {
      toast(err.message || 'Could not remove the contact method', 'error');
    }
    setBusy('');
  }

  async function deleteRule(rule) {
    setBusy(`rule:${rule.id}`);
    try {
      await del(`/users/me/notification-rules/${rule.id}`);
      toast('Rule removed');
      await load();
    } catch (err) {
      toast(err.message || 'Could not remove the rule', 'error');
    }
    setBusy('');
  }

  if (loading) return <LoadingPage />;
  if (error && !options) {
    return (
      <div class="card">
        <div class="section-header"><h3>Contact methods</h3></div>
        <ErrorMessage error={error} onRetry={() => { setLoading(true); load(); }} />
      </div>
    );
  }

  const limits = options?.limits || {};
  const integrations = options?.integrations || [];
  const methodById = Object.fromEntries(methods.map(m => [m.id, m]));
  const cov = coverage(methods, rules);
  const atMethodLimit = limits.max_methods && methods.length >= limits.max_methods;
  const atRuleLimit = limits.max_rules && rules.length >= limits.max_rules;

  return (
    <div>
      <ToastContainer toasts={toasts} />

      <section class="card" aria-labelledby="cm-methods-heading">
        <div class="section-header">
          <h3 id="cm-methods-heading">Contact methods</h3>
          <button type="button" class="btn btn-sm btn-primary" onClick={() => setEditing({})}
            disabled={atMethodLimit}>
            Add contact method
          </button>
        </div>
        <p class="text-muted cm-intro">
          Where YipYap reaches you when you are paged. A method is only used after you confirm it with a code.
          {atMethodLimit ? ` You have reached the limit of ${limits.max_methods} methods.` : ''}
        </p>
        {methods.length === 0 ? (
          <p class="text-muted">You have no contact methods yet. Add one so on-call pages can reach you.</p>
        ) : (
          <ul class="cm-list">
            {methods.map(m => (
              <li key={m.id} class="settings-item cm-item">
                <div class="cm-item-main">
                  <div class="cm-item-title">
                    <strong>{m.label}</strong>
                    <span class="text-muted">{typeLabel(m.type)}</span>
                    <VerifiedBadge method={m} />
                  </div>
                  <p class="cm-item-dest text-muted">{methodDestination(m, integrations)}</p>
                  {!m.verified && (
                    <p class="cm-item-dest text-muted">Not used for paging until verified.</p>
                  )}
                </div>
                <div class="btn-group cm-item-actions">
                  {m.verified ? (
                    <button type="button" class="btn btn-xs" onClick={() => sendTest(m)}
                      disabled={busy === `test:${m.id}`} aria-label={`Send a test to ${m.label}`}>
                      {busy === `test:${m.id}` ? 'Sending...' : 'Send test'}
                    </button>
                  ) : (
                    <button type="button" class="btn btn-xs btn-primary" onClick={() => setVerifying(m)}
                      aria-label={`Verify ${m.label}`}>
                      Verify
                    </button>
                  )}
                  <button type="button" class="btn btn-xs" onClick={() => setEditing(m)}
                    aria-label={`Edit ${m.label}`}>
                    Edit
                  </button>
                  <button type="button" class="btn btn-xs btn-danger" onClick={() => setDeleting(m)}
                    aria-label={`Remove ${m.label}`}>
                    Remove
                  </button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </section>

      <section class="card" style="margin-top: 16px" aria-labelledby="cm-rules-heading">
        <div class="section-header">
          <h3 id="cm-rules-heading">Notification rules</h3>
          <button type="button" class="btn btn-sm btn-primary" onClick={() => setRuleEditing({})}
            disabled={methods.length === 0 || atRuleLimit}>
            Add rule
          </button>
        </div>
        <p class="text-muted cm-intro">
          When you are paged, each rule contacts you after its delay. For example, a text right away and a
          phone call after 5 minutes. Later rules are skipped once the alert is acknowledged.
          Critical alerts are high urgency; warning and info alerts are low urgency.
        </p>
        <CoverageWarning cov={cov} hasMethods={methods.length > 0} />
        {rules.length === 0 ? (
          <p class="text-muted">
            {methods.length === 0 ? 'Add a contact method first, then a rule that uses it.' : 'You have no notification rules yet.'}
          </p>
        ) : (
          <ol class="cm-list">
            {sortRules(rules).map(r => {
              const m = methodById[r.contact_method_id];
              const skipped = !m || !m.verified;
              return (
                <li key={r.id} class="settings-item cm-item">
                  <div class="cm-item-main">
                    <div class="cm-item-title">
                      <strong>{delayLabel(r.delay_minutes)}</strong>
                      <span>{m ? `${m.label} (${typeLabel(m.type)})` : 'Removed method'}</span>
                    </div>
                    <p class="cm-item-dest text-muted">{urgencyLabel(r.urgency)}</p>
                    {skipped && (
                      <p class="cm-item-dest cm-skip">Skipped until {m ? m.label : 'the method'} is verified.</p>
                    )}
                  </div>
                  <div class="btn-group cm-item-actions">
                    <button type="button" class="btn btn-xs" onClick={() => setRuleEditing(r)}
                      aria-label={`Edit rule: ${delayLabel(r.delay_minutes)}, ${m?.label || 'removed method'}`}>
                      Edit
                    </button>
                    <button type="button" class="btn btn-xs btn-danger" onClick={() => deleteRule(r)}
                      disabled={busy === `rule:${r.id}`}
                      aria-label={`Remove rule: ${delayLabel(r.delay_minutes)}, ${m?.label || 'removed method'}`}>
                      Remove
                    </button>
                  </div>
                </li>
              );
            })}
          </ol>
        )}
      </section>

      <MethodModal
        method={editing}
        options={options}
        onClose={() => setEditing(null)}
        onConnected={() => load()}
        onSaved={async (saved, isNew) => {
          setEditing(null);
          await load();
          if (!saved.verified) {
            setVerifying(saved);
          } else {
            toast(isNew ? `${saved.label} added` : `${saved.label} saved`);
          }
        }}
      />

      <VerifyModal
        method={verifying}
        codeTTL={limits.code_ttl_minutes || 15}
        onClose={() => { setVerifying(null); load(); }}
        onVerified={async m => {
          setVerifying(null);
          toast(`${m.label} verified`);
          await load();
        }}
      />

      <RuleModal
        rule={ruleEditing}
        methods={methods}
        maxDelay={limits.max_delay_minutes || 1440}
        onClose={() => setRuleEditing(null)}
        onSaved={async isNew => {
          setRuleEditing(null);
          toast(isNew ? 'Rule added' : 'Rule saved');
          await load();
        }}
      />

      <Modal open={!!deleting} onClose={() => setDeleting(null)} title="Remove contact method">
        {deleting && (
          <DeleteBody method={deleting} rules={rules} methods={methods} busy={busy === `delete:${deleting.id}`}
            onCancel={() => setDeleting(null)} onConfirm={confirmDelete} />
        )}
      </Modal>
    </div>
  );
}

function CoverageWarning({ cov, hasMethods }) {
  if (cov.high && cov.low) return null;
  let msg;
  if (!cov.high && !cov.low) {
    msg = hasMethods
      ? 'No rule uses a verified contact method, so pages cannot reach you. Verify a method and add a rule for it.'
      : 'Pages cannot reach you yet. Add and verify a contact method, then add a rule.';
  } else if (!cov.high) {
    msg = 'Critical alerts cannot reach you. Add a rule for all alerts or critical alerts that uses a verified method.';
  } else {
    msg = 'Warning and info alerts cannot reach you. Add a rule for all alerts or warning and info alerts if you want them.';
  }
  return <div class="form-warning cm-coverage" role="status">{msg}</div>;
}

function DeleteBody({ method, rules, methods, busy, onCancel, onConfirm }) {
  const ruleCount = rules.filter(r => r.contact_method_id === method.id).length;
  const after = coverage(methods.filter(m => m.id !== method.id), rules.filter(r => r.contact_method_id !== method.id));
  return (
    <div>
      <p>
        Remove <strong>{method.label}</strong> ({typeLabel(method.type)})?
        {ruleCount > 0 && ` Its ${ruleCount} notification rule${ruleCount === 1 ? '' : 's'} will be removed too.`}
      </p>
      {method.verified && (!after.high || !after.low) && (
        <div class="form-warning" style="margin: 12px 0">
          {!after.high
            ? 'After this, critical alerts will not reach you.'
            : 'After this, warning and info alerts will not reach you.'}
        </div>
      )}
      <div class="btn-group" style="margin-top: 16px">
        <button type="button" class="btn btn-danger" onClick={onConfirm} disabled={busy}>
          {busy ? 'Removing...' : 'Remove'}
        </button>
        <button type="button" class="btn" onClick={onCancel}>Cancel</button>
      </div>
    </div>
  );
}

/* ─── Add / edit method ─── */

function emptyConfig(type, integrations) {
  const out = {};
  for (const f of METHOD_TYPES[type]?.fields || []) {
    if (f.key === 'via') continue;
    out[f.key] = '';
  }
  const kind = METHOD_TYPES[type]?.fields.find(f => f.key === 'via')?.integration;
  return kind ? applyVia(out, defaultVia(integrations, kind)) : out;
}

// configForSave drops blank optional fields and fields for the other
// sender, and keeps stored secrets (sent back as SENTINEL) unless the user
// typed a new value or cleared it. "via" becomes via_install_id or
// via_channel_id.
export function configForSave(type, config, { clearSecrets = {} } = {}) {
  const out = {};
  for (const f of METHOD_TYPES[type]?.fields || []) {
    if (f.key === 'via') {
      if (config.via_install_id) out.via_install_id = config.via_install_id;
      else if (config.via_channel_id) out.via_channel_id = config.via_channel_id;
      continue;
    }
    if (!fieldApplies(f, config)) continue;
    let v = config[f.key];
    if (typeof v === 'string') v = v.trim();
    if (f.input === 'secret' && clearSecrets[f.key]) continue;
    if (v === '' || v == null) continue;
    out[f.key] = v;
  }
  return out;
}

function MethodModal({ method, options, onClose, onSaved, onConnected }) {
  const open = method !== null;
  const isNew = open && !method.id;
  const formId = useId();
  const [type, setType] = useState('email');
  const [label, setLabel] = useState('');
  const [config, setConfig] = useState(emptyConfig('email'));
  const [clearSecrets, setClearSecrets] = useState({});
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);
  const errRef = useRef(null);

  const types = (options?.types || []).filter(t => t.supported);
  const integrations = options?.integrations || [];

  useEffect(() => {
    if (!open) return;
    setError('');
    setClearSecrets({});
    if (isNew) {
      const first = types.find(t => t.available)?.type || 'email';
      setType(first);
      setLabel('');
      setConfig(emptyConfig(first, integrations));
    } else {
      setType(method.type);
      setLabel(method.label || '');
      setConfig({ ...emptyConfig(method.type), ...(method.config || {}) });
    }
  }, [method]);

  useEffect(() => {
    if (error && errRef.current) errRef.current.focus();
  }, [error]);

  function changeType(t) {
    setType(t);
    setConfig(emptyConfig(t, integrations));
    setError('');
  }

  const typeOpt = types.find(t => t.type === type);
  const def = METHOD_TYPES[type];
  const telegram = type === 'telegram';
  // New Telegram methods: Connect Telegram, a typed ID on the org's own
  // bot, or nothing. An existing one on the YipYap bot keeps its chat; only
  // its name can change.
  const tgMode = telegram && isNew ? telegramContactMode(options) : null;
  const tgPlatform = telegram && !isNew && isPlatformTelegramMethod(method);
  const showFields = !tgPlatform && tgMode !== 'connect' && tgMode !== 'unavailable';
  const needsIntegration = showFields && def?.fields.some(f => f.input === 'integration' && f.required);
  const usable = telegram
    ? integrations.filter(i => i.type === 'telegram' && (i.own_bot || (!isNew && i.id === method.config?.via_channel_id)))
    : integrations.filter(i => i.type === typeOpt?.integration);
  const blockedByIntegration = needsIntegration && !usable.some(i => i.enabled);
  const blockedByPlan = isNew && typeOpt && !typeOpt.available;
  const lockedTypes = types.filter(t => !t.available);
  const integrationName = { slack: 'Slack', discord: 'Discord', telegram: 'Telegram' }[typeOpt?.integration] || '';
  const appSender = viaSource(config) === 'app' ? viaIntegration(integrations, config) : undefined;

  async function submit(e) {
    e.preventDefault();
    // Connect Telegram saves through the bot, not this form.
    if (tgMode === 'connect') return;
    setSaving(true);
    setError('');
    const cfg = configForSave(type, config, { clearSecrets });
    try {
      let saved;
      if (isNew) {
        saved = await post('/users/me/contact-methods', { type, label, config: cfg });
      } else if (tgPlatform) {
        // The chat was set by Connect Telegram; only the name changes.
        saved = await patch(`/users/me/contact-methods/${method.id}`, { label });
      } else {
        saved = await patch(`/users/me/contact-methods/${method.id}`, { label, config: cfg });
      }
      setSaving(false);
      onSaved(saved, isNew);
    } catch (err) {
      setSaving(false);
      setError(err.message || 'Could not save the contact method');
    }
  }

  const title = isNew ? 'Add contact method' : `Edit ${method?.label || 'contact method'}`;
  return (
    <Modal open={open} onClose={onClose} title={title}>
      {open && (
        <form onSubmit={submit}>
          {isNew ? (
            <div class="form-group">
              <label for={`${formId}-type`}>Type</label>
              <select id={`${formId}-type`} value={type} onChange={e => changeType(e.target.value)}
                aria-describedby={lockedTypes.length ? `${formId}-type-help` : undefined}>
                {types.map(t => (
                  <option key={t.type} value={t.type} disabled={!t.available}>
                    {typeLabel(t.type)}{t.available ? '' : ' (paid plan)'}
                  </option>
                ))}
              </select>
              {lockedTypes.length > 0 && (
                <p class="form-help" id={`${formId}-type-help`}>
                  {lockedTypes.map(t => typeLabel(t.type)).join(', ')} need a paid plan.{' '}
                  {appMeta.value?.billing_enabled && <a href="/settings/billing">See plans</a>}
                </p>
              )}
            </div>
          ) : (
            <p class="text-muted" style="margin-top: 0">Type: {typeLabel(type)}</p>
          )}

          {tgMode === 'connect' ? (
            blockedByPlan ? (
              <div class="btn-group">
                <button type="button" class="btn" onClick={onClose}>Cancel</button>
              </div>
            ) : (
              <TelegramContactConnect botUsername={appMeta.value?.telegram_bot_username}
                onLinked={() => onConnected && onConnected()} onClose={onClose} />
            )
          ) : (<>
          {tgMode === 'unavailable' && (
            <div class="form-warning" role="status" style="margin-bottom: 12px">
              Telegram is not set up on this server. Ask your YipYap operator to configure the Telegram bot.
            </div>
          )}

          <div class="form-group">
            <label for={`${formId}-label`}>Name <span class="form-optional">optional</span></label>
            <input id={`${formId}-label`} type="text" value={label} maxLength={options?.limits?.max_label_length || 100}
              placeholder={def?.label} onInput={e => setLabel(e.target.value)} aria-describedby={`${formId}-label-help`} />
            <p class="form-help" id={`${formId}-label-help`}>Shown in your rules and to admins, for example Work phone.</p>
          </div>

          {def?.note && <p class="text-muted cm-note">{def.note}</p>}

          {tgPlatform && (
            <p class="form-help cm-note">
              Connected to Telegram chat {method.config?.chat_id}. To use a different Telegram account, remove this
              method and add Telegram again with Connect Telegram.
            </p>
          )}

          {showFields && def?.fields.filter(f => fieldApplies(f, config)).map(f => (
            <ConfigField key={`${type}-${f.key}`} formId={formId} field={f}
              value={f.key === 'via' ? viaValue(config) : (config[f.key] ?? '')}
              integrations={usable} storedSecret={!isNew && method.config?.[f.key] === SENTINEL}
              cleared={!!clearSecrets[f.key]}
              onClear={v => setClearSecrets({ ...clearSecrets, [f.key]: v })}
              onInput={v => setConfig(c => (f.key === 'via' ? applyVia(c, v) : { ...c, [f.key]: v }))}>
              {f.findMe && appSender && (
                <SlackFindMe key={appSender.id} formId={formId} install={appSender}
                  onFound={id => setConfig(c => ({ ...c, slack_user_id: id }))} />
              )}
            </ConfigField>
          ))}

          {blockedByIntegration && (
            <div class="form-warning" role="status" style="margin-bottom: 12px">
              Your organization has no {integrationName} connection to send through. Ask an admin to
              add the YipYap {integrationName} app or a {integrationName} channel with its own bot under Settings, Channels.
            </div>
          )}

          {!isNew && !tgPlatform && (
            <p class="form-help" style="margin-bottom: 12px">Changing where this method delivers means verifying it again.</p>
          )}

          {error && (
            <div class="form-error" role="alert" tabIndex={-1} ref={errRef}>{error}</div>
          )}
          <div class="btn-group">
            <button type="submit" class="btn btn-primary" disabled={saving || blockedByPlan || blockedByIntegration || tgMode === 'unavailable'}>
              {saving ? 'Saving...' : isNew ? 'Add and verify' : 'Save'}
            </button>
            <button type="button" class="btn" onClick={onClose}>Cancel</button>
          </div>
          </>)}
        </form>
      )}
    </Modal>
  );
}

function ConfigField({ formId, field, value, integrations, storedSecret, cleared, onClear, onInput, children }) {
  const id = `${formId}-${field.key}`;
  const helpId = field.help ? `${id}-help` : undefined;
  const [show, setShow] = useState(false);
  const label = (
    <label for={id} class={field.required ? 'required' : ''}>
      {field.label}
      {!field.required && field.input !== 'integration' && <span class="form-optional">optional</span>}
    </label>
  );

  if (field.input === 'integration' && field.key === 'via') {
    const opts = viaOptions(integrations, field.integration);
    const hasApp = opts.some(o => o.source === 'app');
    const viaHelp = `${id}-help`;
    return (
      <div class="form-group">
        {label}
        <select id={id} value={value} required={field.required} onChange={e => onInput(e.target.value)}
          aria-describedby={viaHelp}>
          <option value="">Choose where to send from</option>
          {opts.map(o => (
            <option key={o.value} value={o.value} disabled={!o.enabled}>{o.label}</option>
          ))}
        </select>
        <p class="form-help" id={viaHelp}>
          {hasApp
            ? `The ${field.integration === 'slack' ? 'Slack' : 'Discord'} app needs no setup from you. An own bot is a channel your organization set up with its own bot token.`
            : 'Channels your organization set up with its own bot token.'}
        </p>
      </div>
    );
  }

  if (field.input === 'integration') {
    return (
      <div class="form-group">
        {label}
        <select id={id} value={value} required={field.required} onChange={e => onInput(e.target.value)}
          aria-describedby={helpId}>
          {field.required
            ? <option value="">Choose an integration</option>
            : <option value="">YipYap bot</option>}
          {integrations.map(i => (
            <option key={i.id} value={i.id} disabled={!i.enabled}>
              {i.name}{i.enabled ? '' : ' (disabled)'}
            </option>
          ))}
        </select>
        {field.integration === 'telegram' && (
          <p class="form-help">Telegram channels that send through your organization's own bot.</p>
        )}
      </div>
    );
  }

  if (field.input === 'secret') {
    return (
      <div class="form-group">
        {label}
        <div class="secret-field">
          <input id={id} type={show ? 'text' : 'password'} autocomplete="off" spellcheck={false}
            value={value === SENTINEL ? '' : value}
            placeholder={storedSecret ? (cleared ? 'Will be removed' : 'Saved. Type a new value to replace it.') : ''}
            required={field.required && !storedSecret} disabled={cleared}
            onInput={e => onInput(e.target.value === '' && storedSecret ? SENTINEL : e.target.value)}
            aria-describedby={helpId} />
          <button type="button" class="secret-toggle" onClick={() => setShow(!show)}
            aria-pressed={show} aria-label={show ? `Hide ${field.label}` : `Show ${field.label}`}>
            {show ? 'Hide' : 'Show'}
          </button>
        </div>
        {field.help && <p class="form-help" id={helpId}>{field.help}</p>}
        {storedSecret && !field.required && (
          <div class="form-check" style="margin-top: 4px">
            <label>
              <input type="checkbox" checked={cleared} onChange={e => onClear(e.target.checked)} />
              Remove the saved {field.label.toLowerCase()}
            </label>
          </div>
        )}
      </div>
    );
  }

  return (
    <div class="form-group">
      {label}
      <input id={id} type={field.input === 'tel' ? 'tel' : field.input === 'email' ? 'email' : field.input === 'url' ? 'url' : 'text'}
        value={value} required={field.required} placeholder={field.placeholder || ''}
        autocomplete={field.autocomplete || 'off'} spellcheck={false}
        inputMode={field.input === 'tel' ? 'tel' : undefined}
        onInput={e => onInput(e.target.value)} aria-describedby={helpId} />
      {field.help && <p class="form-help" id={helpId}>{field.help}</p>}
      {children}
    </div>
  );
}

// SlackFindMe fills the Slack member ID by looking up the signed-in user's
// own YipYap email in the chosen workspace. The member ID field stays as
// the fallback.
function SlackFindMe({ formId, install, onFound }) {
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState(null); // {found, message}
  const [error, setError] = useState('');
  const statusId = `${formId}-findme-status`;

  async function lookup() {
    setBusy(true);
    setError('');
    setResult(null);
    try {
      const res = await get(`/integrations/slack/installations/${encodeURIComponent(install.id)}/me`);
      const r = slackLookupResult(res, install.name);
      setResult(r);
      if (r.found) onFound(r.id);
    } catch (err) {
      setError(slackLookupError(err));
    }
    setBusy(false);
  }

  return (
    <div class="cm-findme" style="margin-top: 8px">
      <button type="button" class="btn btn-sm" onClick={lookup} disabled={busy}
        aria-describedby={statusId}>
        {busy ? 'Looking you up...' : 'Find me in Slack'}
      </button>
      <p class="form-help" id={statusId} role="status" aria-live="polite">
        {result ? result.message : 'Looks up your YipYap account email in this workspace.'}
      </p>
      {error && <div class="form-error" role="alert">{error}</div>}
    </div>
  );
}

/* ─── Verify ─── */

const RESEND_SECONDS = 30;

function VerifyModal({ method, codeTTL, onClose, onVerified }) {
  const open = !!method;
  const formId = useId();
  const [sent, setSent] = useState(false);
  const [sending, setSending] = useState(false);
  const [code, setCode] = useState('');
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const [wait, setWait] = useState(0);
  const codeRef = useRef(null);

  useEffect(() => {
    if (!open) return;
    setSent(!!method.verification_pending);
    setCode('');
    setError('');
    setWait(0);
  }, [method]);

  useEffect(() => {
    if (wait <= 0) return undefined;
    const t = setTimeout(() => setWait(w => w - 1), 1000);
    return () => clearTimeout(t);
  }, [wait]);

  async function sendCode() {
    setSending(true);
    setError('');
    try {
      await post(`/users/me/contact-methods/${method.id}/send-code`, {});
      setSent(true);
      setWait(RESEND_SECONDS);
      setTimeout(() => codeRef.current && codeRef.current.focus(), 0);
    } catch (err) {
      const m = /wait (\d+) seconds/.exec(err.message || '');
      if (err.status === 429 && m) {
        setWait(Number(m[1]));
        setSent(true);
      } else {
        setError(err.message || 'Could not send the code');
      }
    }
    setSending(false);
  }

  async function verify(e) {
    e.preventDefault();
    const clean = code.replace(/\s+/g, '');
    if (!/^[0-9]{6}$/.test(clean)) {
      setError('Enter the 6-digit code.');
      return;
    }
    setChecking(true);
    setError('');
    try {
      const m = await post(`/users/me/contact-methods/${method.id}/verify`, { code: clean });
      setChecking(false);
      onVerified(m || method);
    } catch (err) {
      setChecking(false);
      setError(err.message || 'Could not verify the code');
    }
  }

  let how = '';
  if (method) {
    const dest = methodDestination(method);
    if (method.type === 'voice') how = `by text message to ${dest}`;
    else if (method.type === 'email' || method.type === 'sms') how = `to ${dest}`;
    else how = `through ${typeLabel(method.type)}`;
  }
  return (
    <Modal open={open} onClose={onClose} title={`Verify ${method?.label || 'contact method'}`}>
      {open && (
        <div>
          <p style="margin-top: 0">
            We send a 6-digit code {how}. It expires
            in {codeTTL} minutes. This method is not used for paging until you enter it.
          </p>
          {!sent ? (
            <>
              {error && <div class="form-error" role="alert">{error}</div>}
              <div class="btn-group">
                <button type="button" class="btn btn-primary" onClick={sendCode} disabled={sending}>
                  {sending ? 'Sending...' : 'Send code'}
                </button>
                <button type="button" class="btn" onClick={onClose}>Later</button>
              </div>
            </>
          ) : (
            <form onSubmit={verify}>
              <div class="form-group">
                <label for={`${formId}-code`}>Verification code</label>
                <input id={`${formId}-code`} ref={codeRef} type="text" inputMode="numeric"
                  autocomplete="one-time-code" maxLength={7} value={code}
                  onInput={e => setCode(e.target.value)} required
                  aria-invalid={error ? 'true' : undefined}
                  aria-describedby={error ? `${formId}-err` : undefined} />
              </div>
              {error && <div class="form-error" id={`${formId}-err`} role="alert">{error}</div>}
              <div class="btn-group">
                <button type="submit" class="btn btn-primary" disabled={checking}>
                  {checking ? 'Checking...' : 'Verify'}
                </button>
                <button type="button" class="btn" onClick={sendCode} disabled={sending || wait > 0}>
                  {sending ? 'Sending...' : wait > 0 ? `Send again in ${wait}s` : 'Send a new code'}
                </button>
              </div>
            </form>
          )}
        </div>
      )}
    </Modal>
  );
}

/* ─── Rules ─── */

function RuleModal({ rule, methods, maxDelay, onClose, onSaved }) {
  const open = rule !== null;
  const isNew = open && !rule.id;
  const formId = useId();
  const [methodId, setMethodId] = useState('');
  const [delay, setDelay] = useState('0');
  const [urgency, setUrgency] = useState('any');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!open) return;
    setError('');
    if (isNew) {
      setMethodId((methods.find(m => m.verified) || methods[0])?.id || '');
      setDelay('0');
      setUrgency('any');
    } else {
      setMethodId(rule.contact_method_id);
      setDelay(String(rule.delay_minutes));
      setUrgency(rule.urgency);
    }
  }, [rule]);

  const selected = methods.find(m => m.id === methodId);
  const delayNum = Number(delay);
  const delayValid = delay !== '' && Number.isInteger(delayNum) && delayNum >= 0 && delayNum <= maxDelay;

  async function submit(e) {
    e.preventDefault();
    if (!delayValid) {
      setError(`Delay must be a whole number of minutes from 0 to ${maxDelay}.`);
      return;
    }
    setSaving(true);
    setError('');
    const body = { contact_method_id: methodId, delay_minutes: delayNum, urgency };
    try {
      if (isNew) await post('/users/me/notification-rules', body);
      else await patch(`/users/me/notification-rules/${rule.id}`, body);
      setSaving(false);
      onSaved(isNew);
    } catch (err) {
      setSaving(false);
      setError(err.message || 'Could not save the rule');
    }
  }

  return (
    <Modal open={open} onClose={onClose} title={isNew ? 'Add notification rule' : 'Edit notification rule'}>
      {open && (
        <form onSubmit={submit}>
          <div class="form-group">
            <label for={`${formId}-method`}>Contact method</label>
            <select id={`${formId}-method`} value={methodId} onChange={e => setMethodId(e.target.value)} required
              aria-describedby={selected && !selected.verified ? `${formId}-unverified` : undefined}>
              {methods.map(m => (
                <option key={m.id} value={m.id}>
                  {m.label} ({typeLabel(m.type)}){m.verified ? '' : ', not verified'}
                </option>
              ))}
            </select>
            {selected && !selected.verified && (
              <p class="form-help" id={`${formId}-unverified`}>
                This rule is skipped until {selected.label} is verified.
              </p>
            )}
          </div>
          <div class="form-group">
            <label for={`${formId}-delay`}>Minutes after the page</label>
            <input id={`${formId}-delay`} type="number" min="0" max={maxDelay} step="1" inputMode="numeric"
              value={delay} onInput={e => setDelay(e.target.value)} required
              aria-invalid={delayValid ? undefined : 'true'} aria-describedby={`${formId}-delay-help`} />
            <p class="form-help" id={`${formId}-delay-help`}>
              0 contacts you right away. {delayValid ? `${delayLabel(delayNum)}.` : ''}
            </p>
          </div>
          <div class="form-group">
            <label for={`${formId}-urgency`}>Applies to</label>
            <select id={`${formId}-urgency`} value={urgency} onChange={e => setUrgency(e.target.value)}>
              {URGENCY_OPTIONS.map(o => <option key={o.value} value={o.value}>{o.label}</option>)}
            </select>
          </div>
          {error && <div class="form-error" role="alert">{error}</div>}
          <div class="btn-group">
            <button type="submit" class="btn btn-primary" disabled={saving || !methodId}>
              {saving ? 'Saving...' : isNew ? 'Add rule' : 'Save'}
            </button>
            <button type="button" class="btn" onClick={onClose}>Cancel</button>
          </div>
        </form>
      )}
    </Modal>
  );
}

/* ─── Admin read-only view of a member ─── */

// MemberPagingModal shows an owner or admin how a member will be paged. It
// is read-only: only the member can change their own methods.
export function MemberPagingModal({ member, onClose }) {
  const open = !!member;
  const [data, setData] = useState(null);
  const [error, setError] = useState(null);

  async function load() {
    setData(null);
    setError(null);
    try {
      setData(await get(`/users/${member.id}/contact-methods`));
    } catch (err) {
      setError(err);
    }
  }
  useEffect(() => { if (open) load(); }, [member]);

  const name = member ? (member.name || member.email) : '';
  const methods = data?.contact_methods || [];
  const rules = sortRules(data?.notification_rules || []);
  const byId = Object.fromEntries(methods.map(m => [m.id, m]));
  const cov = coverage(methods, rules);

  return (
    <Modal open={open} onClose={onClose} title={`How ${name} is paged`}>
      {open && (
        <div>
          {error ? (
            <ErrorMessage error={error} onRetry={load} />
          ) : !data ? (
            <div class="steps-loading" role="status"><Spinner /><span class="sr-only">Loading</span></div>
          ) : (
            <>
              {(!cov.high || !cov.low) && (
                <div class="form-warning" style="margin-bottom: 12px">
                  {!cov.high && !cov.low
                    ? `${name} has no verified way to be paged.`
                    : !cov.high
                      ? `Critical alerts cannot reach ${name}.`
                      : `Warning and info alerts cannot reach ${name}.`}
                </div>
              )}
              <h4 class="cm-subheading">Contact methods</h4>
              {methods.length === 0 ? (
                <p class="text-muted">None.</p>
              ) : (
                <ul class="cm-list">
                  {methods.map(m => (
                    <li key={m.id} class="settings-item cm-item">
                      <div class="cm-item-main">
                        <div class="cm-item-title">
                          <strong>{m.label}</strong>
                          <span class="text-muted">{typeLabel(m.type)}</span>
                          <VerifiedBadge method={m} />
                        </div>
                        {m.summary && <p class="cm-item-dest text-muted">{m.summary}</p>}
                      </div>
                    </li>
                  ))}
                </ul>
              )}
              <h4 class="cm-subheading">Notification rules</h4>
              {rules.length === 0 ? (
                <p class="text-muted">None.</p>
              ) : (
                <ol class="cm-list">
                  {rules.map(r => {
                    const m = byId[r.contact_method_id];
                    return (
                      <li key={r.id} class="settings-item cm-item">
                        <div class="cm-item-main">
                          <div class="cm-item-title">
                            <strong>{delayLabel(r.delay_minutes)}</strong>
                            <span>{m ? `${m.label} (${typeLabel(m.type)})` : 'Removed method'}</span>
                          </div>
                          <p class="cm-item-dest text-muted">
                            {urgencyLabel(r.urgency)}{m && !m.verified ? '. Skipped, not verified.' : ''}
                          </p>
                        </div>
                      </li>
                    );
                  })}
                </ol>
              )}
              <p class="form-help">Only {name} can change these.</p>
            </>
          )}
        </div>
      )}
    </Modal>
  );
}
