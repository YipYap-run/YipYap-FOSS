import { useState } from 'preact/hooks';
import { post } from '../../api/client';
import { appMeta } from '../../state/auth';

// Keep in sync with TestBulkImport_TemplateImportsOnFree, which proves this
// file imports cleanly on the Free plan (300s is the Free interval floor).
const EXAMPLE_TEMPLATE = {
  notification_channels: [
    { name: "Slack Alerts", type: "slack", config: { bot_token: "xoxb-...", channel_id: "C..." } }
  ],
  teams: [
    { name: "Platform Team" }
  ],
  escalation_policies: [
    { name: "Platform On-Call" }
  ],
  monitors: [
    {
      name: "API Health",
      type: "http",
      config: { url: "https://example.com/", method: "GET", expected_status: 200 },
      interval_seconds: 300,
      timeout_seconds: 10
    },
    {
      name: "Nightly Backup",
      type: "heartbeat",
      interval_seconds: 86400
    }
  ]
};

// Section keys the API accepts, in creation order, with display names.
const SECTIONS = [
  ['notification_channels', 'notification channel', 'notification channels'],
  ['teams', 'team', 'teams'],
  ['escalation_policies', 'escalation policy', 'escalation policies'],
  ['monitors', 'monitor', 'monitors'],
];
const SECTION_KEYS = SECTIONS.map(s => s[0]);

// Keys from the old docs format, mapped to what the API reads. The server
// rejects these too; checking here gives the answer before upload.
const SECTION_HINTS = {
  channels: 'notification_channels',
  notification_channel: 'notification_channels',
  policies: 'escalation_policies',
  escalations: 'escalation_policies',
  escalation_policy: 'escalation_policies',
  monitor: 'monitors',
  team: 'teams',
};
const MONITOR_HINTS = {
  url: 'config.url',
  interval: 'interval_seconds',
  timeout: 'timeout_seconds',
  policy: 'escalation_policy_id',
  group: 'group_id',
};

const MAX_RESOURCES = 1000;
const MAX_BYTES = 1024 * 1024; // matches the server's request body limit

function plural(n, one, many) {
  return `${n} ${n === 1 ? one : many}`;
}

// checkImport parses the text and runs the shape checks that do not need
// the server. Returns { data, counts, problems }.
function checkImport(text) {
  let data;
  try {
    data = JSON.parse(text);
  } catch (err) {
    return { problems: ['The file is not valid JSON: ' + err.message] };
  }
  if (!data || typeof data !== 'object' || Array.isArray(data)) {
    return { problems: ['The file must be a JSON object with monitors, teams, escalation_policies or notification_channels.'] };
  }
  const problems = [];
  for (const key of Object.keys(data)) {
    if (SECTION_KEYS.includes(key)) continue;
    problems.push(SECTION_HINTS[key]
      ? `Unknown section "${key}". Rename it to "${SECTION_HINTS[key]}".`
      : `Unknown section "${key}". Accepted sections are monitors, teams, escalation_policies and notification_channels.`);
  }
  const counts = {};
  let total = 0;
  for (const [key] of SECTIONS) {
    if (data[key] === undefined || data[key] === null) { counts[key] = 0; continue; }
    if (!Array.isArray(data[key])) {
      problems.push(`"${key}" must be a list.`);
      counts[key] = 0;
      continue;
    }
    counts[key] = data[key].length;
    total += data[key].length;
  }
  if (Array.isArray(data.monitors)) {
    data.monitors.forEach((m, i) => {
      if (!m || typeof m !== 'object') return;
      for (const f of Object.keys(MONITOR_HINTS)) {
        if (f in m) problems.push(`${itemLabel(data, 'monitors', i)}: rename "${f}" to "${MONITOR_HINTS[f]}".`);
      }
    });
  }
  if (total === 0 && problems.length === 0) {
    problems.push('The file has nothing to import.');
  }
  if (total > MAX_RESOURCES) {
    problems.push(`An import can hold at most ${MAX_RESOURCES} items (this one has ${total}). Split it into several files.`);
  }
  return { data, counts, total, problems };
}

// itemLabel names an item for error messages, e.g. 'Monitor 3 "API Health"'.
function itemLabel(data, key, index) {
  const section = SECTIONS.find(s => s[0] === key);
  const noun = section ? section[1] : key;
  const name = data && Array.isArray(data[key]) && data[key][index] && data[key][index].name;
  const base = `${noun.charAt(0).toUpperCase()}${noun.slice(1)} ${index + 1}`;
  return typeof name === 'string' && name ? `${base} "${name}"` : base;
}

