import { useState, useEffect, useRef } from 'preact/hooks';
import { get, put } from '../../api/client';
import { Card, Spinner, ErrorMessage } from '../../components/ui';

// Org setting for who can work with support tickets opened by someone else.
// Owners and admins always have full access and the opener always keeps full
// access to their own ticket, so only member and viewer are configurable.
// Backed by GET/PUT /api/v1/orgs/support-access (owner/admin only).

const ROLE_LABELS = {
  member: { title: 'Members', hint: 'Can manage monitors and alerts.' },
  viewer: { title: 'Viewers', hint: 'Read-only access to the org.' },
};

const LEVEL_OPTIONS = [
  { value: 'none', label: 'Own tickets only', hint: 'Sees only the tickets they open.' },
  { value: 'see', label: 'Read', hint: 'Reads every ticket in the org.' },
  { value: 'reply', label: 'Read and reply', hint: 'Reads and replies to every ticket.' },
  { value: 'close', label: 'Read, reply and close', hint: 'Reads, replies to and closes every ticket.' },
];

const optionStyle = {
  display: 'flex',
  alignItems: 'flex-start',
  gap: 10,
  padding: '10px 12px',
  border: '1px solid var(--color-border)',
  borderRadius: 'var(--radius-sm)',
  cursor: 'pointer',
  minHeight: 44,
};

const optionSelectedStyle = {
  ...optionStyle,
  borderColor: 'var(--color-primary)',
  background: 'var(--color-primary-bg)',
};

