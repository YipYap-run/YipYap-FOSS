import { useState } from 'preact/hooks';
import { post } from '../../api/client';

const EXAMPLE_TEMPLATE = {
  monitors: [
    {
      name: "API Health",
      type: "http",
      config: { url: "https://api.example.com/health", method: "GET", expected_status: 200 },
      interval_seconds: 60,
      timeout_seconds: 10
    }
  ],
  teams: [
    { name: "Platform Team" }
  ],
  notification_channels: [
    { name: "Slack Alerts", type: "slack", config: { token: "xoxb-...", channel_id: "C..." } }
  ]
};

export function ImportTab() {
  const [jsonText, setJsonText] = useState('');
  const [preview, setPreview] = useState(null);
  const [result, setResult] = useState(null);
  const [error, setError] = useState(null);
  const [importing, setImporting] = useState(false);

  function handleFileUpload(e) {
    const file = e.target.files[0];
    if (!file) return;
    const reader = new FileReader();
    reader.onload = ev => setJsonText(ev.target.result);
    reader.readAsText(file);
  }

  function handleValidate() {
    setError(null);
    setPreview(null);
    setResult(null);
    try {
      const data = JSON.parse(jsonText);
      const summary = {
        monitors: (data.monitors || []).length,
        teams: (data.teams || []).length,
        escalation_policies: (data.escalation_policies || []).length,
        notification_channels: (data.notification_channels || []).length,
      };
      setPreview(summary);
    } catch (err) {
      setError('Invalid JSON: ' + err.message);
    }
  }

  async function handleImport() {
    setError(null);
    setResult(null);
    setImporting(true);
    try {
      const data = JSON.parse(jsonText);
      const res = await post('/import', data);
      setResult(res);
      setPreview(null);
    } catch (err) {
      setError(err.message || 'Import failed');
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
    a.click();
    URL.revokeObjectURL(url);
  }

  return (
    <div>
      <div style="display: flex; align-items: center; justify-content: space-between; margin-bottom: 16px;">
        <h3 style="margin: 0;">Bulk Import</h3>
        <button class="btn btn-sm" onClick={downloadTemplate}>Download Template</button>
      </div>
      <p style="color: var(--color-text-secondary); margin-bottom: 16px; font-size: 0.875rem;">
        Import monitors, teams, escalation policies, and notification channels from a JSON file.
        Resources are created in dependency order: channels, then teams, then policies, then monitors.
      </p>

      <div class="form-group">
        <label>Upload JSON file</label>
        <input type="file" accept=".json,application/json" onChange={handleFileUpload} />
      </div>

      <div class="form-group">
        <label>Or paste JSON below</label>
        <textarea
          rows="12"
          value={jsonText}
          onInput={e => setJsonText(e.target.value)}
          placeholder={JSON.stringify(EXAMPLE_TEMPLATE, null, 2)}
          style="font-family: monospace; font-size: 0.8125rem;"
        />
      </div>

      <div style="display: flex; gap: 8px; margin-bottom: 16px;">
        <button class="btn" onClick={handleValidate} disabled={!jsonText.trim()}>Validate</button>
        <button class="btn btn-primary" onClick={handleImport} disabled={!jsonText.trim() || importing}>
          {importing ? 'Importing...' : 'Import'}
        </button>
      </div>

      {error && (
        <div style="padding: 12px; background: var(--color-danger-bg, #fef2f2); color: var(--color-danger, #ef4444); border-radius: 8px; margin-bottom: 12px; font-size: 0.875rem;">
          {error}
        </div>
      )}

      {preview && (
        <div style="padding: 12px; background: var(--color-surface-raised, #1e293b); border-radius: 8px; margin-bottom: 12px;">
          <h4 style="margin: 0 0 8px 0; font-size: 0.9375rem;">Preview - will create:</h4>
          <ul style="margin: 0; padding-left: 20px; font-size: 0.875rem;">
            {preview.monitors > 0 && <li>{preview.monitors} monitor(s)</li>}
            {preview.teams > 0 && <li>{preview.teams} team(s)</li>}
            {preview.escalation_policies > 0 && <li>{preview.escalation_policies} escalation policy/policies</li>}
            {preview.notification_channels > 0 && <li>{preview.notification_channels} notification channel(s)</li>}
          </ul>
        </div>
      )}

      {result && (
        <div style="padding: 12px; background: var(--color-surface-raised, #1e293b); border-radius: 8px;">
          <h4 style="margin: 0 0 8px 0; font-size: 0.9375rem;">Import Results</h4>
          <ul style="margin: 0; padding-left: 20px; font-size: 0.875rem;">
            {Object.entries(result.created || {}).map(([key, count]) => (
              <li key={key}>{key}: {count} created</li>
            ))}
          </ul>
          {result.errors && result.errors.length > 0 && (
            <div style="margin-top: 8px;">
              <h4 style="margin: 0 0 4px 0; font-size: 0.9375rem; color: var(--color-danger, #ef4444);">Errors:</h4>
              <ul style="margin: 0; padding-left: 20px; font-size: 0.8125rem; color: var(--color-danger, #ef4444);">
                {result.errors.map((e, i) => <li key={i}>{e}</li>)}
              </ul>
            </div>
          )}
        </div>
      )}
    </div>
  );
}
