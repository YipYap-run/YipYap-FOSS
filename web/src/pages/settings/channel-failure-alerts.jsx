import { useState, useEffect } from 'preact/hooks';
import { get, put } from '../../api/client';
import { Card, LoadingPage } from '../../components/ui';
import { parseThreshold, thresholdSummary } from './channel-failure-alerts-logic.mjs';
import './channel-permissions.css';

// ChannelFailureAlertsCard sets how many failed deliveries in a row make
// YipYap email the org owners about a channel. Owners and admins can
// change it; everyone else sees the current value.
export function ChannelFailureAlertsCard({ toast }) {
  const [loaded, setLoaded] = useState(null);
  const [value, setValue] = useState('');
  const [loadError, setLoadError] = useState('');
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  function load() {
    setLoadError('');
    get('/org/channel-failure-alerts').then(r => {
      setLoaded(r);
      setValue(String(r.threshold));
    }).catch(err => setLoadError(err?.message || 'Could not load the failure email setting.'));
  }
  useEffect(load, []);

  if (loadError) {
    return (
      <Card>
        <p class="field-error" role="alert">{loadError}</p>
        <button type="button" class="btn btn-sm" onClick={load}>Try again</button>
      </Card>
    );
  }
  if (!loaded) return <Card><LoadingPage /></Card>;

  const editable = !!loaded.can_configure;
  const dirty = value.trim() !== String(loaded.threshold);

  async function save(e) {
    e.preventDefault();
    const parsed = parseThreshold(value, loaded);
    if (parsed.error) {
      setError(parsed.error);
      return;
    }
    setSaving(true);
    setError('');
    try {
      const updated = await put('/org/channel-failure-alerts', { threshold: parsed.value });
      setLoaded(updated);
      setValue(String(updated.threshold));
      toast && toast('Failure email setting saved');
    } catch (err) {
      setError(err?.message || 'Could not save the failure email setting.');
    }
    setSaving(false);
  }

  return (
    <Card class="chanperm">
      <form onSubmit={save} aria-busy={saving ? 'true' : undefined} noValidate>
        <p class="chanperm-intro">
          When a channel fails to deliver several times in a row, YipYap emails the organization
          owners once, with the last error and a link to the channel settings. When the channel
          delivers again, they get a short follow-up.
        </p>
        {loaded.email_enabled === false && (
          <p class="chanperm-warning" role="note">
            This server has no outgoing email set up, so these emails are not sent. Failing
            channels still show on the Channels tab.
          </p>
        )}
        <div class="form-group">
          <label for="chanfail-threshold">Failed deliveries in a row before owners are emailed</label>
          <input id="chanfail-threshold" type="number" inputmode="numeric" min={loaded.min} max={loaded.max} step="1"
            value={value} disabled={!editable || saving}
            aria-invalid={error ? 'true' : undefined}
            aria-describedby={error ? 'chanfail-help chanfail-error' : 'chanfail-help'}
            onInput={e => { setError(''); setValue(e.target.value); }}
            style="max-width: 8rem" />
          <p class="chanperm-help" id="chanfail-help">
            {thresholdSummary(loaded.threshold)} Allowed: {loaded.min} to {loaded.max}. Default: {loaded.default}.
          </p>
          {error && <p class="field-error" id="chanfail-error" role="alert">{error}</p>}
        </div>
        {editable ? (
          <div class="chanperm-actions">
            <button type="submit" class="btn btn-primary" disabled={saving || !dirty}>
              {saving ? 'Saving...' : 'Save'}
            </button>
          </div>
        ) : (
          <p class="chanperm-help">Only owners and admins can change this setting.</p>
        )}
      </form>
    </Card>
  );
}