export function SupportAccessTab() {
  const [saved, setSaved] = useState(null);
  const [levels, setLevels] = useState({});
  const [roles, setRoles] = useState(['member', 'viewer']);
  const [values, setValues] = useState(LEVEL_OPTIONS.map(o => o.value));
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [status, setStatus] = useState('');
  // Discard removes the button that had focus, so focus moves to the
  // first choice of the first role instead of falling back to the page.
  const formRef = useRef(null);

  function apply(data) {
    const next = { ...(data?.levels || {}) };
    setSaved(next);
    setLevels(next);
    if (Array.isArray(data?.roles) && data.roles.length) setRoles(data.roles);
    if (Array.isArray(data?.values) && data.values.length) setValues(data.values);
  }

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      apply(await get('/orgs/support-access'));
    } catch (err) {
      setLoadError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  const changed = saved ? roles.filter(r => (levels[r] || 'none') !== (saved[r] || 'none')) : [];
  const dirty = changed.length > 0;
  // Roles that go from own tickets only to seeing everyone's tickets. Saving
  // exposes past tickets too, so say so before the admin saves.
  const widened = changed.filter(r => (saved[r] || 'none') === 'none' && (levels[r] || 'none') !== 'none');

  function choose(role, value) {
    setLevels(l => ({ ...l, [role]: value }));
    setStatus('');
    setSaveError('');
  }

  function discard() {
    setLevels({ ...saved });
    setSaveError('');
    setStatus('Changes discarded.');
    setTimeout(() => formRef.current?.querySelector('input:checked')?.focus(), 0);
  }

  async function save(e) {
    e.preventDefault();
    if (!dirty) return;
    setSaving(true);
    setSaveError('');
    setStatus('');
    const body = { levels: {} };
    for (const r of changed) body.levels[r] = levels[r] || 'none';
    try {
      apply(await put('/orgs/support-access', body));
      setStatus('Support access saved.');
    } catch (err) {
      setSaveError(err.message || 'Could not save support access. Try again.');
    } finally {
      setSaving(false);
    }
  }

  if (loading) {
    return (
      <Card class="support-access">
        <div role="status" aria-live="polite" style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
          <Spinner />
          <span>Loading support access</span>
        </div>
      </Card>
    );
  }
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  const options = LEVEL_OPTIONS.filter(o => values.includes(o.value));

  return (
    <Card class="support-access">
      <h3 style={{ margin: '0 0 6px' }}>Support ticket access</h3>
      <p style={{ color: 'var(--color-text-secondary)', margin: '0 0 6px', fontSize: '0.875rem', lineHeight: 1.5 }}>
        A support ticket is private to the person who opened it and to your owners and admins.
        Choose what members and viewers can do with tickets that a colleague opened.
      </p>
      <p style={{ color: 'var(--color-text-secondary)', margin: '0 0 20px', fontSize: '0.8125rem', lineHeight: 1.5 }}>
        Everyone keeps full access to tickets they open. Only the person who opened a ticket can rate it.
        Changes apply to existing tickets as well as new ones.
      </p>

      <form ref={formRef} onSubmit={save}>
        {roles.map(role => {
          const meta = ROLE_LABELS[role] || { title: role, hint: '' };
          const current = levels[role] || 'none';
          const hintId = `support-access-${role}-hint`;
          return (
            <fieldset
              key={role}
              aria-describedby={meta.hint ? hintId : undefined}
              style={{ border: 'none', padding: 0, margin: '0 0 20px', minWidth: 0 }}
            >
              <legend style={{ fontWeight: 600, fontSize: '0.9375rem', padding: 0, marginBottom: 2 }}>
                {meta.title}
              </legend>
              {meta.hint && (
                <p id={hintId} style={{ color: 'var(--color-text-secondary)', margin: '0 0 8px', fontSize: '0.8125rem' }}>{meta.hint}</p>
              )}
              <div style={{ display: 'grid', gap: 8, gridTemplateColumns: 'repeat(auto-fit, minmax(200px, 1fr))' }}>
                {options.map(opt => {
                  const id = `support-access-${role}-${opt.value}`;
                  const selected = current === opt.value;
                  return (
                    <label key={opt.value} for={id} style={selected ? optionSelectedStyle : optionStyle}>
                      <input
                        type="radio"
                        id={id}
                        name={`support-access-${role}`}
                        value={opt.value}
                        checked={selected}
                        onChange={() => choose(role, opt.value)}
                        style={{ marginTop: 3, flex: '0 0 auto', width: 16, height: 16 }}
                      />
                      <span style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
                        <span style={{ fontWeight: 500, fontSize: '0.875rem' }}>{opt.label}</span>
                        <span style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem' }}>{opt.hint}</span>
                      </span>
                    </label>
                  );
                })}
              </div>
            </fieldset>
          );
        })}

        {widened.length > 0 && (
          <p id="support-access-widen-note" style={{
            margin: '0 0 12px',
            padding: '10px 12px',
            borderRadius: 'var(--radius-sm)',
            background: 'var(--color-warning-bg)',
            border: '1px solid var(--color-warning)',
            fontSize: '0.8125rem',
            lineHeight: 1.5,
          }}>
            {widened.map(r => (ROLE_LABELS[r]?.title || r)).join(' and ')} will be able to read every ticket
            your org has opened, including tickets opened before this change.
          </p>
        )}

        {saveError && (
          <div class="form-error" role="alert" style={{ marginBottom: 12 }}>{saveError}</div>
        )}

        <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
          <button
            type="submit"
            class="btn btn-primary"
            disabled={!dirty || saving}
            aria-describedby={widened.length > 0 ? 'support-access-widen-note' : undefined}
          >
            {saving ? 'Saving...' : 'Save changes'}
          </button>
          {dirty && !saving && (
            <button type="button" class="btn" onClick={discard}>
              Discard
            </button>
          )}
          <span role="status" aria-live="polite" style={{ color: 'var(--color-text-secondary)', fontSize: '0.8125rem' }}>
            {status || (dirty ? 'Unsaved changes' : '')}
          </span>
        </div>
      </form>
    </Card>
  );
}

// Shown when someone other than an owner or admin opens /settings/support
// directly, so the page is not blank.
export function SupportAccessDenied() {
  return (
    <Card class="support-access">
      <h3 style={{ margin: '0 0 6px' }}>Support ticket access</h3>
      <p style={{ color: 'var(--color-text-secondary)', margin: 0, fontSize: '0.875rem', lineHeight: 1.5 }}>
        Only owners and admins can change who sees support tickets. Ask one of them if you need
        access to tickets a colleague opened.
      </p>
    </Card>
  );
}