// describeError rewrites the server's "monitors[2]: ..." prefix into the
// item's position and name so people can find it in the file.
function describeError(data, msg) {
  const m = /^([a-z_]+)\[(\d+)\]:?\s*(?:"[^"]*":\s*)?(.*)$/.exec(msg);
  if (m && SECTION_KEYS.includes(m[1])) {
    return `${itemLabel(data, m[1], Number(m[2]))}: ${m[3]}`;
  }
  return msg;
}

const panelStyle = 'padding: 12px 16px; border-radius: 8px; margin-bottom: 12px; font-size: 0.875rem; border: 1px solid var(--color-border); background: var(--color-surface-elevated);';
const dangerStyle = 'padding: 12px 16px; border-radius: 8px; margin-bottom: 12px; font-size: 0.875rem; border: 1px solid var(--color-down); background: var(--color-down-bg); color: var(--color-down-text);';
const headingStyle = 'margin: 0 0 6px 0; font-size: 0.9375rem;';
const listStyle = 'margin: 0; padding-left: 20px; overflow-wrap: anywhere;';

export function ImportTab() {
  const [jsonText, setJsonText] = useState('');
  const [fileName, setFileName] = useState('');
  const [check, setCheck] = useState(null);       // result of checkImport, shown as preview or problems
  const [result, setResult] = useState(null);     // successful (200) import summary
  const [rejection, setRejection] = useState(null); // { message, errors, upgrade }
  const [importing, setImporting] = useState(false);

  function clearOutcome() {
    setCheck(null);
    setResult(null);
    setRejection(null);
  }

  function handleFileUpload(e) {
    const file = e.target.files[0];
    clearOutcome();
    if (!file) return;
    if (file.size > MAX_BYTES) {
      setFileName('');
      setRejection({ message: 'The file is larger than 1 MB. Split it into several files.', errors: [] });
      e.target.value = '';
      return;
    }
    setFileName(file.name);
    const reader = new FileReader();
    reader.onload = ev => setJsonText(String(ev.target.result || ''));
    reader.onerror = () => setRejection({ message: 'Could not read the file. Try again or paste the JSON below.', errors: [] });
    reader.readAsText(file);
  }

  function handleValidate() {
    clearOutcome();
    setCheck(checkImport(jsonText));
  }

  async function handleImport() {
    clearOutcome();
    const checked = checkImport(jsonText);
    if (checked.problems.length > 0) {
      setCheck(checked);
      return;
    }
    setImporting(true);
    try {
      const res = await post('/import', checked.data);
      setResult({ ...res, data: checked.data });
      setJsonText('');
      setFileName('');
    } catch (err) {
      const body = err.body || {};
      const errors = Array.isArray(body.errors) ? body.errors.map(e => describeError(checked.data, e)) : [];
      setRejection({
        message: errors.length > 0 ? 'Import rejected. Nothing was created.' : (err.message || 'Import failed. Nothing was created.'),
        errors,
        upgrade: !!body.upgrade_required,
      });
    } finally {
      setImporting(false);
    }
  }

  function downloadTemplate() {
    const blob = new Blob([JSON.stringify(EXAMPLE_TEMPLATE, null, 2)], { type: 'application/json' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'yipyap-import-template.json';
    document.body.appendChild(a);
    a.click();
    a.remove();
    setTimeout(() => URL.revokeObjectURL(url), 0);
  }

  const created = result ? result.created || {} : {};
  const createdTotal = Object.values(created).reduce((a, b) => a + (Number(b) || 0), 0);
  const resultErrors = result && Array.isArray(result.errors) ? result.errors.map(e => describeError(result.data, e)) : [];
  const importedHeartbeat = result && Array.isArray(result.data?.monitors) && result.data.monitors.some(m => m && m.type === 'heartbeat');
  const showBilling = appMeta.value?.edition !== 'foss';

  return (
    <div>
      <div style="display: flex; flex-wrap: wrap; gap: 8px; align-items: center; justify-content: space-between; margin-bottom: 16px;">
        <h3 style="margin: 0;">Bulk Import</h3>
        <button type="button" class="btn btn-sm" onClick={downloadTemplate}>Download template</button>
      </div>
      <p style="color: var(--color-text-secondary); margin-bottom: 16px; font-size: 0.875rem;">
        Import monitors, teams, escalation policies, and notification channels from a JSON file.
        Each item gets the same checks and plan limits as creating it by hand. If any item fails, nothing is imported.
        Resources are created in this order: channels, teams, policies, monitors.
      </p>

      <div class="form-group">
        <label for="import-file">JSON file</label>
        <input id="import-file" type="file" accept=".json,application/json" onChange={handleFileUpload} aria-describedby="import-file-help" />
        <p id="import-file-help" class="form-help">
          {fileName ? `Loaded ${fileName}. You can edit it below before importing.` : `Up to 1 MB and ${MAX_RESOURCES} items per import.`}
        </p>
      </div>

      <div class="form-group">
        <label for="import-json">Or paste JSON</label>
        <textarea
          id="import-json"
          rows="12"
          value={jsonText}
          onInput={e => { setJsonText(e.target.value); clearOutcome(); }}
          placeholder={JSON.stringify(EXAMPLE_TEMPLATE, null, 2)}
          spellcheck={false}
          autocomplete="off"
          aria-describedby="import-json-help"
          style="font-family: monospace; font-size: 0.8125rem;"
        />
        <p id="import-json-help" class="form-help">
          Sections: notification_channels, teams, escalation_policies, monitors. Monitor targets go in "config", and heartbeat monitors get their ping URL after import.
        </p>
      </div>

      <div style="display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 16px;">
        <button type="button" class="btn" onClick={handleValidate} disabled={!jsonText.trim() || importing}>Check file</button>
        <button type="button" class="btn btn-primary" onClick={handleImport} disabled={!jsonText.trim() || importing} aria-busy={importing ? 'true' : 'false'}>
          {importing ? 'Importing...' : 'Import'}
        </button>
      </div>

      <div aria-live="polite">
        {check && check.problems.length === 0 && (
          <div style={panelStyle}>
            <h4 style={headingStyle}>Ready to import</h4>
            <ul style={listStyle}>
              {SECTIONS.filter(([key]) => check.counts[key] > 0).map(([key, one, many]) => (
                <li key={key}>{plural(check.counts[key], one, many)}</li>
              ))}
            </ul>
            <p class="form-help" style="margin-top: 8px;">This checks the file format only. Plan limits, targets and references are checked when you import.</p>
          </div>
        )}

        {result && (
          <div style={panelStyle}>
            <h4 style={headingStyle}>{resultErrors.length > 0 ? 'Imported with errors' : 'Import complete'}</h4>
            {createdTotal > 0 ? (
              <ul style={listStyle}>
                {SECTIONS.filter(([key]) => created[key] > 0).map(([key, one, many]) => (
                  <li key={key}>{plural(created[key], one, many)} created</li>
                ))}
              </ul>
            ) : (
              <p style="margin: 0;">Nothing was created.</p>
            )}
            {resultErrors.length > 0 && (
              <>
                <p style="margin: 8px 0 4px 0; color: var(--color-down-text);">These items could not be saved:</p>
                <ul style={listStyle + ' color: var(--color-down-text);'}>
                  {resultErrors.map((e, i) => <li key={i}>{e}</li>)}
                </ul>
              </>
            )}
            {created.monitors > 0 && (
              <p style="margin: 8px 0 0 0;">
                <a href="/monitors">View monitors</a>
                {importedHeartbeat && '. Open each heartbeat monitor to copy its ping URL.'}
              </p>
            )}
          </div>
        )}
      </div>

      {check && check.problems.length > 0 && (
        <div role="alert" style={dangerStyle}>
          <h4 style={headingStyle}>Fix the file before importing</h4>
          <ul style={listStyle}>
            {check.problems.map((p, i) => <li key={i}>{p}</li>)}
          </ul>
        </div>
      )}

      {rejection && (
        <div role="alert" style={dangerStyle}>
          <h4 style={headingStyle}>{rejection.message}</h4>
          {rejection.errors.length > 0 && (
            <ul style={listStyle}>
              {rejection.errors.map((e, i) => <li key={i}>{e}</li>)}
            </ul>
          )}
          {rejection.upgrade && showBilling && (
            <p style="margin: 8px 0 0 0;">
              Remove items to fit your plan, or <a href="/settings/billing" style="color: inherit; text-decoration: underline;">upgrade</a>.
            </p>
          )}
        </div>
      )}
    </div>
  );
}
