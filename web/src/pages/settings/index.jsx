import { useState, useEffect, useCallback, useRef } from 'preact/hooks';
import { get, post, patch, del, put } from '../../api/client';
import { PageHeader, Card, Modal, LoadingPage, ErrorMessage, formatTime, useToast, ToastContainer, useAutoLabels } from '../../components/ui';
import { currentOrg, currentUser, appMeta, isPaidPlan } from '../../state/auth';
import { clearUpgradePrompt } from '../../state/upgrade';
import { route } from 'preact-router';
import { MFATab, OrgMFASection } from '../../edition/mfa-ui';
import { SessionsTab } from './sessions.jsx';
import { SupportAccessTab } from './support-access.jsx';
import { AccountTab } from './account.jsx';
import { IntegrationsTab } from './integrations';
import { MonitorStatesTab } from './monitor-states.jsx';
import { ImportTab } from './import.jsx';
import { HostedNote } from '../../components/hosted-note';
import { MaintenanceTab } from './maintenance.jsx';
import { ContactMethodsTab } from './contact-methods.jsx';
import { BillingTab } from './billing.jsx';
import { APIKeysTab } from './api-keys.jsx';
import { MembersTab } from './members.jsx';
import { TeamsTab } from './teams.jsx';
import { OrgTab } from './org.jsx';
import { canWrite, isOrgAdmin, settingsTabDeniedNote, settingsTabReadOnlyNote } from '../../state/permissions.mjs';
import { UpgradeLink } from '../../components/upgrade-link';
import { availableSettingsTabs, visibleSettingsTabs } from './tabs.mjs';
import { EmailChannelFields } from './email-channel.jsx';
import { channelAccessNote } from './channel-permissions-logic.mjs';
import { ChannelHealthBadge, ChannelHealthLine, FailingChannelsNote, useLinkedChannel, channelRowId } from './channel-health.jsx';
import { ChannelPlanBadge, ChannelPlanLine, PaidChannelsBanner, ChannelTestNote, useTestCooldowns, channelTestNoteId } from './channel-plan.jsx';
import { testRefusal, testCooldown } from './channel-plan-logic.mjs';
import { testSentMessage, testFilteredMessage, testFailureNote } from './channel-health-logic.mjs';
import { testResultLifetime } from '../../components/ui-logic.mjs';
import { validateEmailConfig, emailConfigBody, parseEmailConfigError, splitRecipients, EMAIL_FIELDS } from './email-channel-logic.mjs';
import './channel-config.css';
import { CONFIG_SENTINEL, isSavedSecret, savedURLText, writeOnlyURLSummary } from './channel-secrets-logic.mjs';
import { WebhookHeadersEditor } from './webhook-headers.jsx';
import { ChannelUsageLine, ChannelDeleteBlocked, InlineConfirm, fromUsageDisclosure, StepChannelWarning, usePolicyDeepLink } from './channel-usage.jsx';
import { usedByList, needsDisableConfirm, disableConfirmText, deleteConfirmText, deleteBlockedBy, formSnapshot, channelOptionLabel } from './channel-usage-logic.mjs';
import { SlackWorkspaces } from './slack-app.jsx';
import { SlackAppChannelFields, SlackAppDisconnectedTag } from './slack-app-channel.jsx';
import { SLACK_APP_TYPE, slackAppConfig, validateSlackAppConfig, slackAppSummary, offerSlackAppType, withInstallation as withSlackInstallation } from './slack-app-channel-state.mjs';
import { slackInstallsData } from './slack-installs.js';
import { DiscordServers, DiscordAppChannelFields, DiscordAppDisconnectedTag } from './discord-app.jsx';
import { DISCORD_APP_TYPE, discordAppConfig, validateDiscordAppConfig, discordAppSummary, offerDiscordAppType, newChannelConfig as newDiscordChannelConfig } from './discord-app-state.mjs';
import { discordInstallsData } from './discord-installs.js';
import { PhoneVerifyPanel, UnverifiedTag, UnverifiedNotice, isPhoneChannel, isUnverified } from './phone-verify.jsx';
import { TelegramChannelSection, TelegramNotLinkedTag, TelegramLinkAnnouncer, announceTelegramLinked, isTelegramUnlinked, telegramSummary, TELEGRAM_NAME_MAX } from './telegram-link.jsx';
import {
  SigningSecretReveal, SigningSecretSection, SigningCreateNote, UnsignedTag,
  applyRotation, withoutSecret, shouldWarnBeforeClose,
} from './webhook-signing.jsx';
import { headersToRows, rowsToHeaders, headerRowForServerField } from './webhook-headers-logic.mjs';
import { isPhoneType, isRetiredType, normalizePhone, formatPhone, phoneConfigBody, parseChannelServerError, outboundURLHelp, channelTypeOptions, testErrorField, parseJsonField, parseJsonFields, stringifyJsonFields, configFieldError, prettyJsonField, jsonFieldRows, addEventTypePattern, acceptedRepliesError, CLOUDEVENT_TYPE_GROUPS } from './channel-config-logic.mjs';


export function SettingsPage({ tab: initialTab }) {
  const [tab, setTab] = useState(initialTab || 'account');
  // Links each <label> in the settings forms to its field (a11y-2).
  const mainRef = useRef(null);
  useAutoLabels(mainRef);

  // In-app links such as /settings/sessions reuse this component, so follow
  // the route parameter when it changes.
  useEffect(() => {
    if (initialTab) setTab(initialTab);
  }, [initialTab]);

  function handleTabChange(t) {
    setTab(t);
    route(`/settings/${t}`, true);
  }

  // Tabs this edition and plan offer, before the role check.
  const availableTabs = availableSettingsTabs(appMeta.value, isPaidPlan.value);
  const role = currentUser.value?.role;
  const visibleTabs = visibleSettingsTabs(appMeta.value, isPaidPlan.value, role);
  // A deep link to a tab that exists but this role cannot open (for example a
  // member opening /settings/billing) shows who manages it instead of a page
  // of 403s. A tab this edition or plan does not offer falls back to Account.
  const deniedTab = visibleTabs.some(t => t.key === tab) ? null : availableTabs.find(t => t.key === tab);
  const deniedNote = deniedTab ? settingsTabDeniedNote(role, deniedTab.key, deniedTab.label) : null;
  const activeTab = deniedNote ? null : (visibleTabs.some(t => t.key === tab) ? tab : 'account');
  const readOnlyNote = activeTab ? settingsTabReadOnlyNote(role, activeTab) : null;

  return (
    <div class="settings-page">
      <PageHeader title="Settings" subtitle="Org, teams, channels, and plan" />
      <div class="settings-layout">
        <nav class="settings-nav" aria-label="Settings sections">
          {visibleTabs.map(t => (
            <button key={t.key}
                    class={`settings-nav-item ${activeTab === t.key ? 'active' : ''}`}
                    aria-current={activeTab === t.key ? 'page' : undefined}
                    onClick={() => handleTabChange(t.key)}>
              {t.label}
            </button>
          ))}
        </nav>
        <div class="settings-main" ref={mainRef}>
          {deniedNote && (
            <section class="settings-denied" aria-labelledby="settings-denied-title">
              <h3 id="settings-denied-title">{deniedTab.label}</h3>
              <p>{deniedNote}</p>
              <p class="text-muted">The Members tab lists who the owners and admins are.</p>
              <div class="btn-group">
                <button class="btn btn-sm btn-primary" onClick={() => handleTabChange('members')}>See members</button>
                <button class="btn btn-sm" onClick={() => handleTabChange('account')}>Go to Account</button>
              </div>
            </section>
          )}
          {readOnlyNote && <p class="settings-role-note">{readOnlyNote}</p>}
          {activeTab === 'account' && <AccountTab />}
          {activeTab === 'sessions' && <SessionsTab />}
          {activeTab === 'contact-methods' && <ContactMethodsTab />}
          {activeTab === 'channels' && <ChannelsTab />}
          {activeTab === 'escalation' && <EscalationTab />}
          {activeTab === 'integrations' && <IntegrationsTab />}
          {activeTab === 'teams' && <TeamsTab />}
          {activeTab === 'monitor-states' && <MonitorStatesTab />}
          {activeTab === 'maintenance' && <MaintenanceTab />}
          {activeTab === 'status-page' && <StatusPageTab />}
          {activeTab === 'api-keys' && <APIKeysTab />}
          {activeTab === 'sso' && <SSOTab />}
          {activeTab === 'members' && <MembersTab />}
          {activeTab === 'support' && <SupportAccessTab />}
          {activeTab === 'billing' && <BillingTab />}
          {activeTab === 'security' && <><MFATab /><OrgMFASection /></>}
          {activeTab === 'import' && <ImportTab />}
          {activeTab === 'data' && <DataTab />}
          {activeTab === 'org' && (<><OrgTab /><HostedNote /></>)}
        </div>
      </div>
    </div>
  );
}

/* ─── Notification Channels ─── */

// SMS and voice channels take only the destination number. The sender
// number, provider account and call script are set by the platform.
const PHONE_FIELD = { key: 'to', label: 'Phone Number', type: 'tel', placeholder: '+1 212-555-4240', required: true, help: 'US and Canada numbers only (+1). Alerts come from the YipYap number.' };

