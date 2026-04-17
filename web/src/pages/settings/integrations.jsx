import { useState, useEffect } from 'preact/hooks';
import { get, put, del, post } from '../../api/client';

export function IntegrationsTab() {
  const [config, setConfig] = useState(null);
  const [loading, setLoading] = useState(true);
  const [endpoint, setEndpoint] = useState('');
  const [headers, setHeaders] = useState('');
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState(null);
  const [error, setError] = useState('');
  const [success, setSuccess] = useState('');

  async function loadConfig() {
    setLoading(true);
    try {
      const data = await get('/integrations/otel');
      setConfig(data);
      setEndpoint(data.endpoint || '');
      // Don't populate headers -- they're never returned from the API
    } catch (err) {
      setError(err.message || 'Failed to load configuration');
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { loadConfig(); }, []);

  async function handleSave(e) {
    e.preventDefault();
    setSaving(true);
    setError('');
    setSuccess('');
    setTestResult(null);
    try {
      const data = await put('/integrations/otel', { endpoint, headers });
      setConfig(data);
      setHeaders(''); // Clear the field after save
      setSuccess('OTEL export configuration saved');
    } catch (err) {
      setError(err.message || 'Failed to save configuration');
    } finally {
      setSaving(false);
    }
  }

  async function handleTest() {
    setTesting(true);
    setTestResult(null);
    try {
      const result = await post('/integrations/otel/test', {});
      setTestResult(result);
    } catch (err) {
      setTestResult({ success: false, error: err.message });
    } finally {
      setTesting(false);
    }
  }

  async function handleRemove() {
    if (!confirm('Remove OTEL export configuration? Metrics will stop being exported.')) return;
    setError('');
    setSuccess('');
    try {
      const data = await del('/integrations/otel');
      setConfig(data);
      setEndpoint('');
      setHeaders('');
      setSuccess('OTEL export configuration removed');
    } catch (err) {
      setError(err.message || 'Failed to remove configuration');
    }
  }

  if (loading) return <div style="padding: 20px; color: var(--color-text-muted)">Loading...</div>;

  return (
    <div>
      <div class="card">
        <div class="section-header">
          <h3>OpenTelemetry Export</h3>
        </div>

        <p style="color: var(--color-text-secondary); font-size: 0.875rem; margin-bottom: 16px; line-height: 1.6">
          Export your monitoring metrics to any OTLP-compatible backend (Grafana Cloud, Honeycomb, Datadog, etc.).
          Metrics are exported every 60 seconds.
        </p>

        {config?.enabled && (
          <div style={{
            display: 'flex',
            alignItems: 'center',
            gap: 8,
            padding: '8px 12px',
            borderRadius: 'var(--radius-sm)',
            background: 'var(--color-up-bg)',
            marginBottom: 16,
            fontSize: '0.8125rem',
          }}>
            <span style={{
              width: 8, height: 8, borderRadius: '50%',
              background: 'var(--color-up)',
            }} />
            Exporting to {config.endpoint}
            {config.headers_configured && ' (with auth headers)'}
          </div>
        )}

        <form onSubmit={handleSave}>
          <div class="form-group">
            <label>OTLP Endpoint</label>
            <input
              type="text"
              value={endpoint}
              onInput={e => setEndpoint(e.target.value)}
              placeholder="collector.example.com:4317"
              required
            />
            <span style="font-size: 0.75rem; color: var(--color-text-muted)">
              gRPC endpoint in host:port format
            </span>
          </div>

          <div class="form-group">
            <label>Headers {config?.headers_configured && '(already configured)'}</label>
            <input
              type="password"
              value={headers}
              onInput={e => setHeaders(e.target.value)}
              placeholder={config?.headers_configured ? 'Leave blank to keep existing' : 'x-api-key=your-key'}
            />
            <span style="font-size: 0.75rem; color: var(--color-text-muted)">
              One header per line, key=value format. Used for authentication.
            </span>
          </div>

          {error && <div class="form-error">{error}</div>}
          {success && <div class="form-success" style="color: var(--color-up); margin-bottom: 8px">{success}</div>}

          {testResult && (
            <div style={{
              padding: '8px 12px',
              borderRadius: 'var(--radius-sm)',
              background: testResult.success ? 'var(--color-up-bg)' : 'var(--color-down-bg)',
              color: testResult.success ? 'var(--color-up)' : 'var(--color-down)',
              marginBottom: 12,
              fontSize: '0.8125rem',
            }}>
              {testResult.success ? 'Connection successful' : `Connection failed: ${testResult.error}`}
            </div>
          )}

          <div style="display: flex; gap: 8px; flex-wrap: wrap">
            <button type="submit" class="btn btn-primary" disabled={saving}>
              {saving ? 'Saving...' : 'Save Configuration'}
            </button>

            {config?.enabled && (
              <button type="button" class="btn" onClick={handleTest} disabled={testing}>
                {testing ? 'Testing...' : 'Test Connection'}
              </button>
            )}

            {config?.enabled && (
              <button type="button" class="btn" onClick={handleRemove}
                style="color: var(--color-down); border-color: var(--color-down)">
                Remove
              </button>
            )}
          </div>
        </form>
      </div>

      <div class="card" style="margin-top: 16px">
        <div class="section-header">
          <h3>Exported Metrics</h3>
        </div>
        <p style="color: var(--color-text-secondary); font-size: 0.875rem; line-height: 1.6; margin-bottom: 12px">
          Only your organization's data is exported. Platform metrics are never shared.
          Metrics start exporting from the moment you save. Historical data is not backfilled.
          If your collector is unreachable, metrics from that period will be lost.
        </p>
        <div style="display: flex; flex-direction: column; gap: 6px; font-size: 0.8125rem">
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.check.latency_ms</code>
            <span style="color: var(--color-text-muted)">Monitor check latency</span>
          </div>
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.check.status</code>
            <span style="color: var(--color-text-muted)">Monitor up/down state</span>
          </div>
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.alerts.active</code>
            <span style="color: var(--color-text-muted)">Currently firing alerts</span>
          </div>
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.notification.sent</code>
            <span style="color: var(--color-text-muted)">Notifications delivered</span>
          </div>
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.notification.fail</code>
            <span style="color: var(--color-text-muted)">Failed notification attempts</span>
          </div>
          <div style="display: flex; gap: 8px; align-items: center">
            <code style="min-width: 220px">yipyap.escalation.step</code>
            <span style="color: var(--color-text-muted)">Escalation progression</span>
          </div>
        </div>
      </div>
    </div>
  );
}
