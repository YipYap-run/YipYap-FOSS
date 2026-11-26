// Pure helpers for the "Send through" choice of Slack and Discord DM
// contact methods. Kept free of Preact so `node --test` can check them
// against the API:
//
//   GET users/me/contact-method-options
//     integrations: [{id, name, type: "slack"|"discord"|"telegram",
//       enabled, source: "app"|"own_bot"|"platform_bot", own_bot}]
//     source "app" is a YipYap Slack or Discord app install (hosted
//     service only); the method stores it as config.via_install_id.
//     source "own_bot" is an org channel with its own bot token; the
//     method stores it as config.via_channel_id. Exactly one is sent.
//   GET integrations/slack/installations/{id}/me (any member)
//     200 {found: true, slack_user_id, name, email}
//     200 {found: false, email}   no member of the workspace has that email
//     404 workspace not found, 409 workspace needs reconnecting,
//     429 too many lookups, 502 Slack did not answer.
//     It only looks up the caller's own account email.

const KIND_NAME = { slack: 'Slack', discord: 'Discord' };

// The select value for one integration. App installs and channels live in
// different tables, so the value carries the source.
export function viaOptionValue(i) {
  return (i.source === 'app' ? 'app:' : 'channel:') + i.id;
}

export function viaLabel(i) {
  const name = i.name || '';
  const base = i.source === 'app'
    ? `${KIND_NAME[i.type] || i.type} app: ${name}`
    : `Own bot: ${name}`;
  return i.enabled ? base : `${base} (disabled)`;
}

// Options for the Send through select of a Slack or Discord DM, app
// installs first.
export function viaOptions(integrations, kind) {
  const list = Array.isArray(integrations) ? integrations : [];
  const mine = list.filter(i => i && i.type === kind);
  const apps = mine.filter(i => i.source === 'app');
  const own = mine.filter(i => i.source !== 'app');
  return [...apps, ...own].map(i => ({
    value: viaOptionValue(i), label: viaLabel(i), enabled: !!i.enabled,
    source: i.source === 'app' ? 'app' : 'own_bot', id: i.id, name: i.name || '',
  }));
}

// The select value for a stored or edited config.
export function viaValue(config) {
  const c = config || {};
  if (c.via_install_id) return 'app:' + c.via_install_id;
  if (c.via_channel_id) return 'channel:' + c.via_channel_id;
  return '';
}

// viaSource is 'app', 'own_bot' or '' when nothing is chosen yet.
export function viaSource(config) {
  const v = viaValue(config);
  if (v.startsWith('app:')) return 'app';
  if (v.startsWith('channel:')) return 'own_bot';
  return '';
}

// applyVia returns config with the choice from the select, clearing the
// other reference so only one is ever sent.
export function applyVia(config, value) {
  const out = { ...(config || {}) };
  delete out.via_install_id;
  delete out.via_channel_id;
  if (typeof value === 'string' && value.startsWith('app:')) out.via_install_id = value.slice(4);
  else if (typeof value === 'string' && value.startsWith('channel:')) out.via_channel_id = value.slice(8);
  return out;
}

// The first enabled option, used to preselect Send through on a new form so
// the right ID field shows straight away.
export function defaultVia(integrations, kind) {
  const first = viaOptions(integrations, kind).find(o => o.enabled);
  return first ? first.value : '';
}

// The integration a config sends through, or undefined.
export function viaIntegration(integrations, config) {
  const v = viaValue(config);
  if (!v || !Array.isArray(integrations)) return undefined;
  return integrations.find(i => i && viaOptionValue(i) === v);
}

// A field marked via: 'app' or 'own_bot' only applies to that sender.
export function fieldApplies(field, config) {
  if (!field || !field.via) return true;
  return field.via === viaSource(config);
}

// What the form says after Find me in Slack.
export function slackLookupResult(res, workspace) {
  const where = workspace || 'this workspace';
  if (res && res.found && res.slack_user_id) {
    const name = res.name ? '@' + res.name : res.slack_user_id;
    return { found: true, id: res.slack_user_id, message: `Found you as ${name} in ${where}.` };
  }
  const email = res && res.email ? res.email : 'your YipYap email';
  return {
    found: false, id: '',
    message: `No member of ${where} uses ${email}. Enter your Slack member ID below instead.`,
  };
}

export function slackLookupError(err) {
  const status = err && err.status;
  const raw = (err && err.body && err.body.error) || (err && err.message) || '';
  if (status === 429) return 'Too many lookups. Wait a minute, or enter your member ID below.';
  if (raw) return sentence(raw);
  return 'Could not look you up in Slack. Enter your member ID below.';
}

function sentence(msg) {
  const s = String(msg).trim();
  if (!s) return s;
  const out = s.charAt(0).toUpperCase() + s.slice(1);
  return /[.!?]$/.test(out) ? out : out + '.';
}