// Voice calls also say what the person will hear and how to acknowledge.
// Keep in sync with the script in internal/api/handlers/voice_call.go.
const VOICE_FIELD = { ...PHONE_FIELD, help: 'US and Canada numbers only (+1). The call comes from the YipYap number, reads the severity and monitor name, and asks you to press 1 to acknowledge. It repeats once if no key is pressed.' };

const CHANNEL_TYPES = {
  webhook: { label: 'Webhook', fields: [
    { key: 'url', label: 'URL', type: 'url', placeholder: 'https://hooks.example.com/alerts', required: true },
    { key: 'format', label: 'Payload Format', type: 'webhook_format' },
    { key: 'headers', label: 'Custom Headers', type: 'headers', help: 'Sent with every request. Saved values are never shown again: Replace sets a new value, Remove deletes the header when you save. Host, Content-Type and Content-Length are set by YipYap.' },
  ]},
  // Fields are drawn by SlackAppChannelFields (workspace and channel pickers).
  slack_app:    { label: 'Slack (YipYap app)', custom: true, fields: [] },
  // Fields are drawn by DiscordAppChannelFields (server and channel pickers).
  discord_app:  { label: 'Discord (YipYap app)', custom: true, fields: [] },
  // Bring-your-own bots post an Open in YipYap link, never Ack/Resolve
  // buttons (notif-12). The form label says so; the list tag stays short.
  slack:        { label: 'Slack (own bot, no Ack buttons)', tag: 'Slack (own bot)', fields: [
    { key: 'bot_token', label: 'Bot Token', type: 'secret', placeholder: 'xoxb-...', required: true, help: 'OAuth & Permissions \u2192 Bot User OAuth Token' },
    { key: 'channel_id', label: 'Channel ID', type: 'text', placeholder: 'C01234ABC', required: true, help: 'Right-click channel \u2192 View channel details \u2192 Copy ID' },
  ]},
  discord:      { label: 'Discord (own bot, no Ack buttons)', tag: 'Discord (own bot)', fields: [
    { key: 'bot_token', label: 'Bot Token', type: 'secret', placeholder: 'MTIz...', required: true, help: 'Developer Portal \u2192 Bot \u2192 Token' },
    { key: 'channel_id', label: 'Channel ID', type: 'text', placeholder: '1234567890', required: true, help: 'Enable Developer Mode \u2192 Right-click channel \u2192 Copy ID' },
  ]},
  // The chat is linked with a one-time code (TelegramChannelSection), never
  // typed in: the API refuses a raw chat_id.
  telegram:     { label: 'Telegram', custom: true, fields: [] },
  // Email: recipients only by default (sent through the platform mailer),
  // with an optional "own SMTP server" section. The form lives in
  // email-channel.jsx; these keys are what it edits.
  smtp:         { label: 'Email', custom: true, fields: [
    { key: 'mode' }, { key: 'to' }, { key: 'host' }, { key: 'port' }, { key: 'tls_mode' },
    { key: 'username' }, { key: 'password' }, { key: 'from' },
  ]},
  telnyx_sms:   { label: 'SMS (Telnyx)', fields: [
    PHONE_FIELD,
  ]},
  telnyx_voice: { label: 'Voice Call (Telnyx)', fields: [
    VOICE_FIELD,
  ]},
  twilio_sms:   { label: 'SMS (Twilio legacy)', legacy: true, fields: [
    PHONE_FIELD,
  ]},
  twilio_voice: { label: 'Voice Call (Twilio legacy)', legacy: true, fields: [
    VOICE_FIELD,
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
  cloudevent_http: { label: 'CloudEvents (Knative)', fields: [
    { key: 'sink_url', label: 'Sink URL', type: 'url', placeholder: 'https://broker.example.com/ns/default', required: true, help: 'The HTTPS URL that accepts CloudEvent POSTs (Knative Broker ingress, Eventarc destination, any CloudEvents-compatible sink). Each CloudEvents channel gets its own copy of every matching event, so two channels can point at different sinks.' },
    { key: 'mode', label: 'HTTP Mode', type: 'select', options: [{ value: 'binary', label: 'Binary (one event per request; ce-* headers)' }, { value: 'structured', label: 'Structured (application/cloudevents+json)' }], default: 'binary' },
    { key: 'event_types', label: 'Event Types Filter', type: 'json', placeholder: '["run.yipyap.alert.*", "run.yipyap.monitor.down.*"]', help: 'A JSON list of patterns over CloudEvent types, where * matches any part, for example ["run.yipyap.alert.*"]. Leave empty to receive every event type.' },
    { key: 'ce_overrides', label: 'CloudEvent Overrides', type: 'json', placeholder: '{"environment": "production"}', help: 'A JSON object of extension attributes added to every event, for example to tag the environment or region. Names use lower-case letters and digits only.' },
    { key: 'accepted_replies', label: 'Accepted Replies', type: 'accepted_replies', help: 'Choose which reply CloudEvent types this channel will act on when a sink returns a CloudEvent in its response body. Replies without a ce_alertref matching a recent outbound event are always rejected.' },
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

function getConfigSummary(type, config, ch) {
  if (!config || typeof config !== 'object') return '';
  // A URL that holds a credential comes back hidden; show its host.
  const hiddenURL = writeOnlyURLSummary(ch);
  if (hiddenURL) return hiddenURL;
  switch (type) {
    case 'webhook': return config.url ? truncate(config.url, 40) : '';
    case 'slack_app': return slackAppSummary(config, slackInstallsData.value?.installations);
    case 'discord_app': return discordAppSummary(config, discordInstallsData.value?.installations);
    case 'slack': return config.channel_id ? 'Channel: ' + config.channel_id : '';
    case 'discord': return config.channel_id ? 'Channel: ' + config.channel_id : '';
    case 'telegram': return telegramSummary(config);
    case 'smtp': {
      const to = splitRecipients(config.to);
      const who = to.length > 2 ? to.slice(0, 2).join(', ') + ' +' + (to.length - 2) : to.join(', ');
      const own = config.mode === 'smtp' || (!config.mode && config.host);
      return own && config.host ? who + ' via ' + config.host : who;
    }
    case 'telnyx_sms':
    case 'telnyx_voice':
    case 'twilio_sms':
    case 'twilio_voice': return formatPhone(config.to);
    default: return '';
  }
}

function SecretInput({ value, onInput, placeholder, id, required }) {
  const [show, setShow] = useState(false);
  return (
    <div class="secret-field">
      <input id={id} type={show ? 'text' : 'password'} value={value} onInput={onInput}
        placeholder={placeholder} autocomplete="off" aria-required={required ? 'true' : undefined} />
      <button type="button" class="secret-toggle" onClick={() => setShow(!show)}
        aria-pressed={show ? 'true' : 'false'} aria-controls={id}
        aria-label={show ? 'Hide value' : 'Show value'}>
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
  const [headerErrors, setHeaderErrors] = useState({}); // { [row key]: { name?, value? } }
  const [testing, setTesting] = useState(null); // channel id being tested
  const [testResult, setTestResult] = useState({}); // { [id]: 'ok' | 'fail' }
  // Refused tests (rate limit, plan, quota): a note under the row and a
  // cooldown that holds back the Test buttons the limit covers.
  const [testNotes, setTestNotes] = useState({}); // { [id]: refusal }
  const { cooldowns, startCooldown, announce, announcement } = useTestCooldowns(setTestNotes);
  // Server facts for the form. platform_email false means email channels
  // must use their own SMTP server (FOSS without a configured mailer).
  const [caps, setCaps] = useState({ platform_email: true, email_max_recipients: 10 });
  // What the caller may do, from GET /org/notification-permissions. The
  // server enforces it; this only hides controls that would be refused.
  // null while loading: write controls stay hidden so a viewer never sees
  // them flash. If the request fails they show, and the server answers any
  // refused action with a 403 message.
  const [perms, setPerms] = useState(null);
  // Policies that block deleting the channel being edited, shown in the
  // edit modal with links; null when no delete was refused.
  const [deleteBlock, setDeleteBlock] = useState(null);
  // An in-form question waiting for an answer: 'delete' (unused channel)
  // or 'disable' (saving an in-use channel as disabled). Answering it
  // either way keeps the form and its edits.
  const [pendingConfirm, setPendingConfirm] = useState(null);
  const [deleting, setDeleting] = useState(false);
  // The form as it was when the edit opened, to tell whether a policy
  // link in the refused-delete notice would discard changes.
  const [openedSnapshot, setOpenedSnapshot] = useState('');
  // The phone channel in its verify step, if any.
  const [verifyChannel, setVerifyChannel] = useState(null);
  // Webhook signing secret on screen right now: { secret, copied, context:
  // 'create' | 'rotate', channelId }. Lives here, not in the section, so a
  // list reload cannot drop it and closing can warn first.
  const [shownSecret, setShownSecret] = useState(null);
  const [secretLeaveWarn, setSecretLeaveWarn] = useState(false);
  const deleteBtnRef = useRef(null);
  const saveBtnRef = useRef(null);
  const addChannelBtn = useRef(null);
  const telegramStart = useRef(null); // set by TelegramChannelSection
  const { toasts, toast } = useToast();

  // quiet skips the full-page spinner, for refreshing health after a test.
  async function load(quiet) {
    if (!quiet) setLoading(true);
    try {
      const data = await get('/notification-channels');
      const list = data.channels || data || [];
      setChannels(list);
      return list;
    } catch (e) {
      if (!quiet) toast('Failed to load channels', 'error');
    }
    if (!quiet) setLoading(false);
    return [];
  }
  useEffect(() => { load(); }, []);
  useEffect(() => {
    get('/notification-channels/capabilities').then(c => c && setCaps(s => ({ ...s, ...c }))).catch(() => {});
    get('/org/notification-permissions')
      .then(p => setPerms(p || { can_manage: true, can_test: true }))
      .catch(() => setPerms({ can_manage: true, can_test: true }));
  }, []);

  // Shows errors and moves focus to the first one, so keyboard and screen
  // reader users land on the field that needs attention.
  function showErrors(errs, forType = type) {
    setErrors(errs);
    const fields = forType === 'smtp' ? EMAIL_FIELDS
      : forType === SLACK_APP_TYPE ? ['installation_id', 'channel_id']
      : forType === DISCORD_APP_TYPE ? ['installation_id', 'channel_id']
      : (CHANNEL_TYPES[forType]?.fields || []).map(f => f.key);
    const first = ['name', ...fields].find(k => errs[k]);
    if (first) {
      setTimeout(() => {
        const el = document.getElementById(first === 'name' ? 'channel-name' : 'channel-field-' + first);
        el?.focus();
      }, 0);
    }
  }

  // Maps an email config 400 from the API onto its field. Returns true when
  // handled.
  function showServerFieldError(e) {
    const parsed = parseEmailConfigError(e?.message);
    if (!parsed || !parsed.field) return false;
    if (parsed.field === 'mode' && /not available/.test(e.message)) {
      setCaps(c => ({ ...c, platform_email: false }));
    }
    showErrors({ [parsed.field]: parsed.message }, 'smtp');
    return true;
  }

  function resetForm() {
    setName(''); setType('webhook'); setConfig({}); setEnabled(true);
    setEditId(null); setErrors({}); setHeaderErrors({}); setDeleteBlock(null);
    setPendingConfirm(null); setOpenedSnapshot('');
    setVerifyChannel(null); setShownSecret(null); setSecretLeaveWarn(false);
  }

  // Closing with an uncopied secret on screen warns once; the next close
  // goes through.
  function closeModal() {
    if (shouldWarnBeforeClose(shownSecret, secretLeaveWarn)) {
      setSecretLeaveWarn(true);
      return;
    }
    setShowModal(false);
    resetForm();
  }

  function markSecretCopied() {
    setShownSecret(s => (s ? { ...s, copied: true } : s));
    setSecretLeaveWarn(false);
  }

  function hideRotatedSecret() {
    if (shouldWarnBeforeClose(shownSecret, secretLeaveWarn)) {
      setSecretLeaveWarn(true);
      return;
    }
    setShownSecret(null);
    setSecretLeaveWarn(false);
  }

  function onSecretRotated(res) {
    // Update the list in place. A full load() would swap the page for the
    // loading view, unmount the modal and lose the new secret.
    setChannels(list => applyRotation(list, res));
    setShownSecret({ secret: res.signing_secret || '', copied: false, context: 'rotate', channelId: res.id });
    setSecretLeaveWarn(false);
    toast('New signing secret created. Copy it now.');
  }

  function openVerify(ch) {
    resetForm();
    setVerifyChannel(ch);
    setShowModal(true);
  }

  function onTelegramLinked(res) {
    announceTelegramLinked(res);
    toast(res?.chat_title ? 'Linked to ' + res.chat_title : 'Telegram chat linked');
    setShowModal(false);
    resetForm();
    // A full load() would swap the page for the spinner and unmount the
    // live region before it speaks. The modal is gone, so put focus on a
    // control that stays.
    load(true);
    setTimeout(() => addChannelBtn.current && addChannelBtn.current.focus(), 0);
  }

  function onVerified() {
    toast('Number verified. This channel now gets alerts.');
    closeModal();
    load();
  }

  function openCreate() {
    resetForm();
    setShowModal(true);
  }

  // From the Slack workspaces section: a new Slack (YipYap app) channel,
  // with the workspace preselected.
  function openCreateSlack(installationId) {
    resetForm();
    setType(SLACK_APP_TYPE);
    setConfig(withSlackInstallation({}, installationId || ''));
    setShowModal(true);
  }

  // From the Discord servers section: a new Discord (YipYap app) channel,
  // with the server preselected when known.
  function openCreateDiscord(installationId) {
    resetForm();
    setType(DISCORD_APP_TYPE);
    setConfig(newDiscordChannelConfig(installationId));
    setShowModal(true);
  }

  function openEdit(ch) {
    setEditId(ch.id);
    setName(ch.name);
    setType(ch.type);
    // JSON fields (event filter, overrides) edit as text; webhook headers
    // edit as rows whose saved values stay on the server.
    const cfg = stringifyJsonFields(ch.type, ch.config && typeof ch.config === 'object' ? ch.config : {});
    if (ch.type === 'webhook') cfg.headers = headersToRows(ch.config?.headers);
    // Email rows saved before the mode field: a host means their own server.
    if (ch.type === 'smtp' && !cfg.mode) cfg.mode = cfg.host ? 'smtp' : 'platform';
    setConfig({ ...cfg, _formatManual: !!ch.config?.format, _urlHost: ch.url_set ? ch.url_host : '', _urlField: ch.url_set ? ch.url_field : '' });
    setEnabled(ch.enabled !== false);
    setErrors({}); setHeaderErrors({}); setDeleteBlock(null); setPendingConfirm(null);
    setOpenedSnapshot(formSnapshot({ name: ch.name, type: ch.type, config: cfg, enabled: ch.enabled !== false }));
    setShowModal(true);
  }

  function validate() {
    const errs = {};
    if (!name.trim()) errs.name = 'Required';
    if (type === SLACK_APP_TYPE) Object.assign(errs, validateSlackAppConfig(config));
    if (type === DISCORD_APP_TYPE) Object.assign(errs, validateDiscordAppConfig(config));
    const typeDef = CHANNEL_TYPES[type];
    if (typeDef) {
      if (type === 'smtp') {
        Object.assign(errs, validateEmailConfig(config, { maxRecipients: caps.email_max_recipients }));
      }
      for (const f of typeDef.custom ? [] : typeDef.fields) {
        if (f.required && !config[f.key]?.toString().trim()) {
          errs[f.key] = 'Required';
        }
        if (f.type === 'url' && config[f.key] && !isSavedSecret(config[f.key]) && !/^https?:\/\/.+/.test(config[f.key])) {
          errs[f.key] = 'Must be a valid URL';
        }
        if (f.type === 'json') {
          const r = parseJsonField(type, f.key, config[f.key]);
          if (r.error) errs[f.key] = r.error;
        }
        if (f.type === 'accepted_replies') {
          const msg = acceptedRepliesError(config[f.key]);
          if (msg) errs[f.key] = msg;
        }
        if (f.type === 'headers') {
          const r = rowsToHeaders(config[f.key]);
          setHeaderErrors(r.errors);
          if (r.formError) errs[f.key] = r.formError;
          else if (Object.keys(r.errors).length) errs[f.key] = 'Fix the headers marked below';
        }
      }
      if (isPhoneType(type)) {
        const n = normalizePhone(config.to);
        if (n.error) errs.to = n.error;
      }
    }
    showErrors(errs);
    return Object.keys(errs).length === 0;
  }

  // confirmedDisable is set when the user has answered the in-form
  // question about turning off a channel that policies send through.
  async function save({ confirmedDisable = false } = {}) {
    // New Telegram channels come from the link code, not this form, so
    // Enter in the name field asks for a code instead.
    if (type === 'telegram' && !editId) {
      if (telegramStart.current) telegramStart.current();
      return;
    }
    if (!validate()) return;
    // Saving closes the modal, so treat it like a close while a rotated
    // secret is still uncopied.
    if (shouldWarnBeforeClose(shownSecret, secretLeaveWarn)) {
      setSecretLeaveWarn(true);
      return;
    }
    if (editId && !confirmedDisable) {
      const current = channels.find(c => c.id === editId);
      if (needsDisableConfirm(current?.enabled !== false, enabled, usedByList(current))) {
        setDeleteBlock(null);
        setPendingConfirm('disable');
        return;
      }
    }
    setPendingConfirm(null);
    try {
      // JSON text fields go to the API as parsed values. validate() has
      // already refused any that do not parse, so nothing is dropped here.
      const parsed = parseJsonFields(type, config);
      if (Object.keys(parsed.errors).length) { showErrors(parsed.errors); return; }
      const cfgToSend = parsed.config;
      delete cfgToSend._formatManual;
      delete cfgToSend._urlHost;
      delete cfgToSend._urlField;
      if (type === 'webhook') {
        const h = rowsToHeaders(config.headers).headers;
        if (h) cfgToSend.headers = h; else delete cfgToSend.headers;
      }
      if (type === 'telegram') delete cfgToSend.bot_token;
      const body = { name, type, config: cfgToSend, enabled };
      if (type === SLACK_APP_TYPE) body.config = slackAppConfig(cfgToSend);
      if (type === DISCORD_APP_TYPE) body.config = discordAppConfig(cfgToSend);
      // Send only the keys the chosen mode allows; the API rejects server
      // settings on a recipients-only channel.
      if (type === 'smtp') body.config = emailConfigBody(cfgToSend);
      // SMS and voice take exactly {"to"}; any other key is a 400.
      if (isPhoneType(type)) body.config = phoneConfigBody(cfgToSend);
      const saved = editId
        ? await patch('/notification-channels/' + editId, body)
        : await post('/notification-channels', body);
      // New SMS and voice channels, and saved number changes, come back
      // pending. Go straight to the verify step instead of closing.
      if (saved && isPhoneChannel(saved.type) && isUnverified(saved) && canManage) {
        toast(editId ? 'Channel updated. Verify the number to turn it on.' : 'Channel created. Verify the number to turn it on.');
        resetForm();
        setVerifyChannel(saved);
        load(true);
        return;
      }
      // A new webhook channel returns its signing secret once. Keep the
      // modal open on it so the user can copy it.
      if (!editId && saved?.signing_secret) {
        resetForm();
        setShownSecret({ secret: saved.signing_secret, copied: false, context: 'create', channelId: saved.id });
        setChannels(list => [...list, withoutSecret(saved)]);
        return;
      }
      toast(editId ? 'Channel updated' : 'Channel created');
      setShowModal(false);
      resetForm();
      load();
    } catch (e) {
      if (type === 'smtp' && showServerFieldError(e)) return;
      const formKeys = (CHANNEL_TYPES[type]?.fields || []).map(f => f.key);
      const fieldErr = e?.status === 400 && (configFieldError(type, e.body, formKeys) || parseChannelServerError(type, e.message));
      // A header error ("headers.X-Key") also marks that row.
      const rowKey = fieldErr && type === 'webhook' && headerRowForServerField(config.headers, e.body?.field);
      if (rowKey) setHeaderErrors({ [rowKey]: { value: fieldErr.message } });
      if (fieldErr) {
        showErrors({ [fieldErr.field]: fieldErr.message });
        return;
      }
      toast(e?.message || 'Failed to save channel', 'error');
    }
  }

  async function testChannel(id) {
    setTesting(id);
    setTestResult(r => ({ ...r, [id]: null }));
    setTestNotes(n => { const { [id]: _, ...rest } = n; return rest; });
    try {
      const resp = await post('/notification-channels/' + id + '/test', {});
      const filtered = testFilteredMessage(resp);
      if (filtered) {
        // Not a delivery failure, but nothing arrived either: leave the
        // button neutral and say why.
        toast(filtered, 'error');
      } else {
        setTestResult(r => ({ ...r, [id]: 'ok' }));
        toast(testSentMessage(resp));
        // A successful test clears a failing status on the server.
        load(true);
      }
    } catch (e) {
      const ch0 = channels.find(c => c.id === id);
      const refusal = testRefusal(e, ch0?.type);
      if (refusal) {
        // Not a delivery failure: the test was never sent. Leave the
        // button neutral and explain under the row.
        setTestNotes(n => ({ ...n, [id]: refusal }));
        announce(refusal.message);
        if (refusal.cooldown) startCooldown(refusal.cooldown);
        if (refusal.refreshOrg) {
          // The plan or grace may have changed since login; refresh so the
          // row badges match what the server enforces. The row note links
          // to billing, so the page-wide banner would repeat it.
          clearUpgradePrompt();
          get('/org').then(o => { if (o) currentOrg.value = o; }).catch(() => {});
        }
        setTesting(null);
        return;
      }
      setTestResult(r => ({ ...r, [id]: 'fail' }));
      // A stored email config that no longer passes the checks (for example
      // a row saved before encryption was required) cannot be tested until it
      // is fixed, so open the editor on the field that needs it.
      const parsed = parseEmailConfigError(e?.message);
      const ch = channels.find(c => c.id === id);
      // A URL saved before private addresses were refused fails the same way.
      const urlErr = ch && perms?.can_manage && testErrorField(ch.type, e?.message);
      // A setting saved before typed checks (for example a filter stored as
      // text) is named by the server; open the form on it.
      const cfgErr = ch && perms?.can_manage && e?.status === 400
        && configFieldError(ch.type, e.body, (CHANNEL_TYPES[ch.type]?.fields || []).map(f => f.key));
      if (ch && ch.type === 'smtp' && parsed?.field) {
        toast('This channel needs an update before it can send. Fix the highlighted field and save.', 'error');
        openEdit(ch);
        setTimeout(() => showServerFieldError(e), 0);
      } else if (urlErr) {
        toast('This channel points to an address alerts cannot be sent to. Change it and save.', 'error');
        openEdit(ch);
        setTimeout(() => showErrors({ [urlErr.field]: urlErr.message }, ch.type), 0);
      } else if (cfgErr) {
        toast('This channel has a setting alerts cannot use. Fix the highlighted field and save.', 'error');
        openEdit(ch);
        setTimeout(() => showErrors({ [cfgErr.field]: cfgErr.message }, ch.type), 0);
      } else {
        // Keep the reason under the row until dismissed or the next Test.
        const note = testFailureNote(e?.message);
        setTestNotes(n => ({ ...n, [id]: note }));
        announce((ch?.name ? ch.name + ': ' : '') + note.message);
      }
    }
    setTesting(null);
  }

  // A sent Test clears its button after a few seconds; a failed one stays.
  useEffect(() => {
    const timers = Object.entries(testResult)
      .filter(([, st]) => st && testResultLifetime(st) !== null)
      .map(([id, st]) => setTimeout(() => setTestResult(r => (r[id] === st ? { ...r, [id]: null } : r)), testResultLifetime(st)));
    return () => timers.forEach(clearTimeout);
  }, [testResult]);

  // Dismissing a failure note also resets the Test button.
  function dismissTestNote(id) {
    setTestNotes(n => { const { [id]: _, ...rest } = n; return rest; });
    setTestResult(r => (r[id] === 'fail' ? { ...r, [id]: null } : r));
    setTimeout(() => document.getElementById('channel-test-' + id)?.focus(), 0);
  }

  // Delete Channel button: an in-use channel shows the policies that
  // block it, any other asks in the form before deleting.
  function requestDelete(id) {
    const blocked = deleteBlockedBy(channels.find(c => c.id === id));
    if (blocked) { setPendingConfirm(null); setDeleteBlock(blocked); return; }
    setDeleteBlock(null);
    setPendingConfirm('delete');
  }

  // Returns true only when the channel was deleted, so the caller closes
  // the modal then and keeps unsaved edits otherwise (refusal or error).
  async function deleteChannel(id) {
    setDeleting(true);
    try {
      await del('/notification-channels/' + id);
      toast('Channel deleted');
      load();
      return true;
    } catch (e) {
      // A policy started using it since the list loaded.
      const refused = deleteBlockedBy(null, e);
      if (refused) { setPendingConfirm(null); setDeleteBlock(refused); load(true); return false; }
      toast(e?.message || 'Failed to delete', 'error');
      return false;
    } finally {
      setDeleting(false);
    }
  }

  // Cancel or dismiss puts focus back on the button that asked.
  function closeConfirm(focusRef) {
    setPendingConfirm(null);
    setDeleteBlock(null);
    setTimeout(() => focusRef.current?.focus(), 0);
  }

  function updateConfig(key, value) {
    setConfig(c => {
      const next = { ...c, [key]: value };
      if (key === 'url' && type === 'webhook' && !c._formatManual) {
        next.format = detectWebhookFormat(value);
      }
      return next;
    });
    if (errors[key]) setErrors(({ [key]: _, ...rest }) => rest);
  }

  function handleTypeChange(newType) {
    setType(newType);
    // Keep config values that overlap between types, clear the rest
    const newFields = (CHANNEL_TYPES[newType]?.fields || []).map(f => f.key);
    setConfig(c => {
      const next = {};
      for (const k of newFields) {
        // 'mode' means different things per type (email vs CloudEvents).
        // 'to' is a phone number on SMS/voice and addresses on email.
        const crossesEmail = k === 'to' && (newType === 'smtp') !== (type === 'smtp');
        if (k !== 'mode' && !crossesEmail && c[k] !== undefined) next[k] = c[k];
      }
      return next;
    });
    setErrors({});
  }

  function renderField(f) {
    const fid = 'channel-field-' + f.key;
    const req = f.required ? 'true' : undefined;
    // A URL that holds a credential is write-only: only its host comes back.
    const urlSaved = f.type === 'url' && isSavedSecret(config[f.key]);
    const urlReplacing = f.type === 'url' && !urlSaved && !!config._urlHost && config._urlField === f.key;
    const help = urlSaved
      ? 'This URL contains a credential, so only its host is shown. Replace it to send alerts to a new address.'
      : outboundURLHelp(type, f.key, appMeta.value?.edition, f.help);
    const aria = errors[f.key]
      ? { 'aria-invalid': 'true', 'aria-describedby': fid + '-error' }
      : help ? { 'aria-describedby': fid + '-help' } : {};
    return (
      <div key={f.key} class={'form-group' + (errors[f.key] ? ' has-error' : '')}>
        <label for={fid} id={fid + '-label'} class={f.required ? 'required' : undefined}>{f.label}</label>
        {f.type === 'secret' ? (
          <SecretInput id={fid} required={f.required} value={config[f.key] || ''} placeholder={f.placeholder}
            onInput={e => updateConfig(f.key, e.target.value)} />
        ) : f.type === 'json' ? (
          <>
            <textarea id={fid} class="json-input" rows={jsonFieldRows(config[f.key])} spellcheck={false} autocomplete="off"
              autocapitalize="off" aria-required={req} {...aria} value={typeof config[f.key] === 'string' ? config[f.key] : ''} placeholder={f.placeholder}
              onInput={e => updateConfig(f.key, e.target.value)}
              onBlur={e => { const v = prettyJsonField(type, f.key, e.target.value); if (v !== e.target.value) updateConfig(f.key, v); }} />
            {type === 'cloudevent_http' && f.key === 'event_types' && (
              <EventTypePicker onAdd={pattern => {
                const next = addEventTypePattern(config[f.key], pattern);
                if (next === null) {
                  showErrors({ [f.key]: 'Fix the list above before adding a pattern' });
                  return;
                }
                updateConfig(f.key, next);
              }} />
            )}
          </>
        ) : f.type === 'number' ? (
          <input id={fid} type="number" aria-required={req} {...aria} value={config[f.key] ?? ''} placeholder={f.placeholder}
            onInput={e => updateConfig(f.key, e.target.value === '' ? '' : +e.target.value)} />
        ) : f.type === 'webhook_format' ? (
          <select id={fid} value={config.format || 'generic'} onChange={e => {
            setConfig(c => ({ ...c, format: e.target.value, _formatManual: true }));
          }}>
            {Object.entries(WEBHOOK_FORMATS).map(([k, v]) => (
              <option key={k} value={k}>{v.label}</option>
            ))}
          </select>
        ) : f.type === 'select' ? (
          <select id={fid} aria-required={req} {...aria} value={config[f.key] || f.default || ''} onChange={e => updateConfig(f.key, e.target.value)}>
            {(f.options || []).map(opt => (
              <option key={opt.value} value={opt.value}>{opt.label}</option>
            ))}
          </select>
        ) : f.type === 'accepted_replies' ? (
          <AcceptedRepliesEditor id={fid} describedBy={aria['aria-describedby']} invalid={!!errors[f.key]}
            value={config[f.key]} onChange={v => updateConfig(f.key, v)} />
        ) : f.type === 'tel' ? (
          <input id={fid} type="tel" inputMode="tel" autocomplete="tel" aria-required={req} {...aria} value={config[f.key] || ''} placeholder={f.placeholder}
            onInput={e => updateConfig(f.key, e.target.value)}
            onBlur={e => { const n = normalizePhone(e.target.value); if (n.value) updateConfig(f.key, formatPhone(n.value)); }} />
        ) : f.type === 'headers' ? (
          <WebhookHeadersEditor id={fid} rows={config[f.key]} rowErrors={headerErrors} invalid={!!errors[f.key]}
            describedBy={aria['aria-describedby']}
            onChange={(rows, changed) => {
              updateConfig(f.key, rows);
              if (headerErrors[changed]) setHeaderErrors(({ [changed]: _, ...rest }) => rest);
            }} />
        ) : urlSaved ? (
          // The stored URL holds a credential and is never sent back.
          // Replace clears it so a new one can be typed.
          <div class="saved-url">
            <input id={fid} type="text" readOnly aria-required={req} {...aria} value={savedURLText(config._urlHost)} />
            <button type="button" class="btn btn-sm" id={fid + '-replace'} aria-label={'Replace ' + f.label}
              onClick={() => { updateConfig(f.key, ''); setTimeout(() => document.getElementById(fid)?.focus(), 0); }}>Replace</button>
          </div>
        ) : urlReplacing ? (
          // Replacing a saved URL: offer the way back without a reload.
          <div class="saved-url">
            <input id={fid} type="url" aria-required={req} {...aria} value={config[f.key] || ''} placeholder={f.placeholder}
              autocomplete="off" spellcheck={false} onInput={e => updateConfig(f.key, e.target.value)} />
            <button type="button" class="btn btn-sm" aria-label={'Keep saved ' + f.label}
              onClick={() => {
                // Set directly so webhook format auto-detect does not run.
                setConfig(c => ({ ...c, [f.key]: CONFIG_SENTINEL }));
                if (errors[f.key]) setErrors(({ [f.key]: _, ...rest }) => rest);
                setTimeout(() => document.getElementById(fid + '-replace')?.focus(), 0);
              }}>Keep saved</button>
          </div>
        ) : (
          <input id={fid} type={f.type === 'url' ? 'url' : 'text'} aria-required={req} {...aria} value={config[f.key] || ''} placeholder={f.placeholder}
            onInput={e => updateConfig(f.key, e.target.value)} />
        )}
        {help && !errors[f.key] && <div class="form-help" id={fid + '-help'}>{help}</div>}
        {errors[f.key] && <div class="field-error" id={fid + '-error'}>{errors[f.key]}</div>}
      </div>
    );
  }

  const typeDef = CHANNEL_TYPES[type];
  // A new Telegram channel is created by the bot when the chat sends its
  // code, so the form has no Create button.
  const newTelegram = type === 'telegram' && !editId;

  // Called before the loading return so the hook order never changes.
  const linkedId = useLinkedChannel(channels, loading, toast);
  if (loading) return <LoadingPage />;

  const canManage = !!perms?.can_manage;
  const canTest = !!perms?.can_test;
  const accessNote = channelAccessNote(perms);
  const typeOptions = channelTypeOptions(CHANNEL_TYPES, { edition: appMeta.value?.edition, paid: isPaidPlan.value })
    .filter(k => k !== SLACK_APP_TYPE || type === SLACK_APP_TYPE || (appMeta.value?.edition !== 'foss' && offerSlackAppType(slackInstallsData.value, !!editId)))
    .filter(k => k !== DISCORD_APP_TYPE || type === DISCORD_APP_TYPE || (appMeta.value?.edition !== 'foss' && offerDiscordAppType(discordInstallsData.value, !!editId)));

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <TelegramLinkAnnouncer />
      <SlackWorkspaces canManage={canManage} channels={channels} onAddChannel={openCreateSlack} />
      <DiscordServers canManage={canManage} channels={channels} onAddChannel={openCreateDiscord} />
      <div class="section-header">
        <h3>Notification Channels</h3>
        {canManage && <button ref={addChannelBtn} class="btn btn-sm btn-primary" onClick={openCreate}>Add Channel</button>}
      </div>
      {accessNote && (
        <p class="channels-access-note" id="channels-access-note">
          {accessNote} Owners and admins set this under Settings, Organization.
        </p>
      )}
      <FailingChannelsNote channels={channels} canManage={canManage} />
      <PaidChannelsBanner channels={channels} />
      <div class="channel-sr-live" aria-live="polite" aria-atomic="true">{announcement}</div>
      <div class="settings-list">
        {channels.map(ch => {
          const summary = getConfigSummary(ch.type, ch.config, ch);
          const tState = testResult[ch.id];
          const isTesting = testing === ch.id;
          const cooling = testCooldown(cooldowns, ch.type);
          const testNote = testNotes[ch.id];
          const testBtnClass = tState === 'ok' ? 'btn btn-xs btn-test-ok'
            : tState === 'fail' ? 'btn btn-xs btn-test-fail'
            : 'btn btn-xs';
          return (
            // The row click is a mouse shortcut; the Edit button is the
            // keyboard and screen reader path to the same action.
            // The row named by the failure email link is focusable so focus
            // can land on it, and is highlighted until the page reloads.
            <div key={ch.id} id={channelRowId(ch.id)}
              class={'settings-item' + (linkedId === ch.id ? ' channel-row-linked' : '')}
              tabIndex={linkedId === ch.id ? -1 : undefined}
              aria-current={linkedId === ch.id ? 'location' : undefined}
              style={canManage ? 'cursor: pointer' : undefined}
              onClick={canManage ? e => { if (!fromUsageDisclosure(e)) openEdit(ch); } : undefined}>
              <div class="channel-info">
                <div class="channel-header">
                  <span class={'channel-dot ' + (ch.enabled ? 'channel-dot-on' : 'channel-dot-off')} aria-hidden="true" />
                  <strong>{ch.name}</strong>
                  {!ch.enabled && <span class="label-tag channel-disabled-tag">Disabled</span>}
                  <span class="label-tag">{(CHANNEL_TYPES[ch.type]?.tag || CHANNEL_TYPES[ch.type]?.label) || ch.type}</span>
                  <ChannelHealthBadge channel={ch} />
                  <ChannelPlanBadge channel={ch} />
                  <UnverifiedTag channel={ch} />
                  <TelegramNotLinkedTag channel={ch} />
                  <UnsignedTag channel={ch} />
                  <SlackAppDisconnectedTag channel={ch} />
                  <DiscordAppDisconnectedTag channel={ch} />
                </div>
                {summary && <span class="channel-summary">{summary}</span>}
                <ChannelHealthLine channel={ch} />
                <ChannelPlanLine channel={ch} />
                <ChannelUsageLine channel={ch} />
                <ChannelTestNote channel={ch} note={testNote} onDismiss={() => dismissTestNote(ch.id)} />
              </div>
              {(canTest || canManage) && (
                <div class="btn-group">
                  {isTelegramUnlinked(ch) ? (
                    canManage && (
                      <button type="button" class="btn btn-xs btn-primary"
                        aria-label={'Link a Telegram chat to ' + ch.name}
                        onClick={e => { e.stopPropagation(); openEdit(ch); }}>
                        Link chat
                      </button>
                    )
                  ) : isUnverified(ch) ? (
                    canManage && (
                      <button type="button" class="btn btn-xs btn-primary"
                        aria-label={'Verify number for ' + ch.name}
                        onClick={e => { e.stopPropagation(); openVerify(ch); }}>
                        Verify
                      </button>
                    )
                  ) : canTest && (
                    <button type="button" id={'channel-test-' + ch.id} class={testBtnClass} aria-label={isTesting ? 'Testing ' + ch.name : tState === 'ok' ? 'Test sent to ' + ch.name : tState === 'fail' ? 'Test failed for ' + ch.name : 'Test ' + ch.name}
                      aria-describedby={testNote ? channelTestNoteId(ch.id) : undefined}
                      onClick={e => { e.stopPropagation(); testChannel(ch.id); }} disabled={isTesting || !!cooling}>
                      {isTesting ? 'Testing\u2026' : tState === 'ok' ? '\u2713 Sent' : tState === 'fail' ? '\u2717 Failed' : 'Test'}
                    </button>
                  )}
                  {canManage && (
                    <button type="button" class="btn btn-xs" aria-label={'Edit ' + ch.name}
                      onClick={e => { e.stopPropagation(); openEdit(ch); }}>
                      Edit
                    </button>
                  )}
                </div>
              )}
            </div>
          );
        })}
        {channels.length === 0 && (
          <div style="text-align: center; padding: 2rem; color: var(--color-text-muted);">
            <p style="margin-bottom: 1rem;">No notification channels configured yet.</p>
            <p style="margin-bottom: 1rem; font-size: 0.875rem;">
              {canManage || !perms
                ? 'Set up a channel to start receiving alerts when your monitors detect issues.'
                : 'Ask an owner or admin to add one so alerts can reach your team.'}
            </p>
            {canManage && (
              <button class="btn btn-primary" onClick={openCreate}>
                Create Your First Channel
              </button>
            )}
          </div>
        )}
      </div>

      <Modal open={showModal} onClose={closeModal}
        title={verifyChannel ? 'Verify phone number' : shownSecret?.context === 'create' ? 'Channel created' : editId ? 'Edit Channel' : 'Add Channel'}>
        {shownSecret?.context === 'create' ? (
          <SigningSecretReveal shown={shownSecret} onCopied={markSecretCopied}
            warn={secretLeaveWarn} onDone={closeModal} />
        ) : verifyChannel ? (
          <PhoneVerifyPanel key={verifyChannel.id} channel={verifyChannel}
            onVerified={onVerified} onLater={closeModal} />
        ) : (
        <form class="channel-form" noValidate onSubmit={e => { e.preventDefault(); save(); }}>
          {editId && (() => {
            const current = channels.find(c => c.id === editId);
            return isUnverified(current)
              ? <UnverifiedNotice onVerify={canManage ? () => openVerify(current) : null} />
              : null;
          })()}
          <p class="form-required-note"><span aria-hidden="true">*</span> marks a required field.</p>
          <div class={'form-group' + (errors.name ? ' has-error' : '')}>
            <label class="required" for="channel-name">Name</label>
            <input id="channel-name" type="text" value={name} aria-required="true"
              maxLength={type === 'telegram' ? TELEGRAM_NAME_MAX : undefined}
              aria-invalid={errors.name ? 'true' : undefined} aria-describedby={errors.name ? 'channel-name-error' : undefined}
              onInput={e => setName(e.target.value)} placeholder="e.g. Ops Slack" />
            {errors.name && <div class="field-error" id="channel-name-error">{errors.name}</div>}
          </div>
          <div class="form-group">
            <label class="required" for="channel-type">Type</label>
            {/* The API keeps a channel's type fixed after creation. On edit
                the type shows as a read-only field, which stays in the tab
                order and is announced, unlike a disabled select. */}
            {editId ? (
              <>
                <input id="channel-type" type="text" readOnly value={CHANNEL_TYPES[type]?.label || type}
                  aria-describedby="channel-type-help" />
                <div class="form-help" id="channel-type-help">
                  The type cannot be changed. To switch, create a new channel and delete this one.
                </div>
              </>
            ) : (
              <select id="channel-type" value={type} onChange={e => handleTypeChange(e.target.value)}>
                {typeOptions.map(k => <option key={k} value={k}>{CHANNEL_TYPES[k].label}</option>)}
              </select>
            )}
          </div>

          {editId && isRetiredType(type) && (
            <p class="channel-legacy-note" role="note">
              Twilio channels are retired. This one still delivers and you can change its number,
              but new SMS and voice channels use Telnyx.
            </p>
          )}

          {type === 'telegram' && (
            <TelegramChannelSection
              key={editId || 'new'}
              isNew={!editId}
              channel={editId ? channels.find(c => c.id === editId) : null}
              name={name}
              canManage={canManage}
              botUsername={appMeta.value?.telegram_bot_username}
              onNeedName={() => {
                setErrors(e => ({ ...e, name: 'Enter a name first' }));
                const el = document.getElementById('channel-name');
                if (el) el.focus();
              }}
              startRef={telegramStart}
              onLinked={onTelegramLinked} />
          )}

          {(type === 'slack' || type === 'discord') && (
            <p class="form-help" style="margin-top:-8px; margin-bottom:12px">
              Alerts sent through your own bot carry an Open in YipYap link instead of Ack
              and Resolve buttons.
              {!editId && appMeta.value?.edition !== 'foss' && (type === 'slack'
                ? offerSlackAppType(slackInstallsData.value, false)
                : offerDiscordAppType(discordInstallsData.value, false)) && (
                <>
                  {' '}
                  <button type="button" class="btn-link slack-app-switch"
                    onClick={() => handleTypeChange(type === 'slack' ? SLACK_APP_TYPE : DISCORD_APP_TYPE)}>
                    {type === 'slack' ? 'Use Slack (YipYap app) instead' : 'Use Discord (YipYap app) instead'}
                  </button>
                  {' to get the buttons.'}
                </>
              )}
            </p>
          )}

          {type === SLACK_APP_TYPE && (
            <SlackAppChannelFields config={config} errors={errors} canManage={canManage}
              onChange={next => setConfig(next)} />
          )}

          {type === DISCORD_APP_TYPE && (
            <DiscordAppChannelFields config={config} errors={errors} canManage={canManage}
              onChange={next => setConfig(next)} />
          )}

          {typeDef && !typeDef.custom && typeDef.fields.map(f => renderField(f))}

          {type === 'smtp' && (
            <EmailChannelFields config={config} errors={errors} isEdit={!!editId}
              platformEmail={caps.platform_email} maxRecipients={caps.email_max_recipients}
              edition={appMeta.value?.edition}
              setConfig={setConfig}
              clearErrors={keys => {
                if (keys.some(k => errors[k])) setErrors(e => Object.fromEntries(Object.entries(e).filter(([k]) => !keys.includes(k))));
              }} />
          )}

          {!editId && type === 'webhook' && <SigningCreateNote />}

          {editId && type === 'webhook' && (() => {
            const current = channels.find(c => c.id === editId);
            const shown = shownSecret?.context === 'rotate' && shownSecret.channelId === editId ? shownSecret : null;
            return current
              ? <SigningSecretSection key={current.id} channel={current} canManage={canManage}
                  shown={shown} warn={secretLeaveWarn} onRotated={onSecretRotated}
                  onCopied={markSecretCopied}
                  onDismiss={hideRotatedSecret} />
              : null;
          })()}

          {!newTelegram && (
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={enabled} onChange={e => setEnabled(e.target.checked)} />
              Enabled
            </label>
          </div>
          )}
          {deleteBlock && (
            <ChannelDeleteBlocked usedBy={deleteBlock}
              dirty={!!openedSnapshot && formSnapshot({ name, type, config, enabled }) !== openedSnapshot}
              onDismiss={() => closeConfirm(deleteBtnRef)} />
          )}
          {pendingConfirm === 'delete' && editId && (
            <InlineConfirm id="channel-delete-confirm" danger busy={deleting}
              title={deleteConfirmText(channels.find(c => c.id === editId)?.name)}
              confirmLabel="Delete channel"
              onCancel={() => closeConfirm(deleteBtnRef)}
              onConfirm={async () => {
                if (await deleteChannel(editId)) { setShowModal(false); resetForm(); }
              }} />
          )}
          {pendingConfirm === 'disable' && editId && !enabled && (
            <InlineConfirm id="channel-disable-confirm" title="Disable a channel that policies use?"
              confirmLabel="Disable and save"
              onCancel={() => closeConfirm(saveBtnRef)}
              onConfirm={() => save({ confirmedDisable: true })}>
              <p>{disableConfirmText(true, false, usedByList(channels.find(c => c.id === editId)))}</p>
              <p>Steps with no other enabled channel will not notify anyone until it is enabled again.</p>
            </InlineConfirm>
          )}
          <div class="channel-form-actions">
            {newTelegram
              ? <button type="button" class="btn" onClick={closeModal}>Cancel</button>
              : <button type="submit" class="btn btn-primary" ref={saveBtnRef}>{editId ? 'Update' : 'Create'}</button>}
            {editId && (
              <button type="button" class="btn btn-danger btn-sm" ref={deleteBtnRef}
                aria-expanded={pendingConfirm === 'delete' || !!deleteBlock ? 'true' : 'false'}
                onClick={() => requestDelete(editId)}>
                Delete Channel
              </button>
            )}
          </div>
        </form>
        )}
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
          <label>Uptime Range</label>
          {(() => {
            const plan = currentOrg.value?.plan || 'free';
            const allPeriods = ['24h', '7d', '30d', '90d'];
            const maxIdx = plan === 'free' ? 0 : allPeriods.length - 1;
            const currentPeriods = (monitor.uptime_periods || '24h').split(',').filter(Boolean);
            // Find the furthest selected period to set slider position
            let sliderVal = 0;
            allPeriods.forEach((p, i) => { if (currentPeriods.includes(p)) sliderVal = i; });
            return (
              <>
                <div style="display: flex; align-items: center; gap: 12px;">
                  <input
                    type="range"
                    min="0"
                    max={maxIdx}
                    value={sliderVal}
                    style="flex: 1; accent-color: var(--color-primary);"
                    onInput={e => {
                      const idx = parseInt(e.target.value);
                      const selected = allPeriods.slice(0, idx + 1);
                      onUpdate({ uptime_periods: selected.join(',') });
                    }}
                  />
                  <span style="font-size: 0.875rem; font-weight: 600; min-width: 36px; text-align: right;">
                    {allPeriods[sliderVal]}
                  </span>
                </div>
                <div style="display: flex; justify-content: space-between; font-size: 0.6875rem; color: var(--color-text-muted); margin-top: 4px; padding: 0 2px;">
                  {allPeriods.slice(0, maxIdx + 1).map(p => (
                    <span key={p}>{p}</span>
                  ))}
                </div>
                <p class="form-help" style="margin-top: 8px;">
                  Shows uptime bars from 24h up to {allPeriods[sliderVal]} on the status page.
                  {currentPeriods.length > 1 && ` Displaying: ${currentPeriods.join(', ')}`}
                </p>
              </>
            );
          })()}
          {(currentOrg.value?.plan || 'free') === 'free' && (
            <p class="form-help">Free plan supports 24h only. <UpgradeLink fallback="Ask an owner or admin to upgrade for more periods.">Upgrade</UpgradeLink>{isOrgAdmin(currentUser.value?.role) && ' for more periods.'}</p>
          )}
        </div>
      )}
      <button class="btn btn-xs btn-danger" onClick={onRemove}>Remove Monitor</button>
    </div>
  );
}

function StatusPageTab() {
  // Viewers are read-only (decision core-billing-org#5).
  const writer = canWrite(currentUser.value?.role);
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
    ? (isOrgAdmin(currentUser.value?.role) ? 'Upgrade to create status pages' : 'Status pages need a paid plan. Ask an owner or admin to upgrade.')
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
        {!writer ? null : atLimit
          ? <span class="text-muted" style="font-size: 0.875rem;">{limitMessage}</span>
          : <button class="btn btn-sm btn-primary" onClick={() => { closeModal(); setShowModal(true); }}>Create Status Page</button>
        }
      </div>
      <div class="settings-list">
        {pages.map(page => (
          <div key={page.id} class="settings-item" style={writer ? 'cursor: pointer;' : undefined} onClick={writer ? () => openEdit(page) : undefined}>
            <div>
              <strong>{page.name}</strong>
              <span class="text-muted"> /{page.slug}</span>
              {page.enabled
                ? <span class="label-tag" style="margin-left: 8px;">Enabled</span>
                : <span class="label-tag label-muted" style="margin-left: 8px;">Disabled</span>
              }
            </div>
            <div class="btn-group" onClick={e => e.stopPropagation()}>
              <a class="btn btn-xs" href={`${window.location.origin}/${org?.slug}/status/${page.slug}`} target="_blank" rel="noopener">View →</a>
              {writer && <button class="btn btn-xs" aria-label={`Edit status page ${page.name}`} onClick={() => openEdit(page)}>Edit</button>}
              {writer && <button class="btn btn-xs" onClick={() => openLayout(page.id)}>Layout</button>}
              {writer && <button class="btn btn-xs btn-danger" onClick={() => deletePage(page.id)}>Delete</button>}
            </div>
          </div>
        ))}
        {pages.length === 0 && <p class="text-muted">No status pages yet. Create one to share your system status publicly.</p>}
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
            {form.slug && <p class="form-help">Public URL: {window.location.origin}/{org?.slug}/status/{form.slug}</p>}
          </div>
          <div class="form-group">
            <label>Description</label>
            <textarea value={form.description} onInput={e => setForm({...form, description: e.target.value})} />
          </div>
          <div class="form-group">
            <label>Custom CSS</label>
            <textarea value={form.custom_css} onInput={e => setForm({...form, custom_css: e.target.value})}
                      style="font-family: var(--font-mono); font-size: 0.8125rem;" rows="6"
                      placeholder=".sp-component { border-color: #your-brand; }" />
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

/* ─── SSO / OIDC ─── */
function SSOTab() {
  const [connections, setConnections] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState({
    provider: '', client_id: '', client_secret: '',
    issuer_url: '', authorize_url: '', token_url: '', userinfo_url: '',
    scopes: 'openid,email,profile', auto_provision: true, default_role: 'member',
    enabled: true,
  });
  const { toasts, toast } = useToast();

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      const data = await get('/org/oidc-connections');
      setConnections(data || []);
    } catch (err) {
      setLoadError(err);
    }
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
    try {
      if (editId) await patch(`/org/oidc-connections/${editId}`, body);
      else await post('/org/oidc-connections', body);
    } catch (err) {
      // Keep the modal open so the user can correct the form.
      if (!err.readonly) toast(err.message || 'Could not save the SSO connection', 'error');
      return;
    }
    setShowModal(false);
    setEditId(null);
    resetForm();
    load();
  }

  async function deleteConn(id) {
    if (!confirm('Delete this SSO connection?')) return;
    try {
      await del(`/org/oidc-connections/${id}`);
      load();
    } catch (err) {
      if (!err.readonly) toast(err.message || 'Could not delete the SSO connection', 'error');
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
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  return (
    <div>
      <ToastContainer toasts={toasts} />
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
        {connections.length === 0 && <p class="text-muted">No SSO connections configured. Add one to let your team sign in with an identity provider.</p>}
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

/* ─── cloudevent_http accepted-replies editor ─── */

// The config shape for accepted_replies is:
//   {
//     types: ["run.yipyap.reply.alert.claimed.v1", ...],
//     rate_limit: 60,
//     trust_elevated: false,
//     allow_deregister: false,
//     max_suppress_seconds: 3600,
//     max_remediation_seconds: 600,
//     max_route_duration_seconds: 86400,
//   }
// The editor groups reply types by blast radius and gates the elevated
// group behind an explicit Trust Elevated flag. Per-type numeric caps
// appear alongside the types they apply to. Zero means "use the dispatcher
// default."

// Blast-radius groups for the reply catalog. Keep these in sync with the
// 13 types defined in internal/cloudevents/reply/handlers/. When a type
// exposes a per-channel cap, the cap_key points at the field in the
// accepted_replies config object that holds the value.
const REPLY_GROUPS = [
  {
    key: 'read_only',
    label: 'Read-only annotations (Phase 2)',
    help: 'Safe: each of these just writes an annotation or timeline entry. No alert or monitor state changes.',
    types: [
      { type: 'run.yipyap.reply.alert.claimed.v1',  label: 'Claimed',  help: 'Timeline: claimed by a person or automation.' },
      { type: 'run.yipyap.reply.alert.enriched.v1', label: 'Enriched', help: 'Context attachments (runbook links, graphs, log snippets).' },
      { type: 'run.yipyap.reply.alert.linked.v1',   label: 'Linked',   help: 'External references (Jira, PagerDuty, Slack thread).' },
    ],
  },
  {
    key: 'state_standard',
    label: 'State-mutating, standard (Phase 5)',
    help: 'Mutates alert state but is loop-safe and reversible by an operator.',
    types: [
      { type: 'run.yipyap.reply.alert.suppressed.v1',          label: 'Suppressed',         help: 'Temporarily stop notifying for this alert.', cap_key: 'max_suppress_seconds', cap_default: 3600, cap_label: 'seconds' },
      { type: 'run.yipyap.reply.alert.deduplicated.v1',        label: 'Deduplicated',       help: 'Mark as a duplicate of another alert.' },
      { type: 'run.yipyap.reply.alert.ack_with_context.v1',    label: 'Ack with context',   help: 'Acknowledge and attach investigation notes.' },
      { type: 'run.yipyap.reply.alert.ownership.v1',           label: 'Ownership',          help: 'Transfer ownership of the alert (user, team, service).' },
      { type: 'run.yipyap.reply.alert.remediation_started.v1', label: 'Remediation started', help: 'Signal that an automated or manual fix is in progress.', cap_key: 'max_remediation_seconds', cap_default: 600, cap_label: 'seconds' },
      { type: 'run.yipyap.reply.alert.remediation_result.v1',  label: 'Remediation result', help: 'Report the outcome; a success can auto-resolve the alert.' },
      { type: 'run.yipyap.reply.alert.status_page.v1',         label: 'Status page',        help: 'Publish an incident update to the public status page.' },
    ],
  },
  {
    key: 'state_elevated',
    label: 'State-mutating, elevated',
    help: 'Requires the Trust Elevated flag. These reply types can escalate pages or rewire routing, so only enable on sinks you control.',
    requires_trust: true,
    types: [
      { type: 'run.yipyap.reply.alert.escalated.v1', label: 'Escalated', help: 'Force-advance the alert on its escalation policy.' },
      { type: 'run.yipyap.reply.alert.route.v1',     label: 'Route',     help: 'Deliver the alert to a different notification channel for a bounded window.', cap_key: 'max_route_duration_seconds', cap_default: 86400, cap_label: 'seconds' },
    ],
  },
  {
    key: 'high_blast',
    label: 'High-blast-radius',
    help: 'Requires Trust Elevated AND Allow Deregister. Disabling a monitor stops all alerts from it; there is no automatic re-enable.',
    requires_trust: true,
    requires_deregister: true,
    types: [
      { type: 'run.yipyap.reply.monitor.deregister.v1', label: 'Monitor deregister', help: 'Disable the monitor that fired this alert.', warning: true },
    ],
  },
];

// List of all cap keys so normalize can coerce numeric fields safely.
const REPLY_CAP_KEYS = ['max_suppress_seconds', 'max_remediation_seconds', 'max_route_duration_seconds'];

function normalizeAcceptedReplies(value) {
  if (!value || typeof value !== 'object') {
    return {
      types: [],
      rate_limit: 0,
      trust_elevated: false,
      allow_deregister: false,
      max_suppress_seconds: 0,
      max_remediation_seconds: 0,
      max_route_duration_seconds: 0,
    };
  }
  const out = {
    types: Array.isArray(value.types) ? value.types : [],
    rate_limit: typeof value.rate_limit === 'number' ? value.rate_limit : 0,
    trust_elevated: value.trust_elevated === true,
    allow_deregister: value.allow_deregister === true,
    max_suppress_seconds: 0,
    max_remediation_seconds: 0,
    max_route_duration_seconds: 0,
  };
  for (const k of REPLY_CAP_KEYS) {
    if (typeof value[k] === 'number') out[k] = value[k];
  }
  return out;
}

// typeIsElevated returns true if the reply type lives in an elevated group
// and is therefore gated on trust_elevated being true.
function groupForType(type) {
  for (const g of REPLY_GROUPS) {
    if (g.types.some((t) => t.type === type)) return g;
  }
  return null;
}

// Filter the types array to remove anything whose group's prerequisites
// no longer hold (e.g. user unchecked Trust Elevated). Called from every
// onChange path so the persisted config can never enable a type whose
// gate is off.
function enforceGates(cfg) {
  const kept = cfg.types.filter((t) => {
    const g = groupForType(t);
    if (!g) return true; // unknown type: leave intact (future-compat)
    if (g.requires_trust && !cfg.trust_elevated) return false;
    if (g.requires_deregister && !cfg.allow_deregister) return false;
    return true;
  });
  return { ...cfg, types: kept };
}

// Lists the outbound CloudEvent types under the Event Types Filter box, with
// a button per family that adds its pattern to the list.
function EventTypePicker({ onAdd }) {
  return (
    <details class="event-type-picker">
      <summary>Event types you can filter on</summary>
      {CLOUDEVENT_TYPE_GROUPS.map(g => (
        <div key={g.pattern} class="event-type-group">
          <div class="event-type-group-head">
            <span class="event-type-group-label">{g.label}</span>
            <button type="button" class="btn btn-sm btn-secondary" onClick={() => onAdd(g.pattern)}
              aria-label={'Add ' + g.pattern + ' to the filter'}>
              Add <code>{g.pattern}</code>
            </button>
          </div>
          <ul>
            {g.types.map(t => <li key={t}><code>{t}</code></li>)}
          </ul>
        </div>
      ))}
    </details>
  );
}

// Number inputs for reply caps. Whole numbers only: the API refuses
// decimals, and validate() says so before save.
function capValue(raw) {
  if (raw === '') return 0;
  const n = Number(raw);
  return Number.isFinite(n) ? n : raw;
}

function AcceptedRepliesEditor({ id, describedBy, invalid, value, onChange }) {
  const cfg = normalizeAcceptedReplies(value);

  const commit = (next) => onChange(enforceGates(next));

  const toggleType = (group, type) => {
    if (group.requires_trust && !cfg.trust_elevated) return;
    if (group.requires_deregister && !cfg.allow_deregister) return;
    const set = new Set(cfg.types);
    if (set.has(type)) set.delete(type); else set.add(type);
    commit({ ...cfg, types: Array.from(set) });
  };

  const setCap = (key, n) => commit({ ...cfg, [key]: n });

  const setTrust = (v) => {
    // Uncheck Allow Deregister too when Trust is turned off. It's
    // meaningless on its own, and enforceGates would otherwise strip
    // monitor.deregister but leave allow_deregister visually checked.
    const next = { ...cfg, trust_elevated: v };
    if (!v) next.allow_deregister = false;
    commit(next);
  };

  const setAllowDeregister = (v) => {
    if (!cfg.trust_elevated) return; // no-op unless Trust is on
    commit({ ...cfg, allow_deregister: v });
  };

  return (
    <div class="accepted-replies-editor" id={id} role="group" tabIndex={-1}
      aria-labelledby={id ? id + '-label' : undefined} aria-describedby={describedBy}
      aria-invalid={invalid ? 'true' : undefined}>
      {REPLY_GROUPS.map((g) => {
        const gatedByTrust = g.requires_trust && !cfg.trust_elevated;
        const gatedByDeregister = g.requires_deregister && !cfg.allow_deregister;
        const disabled = gatedByTrust || gatedByDeregister;
        return (
          <div key={g.key} style="margin-bottom:14px">
            <div style="font-weight:600;margin-bottom:4px">{g.label}</div>
            {g.help && <div class="form-help" style="margin-bottom:6px">{g.help}</div>}

            {g.key === 'state_elevated' && (
              <label style="display:flex;align-items:center;gap:8px;margin:4px 0;cursor:pointer">
                <input type="checkbox"
                  checked={cfg.trust_elevated}
                  onChange={(e) => setTrust(e.target.checked)} />
                <strong>Trust elevated</strong>
                <span class="text-muted" style="font-size:0.8em">Enables the elevated handlers below.</span>
              </label>
            )}
            {g.key === 'high_blast' && (
              <label style={`display:flex;align-items:center;gap:8px;margin:4px 0;cursor:${cfg.trust_elevated ? 'pointer' : 'not-allowed'};opacity:${cfg.trust_elevated ? 1 : 0.55}`}
                     title={cfg.trust_elevated ? '' : 'Enable Trust Elevated first.'}>
                <input type="checkbox"
                  checked={cfg.allow_deregister && cfg.trust_elevated}
                  disabled={!cfg.trust_elevated}
                  onChange={(e) => setAllowDeregister(e.target.checked)} />
                <strong>Allow deregister</strong>
                <span class="text-muted" style="font-size:0.8em">Enables the Monitor deregister handler.</span>
              </label>
            )}

            <ul class="accepted-replies-list" style="list-style:none;padding:0;margin:0">
              {g.types.map((r) => {
                const checked = cfg.types.includes(r.type);
                const rowOpacity = disabled ? 0.55 : 1;
                return (
                  <li key={r.type} style={`padding:4px 0;opacity:${rowOpacity}`}>
                    <label style={`display:flex;align-items:flex-start;gap:8px;cursor:${disabled ? 'not-allowed' : 'pointer'}`}
                           title={disabled ? (gatedByTrust ? 'Enable Trust Elevated first.' : 'Enable Allow Deregister first.') : ''}>
                      <input type="checkbox"
                        checked={checked && !disabled}
                        disabled={disabled}
                        onChange={() => toggleType(g, r.type)} />
                      <span style="flex:1">
                        <strong>{r.label}</strong>{' '}<code style="font-size:0.8em">{r.type}</code>
                        <div class="form-help" style="margin-top:2px">{r.help}</div>
                        {r.cap_key && (
                          <div style="margin-top:4px;display:flex;align-items:center;gap:6px">
                            <span class="text-muted" style="font-size:0.8em">Cap:</span>
                            <input type="number" min="0" step="1" inputMode="numeric"
                              aria-label={r.label + ' cap in ' + r.cap_label + ', 0 for the default of ' + r.cap_default}
                              value={cfg[r.cap_key] || ''}
                              placeholder={r.cap_default}
                              disabled={disabled}
                              onInput={(e) => setCap(r.cap_key, capValue(e.target.value))}
                              style="width:90px" />
                            <span class="text-muted" style="font-size:0.8em">{r.cap_label} (0 = default {r.cap_default})</span>
                          </div>
                        )}
                        {r.warning && checked && !disabled && (
                          <div class="reply-blast-warning" role="note">
                            High blast radius: disabling a monitor stops all alerts from it. There is no automatic re-enable.
                          </div>
                        )}
                      </span>
                    </label>
                  </li>
                );
              })}
            </ul>
          </div>
        );
      })}

      <div style="display:flex;align-items:center;gap:8px;margin-top:10px">
        <label style="display:flex;align-items:center;gap:6px">
          Rate limit:
          <input type="number" min="0" step="1" inputMode="numeric" value={cfg.rate_limit || ''}
            placeholder="60"
            onInput={(e) => commit({ ...cfg, rate_limit: capValue(e.target.value) })}
            style="width:80px" />
          <span class="text-muted" style="font-size:0.8em">replies / min per reply type (0 = default 60)</span>
        </label>
      </div>
    </div>
  );
}
