import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, formatTime } from '../../components/ui';
import { SSOEnforcement } from './sso-enforcement';
import '../../styles/sso-domains.css';

const EMPTY_FORM = {
  provider: '', client_id: '', client_secret: '',
  issuer_url: '', authorize_url: '', token_url: '', userinfo_url: '',
  scopes: 'openid,email,profile', auto_provision: true, default_role: 'member',
  enabled: true,
};

// errorText returns the server message for a failed request, or a fallback.
function errorText(err, fallback) {
  if (err?.readonly) return err.message;
  if (err?.status === 429) return 'Too many attempts. Wait a minute and try again.';
  const msg = err?.body?.error;
  if (!msg) return fallback;
  return msg.charAt(0).toUpperCase() + msg.slice(1) + (/[.!?]$/.test(msg) ? '' : '.');
}

/* ─── SSO / OIDC (Settings > SSO) ─── */
export function SSOTab() {
  const [connections, setConnections] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [showModal, setShowModal] = useState(false);
  const [editId, setEditId] = useState(null);
  const [form, setForm] = useState(EMPTY_FORM);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState('');
  const [deleteTarget, setDeleteTarget] = useState(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');
  const [status, setStatus] = useState('');
  // Bumped after any change that can make the org ready (or not) for
  // Enforce SSO, so that section rechecks without a page reload.
  const [setupVersion, setSetupVersion] = useState(0);
  const setupChanged = () => setSetupVersion(v => v + 1);

  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      const data = await get('/org/oidc-connections');
      setConnections(Array.isArray(data) ? data : []);
    } catch (err) {
      setLoadError(err);
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  function openAdd() {
    setEditId(null);
    setForm(EMPTY_FORM);
    setSaveError('');
    setShowModal(true);
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
    setSaveError('');
    setShowModal(true);
  }

  function closeModal() {
    if (saving) return;
    setShowModal(false);
  }

  async function save() {
    if (saving) return;
    setSaving(true);
    setSaveError('');
    const body = {
      ...form,
      scopes: form.scopes.split(',').map(s => s.trim()).filter(Boolean),
    };
    try {
      if (editId) await patch(`/org/oidc-connections/${encodeURIComponent(editId)}`, body);
      else await post('/org/oidc-connections', body);
      setShowModal(false);
      setStatus(editId ? `${form.provider} is saved.` : `${form.provider} is added. Add an email domain below so people can find it.`);
      setEditId(null);
      setForm(EMPTY_FORM);
      await load();
      setupChanged();
    } catch (err) {
      setSaveError(errorText(err, 'Could not save the connection. Check the fields and try again.'));
    }
    setSaving(false);
  }

  function openDelete(c) {
    setDeleteTarget(c);
    setDeleteError('');
  }

  function closeDelete() {
    if (deleting) return;
    setDeleteTarget(null);
  }

  async function confirmDelete() {
    if (!deleteTarget || deleting) return;
    setDeleting(true);
    setDeleteError('');
    try {
      await del(`/org/oidc-connections/${encodeURIComponent(deleteTarget.id)}`);
      setStatus(`${deleteTarget.provider} is deleted.`);
      setDeleteTarget(null);
      await load();
      setupChanged();
      const heading = document.getElementById('sso-connections-heading');
      if (heading) heading.focus();
    } catch (err) {
      setDeleteError(errorText(err, 'Could not delete the connection. Try again in a moment.'));
    }
    setDeleting(false);
  }

  const set = (key) => (e) => setForm({ ...form, [key]: e.target.type === 'checkbox' ? e.target.checked : e.target.value });
  const isOIDCMode = !!form.issuer_url;

  if (loading) return <LoadingPage />;
  if (loadError) return <ErrorMessage error={loadError} onRetry={load} />;

  return (
    <div class="sso-settings">
      <div class="section-header">
        <h3 id="sso-connections-heading" tabIndex={-1}>SSO connections</h3>
        <button type="button" class="btn btn-sm btn-primary" onClick={openAdd}>
          Add connection
        </button>
      </div>
      <p class="text-muted sso-intro">
        People start SSO sign-in by entering their work email on the sign-in page. Add your
        company email domain to a connection and verify it with a DNS record so their sign-in
        reaches your identity provider.
      </p>

      <p class="sso-sr-only" role="status" aria-live="polite">{status}</p>
      {status && <p class="sso-status" aria-hidden="true">{status}</p>}

      {connections.length === 0 ? (
        <div class="sso-empty">
          <p><strong>No SSO connections yet.</strong></p>
          <p class="text-muted">
            Add one to let your team sign in with your identity provider (Okta, Google
            Workspace, Microsoft Entra ID or any OIDC provider).
          </p>
        </div>
      ) : (
        <ul class="sso-conn-list">
          {connections.map(c => (
            <li key={c.id} class="sso-conn">
              <div class="sso-conn-head">
                <div class="sso-conn-title">
                  <h4>{c.provider}</h4>
                  <span class="label-tag">{c.issuer_url ? 'OIDC' : 'OAuth2'}</span>
                  {!c.enabled && <span class="label-tag">Disabled</span>}
                </div>
                <div class="btn-group">
                  <button type="button" class="btn btn-xs" onClick={() => edit(c)}
                          aria-label={`Edit ${c.provider}`}>Edit</button>
                  <button type="button" class="btn btn-xs btn-danger" onClick={() => openDelete(c)}
                          aria-label={`Delete ${c.provider}`}>Delete</button>
                </div>
              </div>
              <ConnectionDomains connection={c} onChange={setupChanged} />
            </li>
          ))}
        </ul>
      )}

      {connections.length > 0 && <SSOEnforcement setupVersion={setupVersion} />}

      <Modal open={showModal} onClose={closeModal} title={editId ? 'Edit SSO connection' : 'Add SSO connection'}>
        <form onSubmit={e => { e.preventDefault(); save(); }}>
          {saveError && <p class="form-error" role="alert">{saveError}</p>}
          <div class="form-group">
            <label for="sso-provider">Provider name</label>
            <input id="sso-provider" type="text" value={form.provider} onInput={set('provider')}
                   required maxLength={100} placeholder="e.g. Okta, Google, Microsoft" />
          </div>
          <div class="form-group">
            <label for="sso-client-id">Client ID</label>
            <input id="sso-client-id" type="text" value={form.client_id} onInput={set('client_id')}
                   required autocomplete="off" spellcheck={false} />
          </div>
          <div class="form-group">
            <label for="sso-client-secret">Client secret</label>
            <input id="sso-client-secret" type="password" value={form.client_secret}
                   onInput={set('client_secret')} autocomplete="new-password"
                   aria-describedby={editId ? 'sso-client-secret-hint' : undefined}
                   required={!editId} />
            {editId && <p id="sso-client-secret-hint" class="text-muted text-xs">Leave blank to keep the current secret.</p>}
          </div>
          <div class="form-group">
            <label for="sso-issuer">Issuer URL (OIDC discovery)</label>
            <input id="sso-issuer" type="url" value={form.issuer_url} onInput={set('issuer_url')}
                   aria-describedby="sso-issuer-hint" placeholder="https://accounts.google.com" />
            <p id="sso-issuer-hint" class="text-muted text-xs">
              Set this for OIDC providers. Leave it blank for plain OAuth2 and fill in the URLs below.
            </p>
          </div>
          {!isOIDCMode && (
            <>
              <div class="form-group">
                <label for="sso-authorize-url">Authorize URL</label>
                <input id="sso-authorize-url" type="url" value={form.authorize_url} onInput={set('authorize_url')}
                       placeholder="https://github.com/login/oauth/authorize" required />
              </div>
              <div class="form-group">
                <label for="sso-token-url">Token URL</label>
                <input id="sso-token-url" type="url" value={form.token_url} onInput={set('token_url')}
                       placeholder="https://github.com/login/oauth/access_token" required />
              </div>
              <div class="form-group">
                <label for="sso-userinfo-url">Userinfo URL</label>
                <input id="sso-userinfo-url" type="url" value={form.userinfo_url} onInput={set('userinfo_url')}
                       placeholder="https://api.github.com/user" required />
              </div>
            </>
          )}
          <div class="form-group">
            <label for="sso-scopes">Scopes</label>
            <input id="sso-scopes" type="text" value={form.scopes} onInput={set('scopes')}
                   aria-describedby="sso-scopes-hint" placeholder="openid,email,profile" spellcheck={false} />
            <p id="sso-scopes-hint" class="text-muted text-xs">Comma separated.</p>
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.auto_provision} onChange={set('auto_provision')} />
              Create accounts for new people on their first SSO sign-in
            </label>
          </div>
          <div class="form-group">
            <label for="sso-default-role">Role for new accounts</label>
            <select id="sso-default-role" value={form.default_role} onChange={set('default_role')}>
              <option value="viewer">Viewer</option>
              <option value="member">Member</option>
              <option value="admin">Admin</option>
            </select>
          </div>
          <div class="form-group form-check">
            <label>
              <input type="checkbox" checked={form.enabled} onChange={set('enabled')} />
              Enabled
            </label>
          </div>
          <div class="btn-group">
            <button type="submit" class="btn btn-primary" disabled={saving} aria-busy={saving ? 'true' : 'false'}>
              {saving ? 'Saving...' : 'Save'}
            </button>
            <button type="button" class="btn btn-outline" onClick={closeModal} disabled={saving}>Cancel</button>
          </div>
        </form>
      </Modal>

      <Modal open={!!deleteTarget} onClose={closeDelete} title={`Delete ${deleteTarget?.provider || 'connection'}?`}>
        <p class="sso-modal-intro">
          People can no longer sign in with {deleteTarget?.provider}. Its email domains are
          removed too, so you would need to verify them again for a new connection.
          Accounts are kept. People who only used SSO can set a password with Forgot
          password on the sign-in page.
        </p>
        {deleteError && <p class="form-error" role="alert">{deleteError}</p>}
        <div class="btn-group">
          <button type="button" class="btn btn-danger" onClick={confirmDelete}
                  disabled={deleting} aria-busy={deleting ? 'true' : 'false'}>
            {deleting ? 'Deleting...' : 'Delete connection'}
          </button>
          <button type="button" class="btn btn-outline" onClick={closeDelete} disabled={deleting}>Cancel</button>
        </div>
      </Modal>
    </div>
  );
}

// CopyValue shows a DNS value in a read-only field with a copy button. The
// field stays selectable so copying by hand works without clipboard access.
function CopyValue({ id, label, value, onCopied }) {
  const inputRef = useRef(null);
  const timer = useRef(null);
  // '' | 'ok' | 'fail': shown on screen as well as announced.
  const [result, setResult] = useState('');
  const hintId = `${id}-copy-hint`;

  useEffect(() => () => clearTimeout(timer.current), []);

  async function copy() {
    clearTimeout(timer.current);
    try {
      if (!navigator.clipboard) throw new Error('clipboard unavailable');
      await navigator.clipboard.writeText(value);
      setResult('ok');
      onCopied(`${label} copied.`);
      timer.current = setTimeout(() => setResult(''), 2500);
    } catch (_) {
      if (inputRef.current) {
        inputRef.current.focus();
        inputRef.current.select();
      }
      setResult('fail');
      onCopied(`Could not copy. The ${label.toLowerCase()} is selected; press Ctrl+C or Cmd+C.`);
    }
  }

  return (
    <div class="sso-copy">
      <label for={id} class="sso-copy-label">{label}</label>
      <div class="sso-copy-row">
        <input id={id} ref={inputRef} type="text" readOnly value={value} spellcheck={false}
               class="sso-copy-input" onFocus={e => e.target.select()}
               aria-describedby={result === 'fail' ? hintId : undefined} />
        <button type="button" class="btn btn-xs btn-outline sso-copy-btn" onClick={copy}
                aria-label={`Copy ${label.toLowerCase()}`}>
          {result === 'ok' ? 'Copied' : 'Copy'}
        </button>
      </div>
      {result === 'fail' && (
        <p id={hintId} class="text-xs sso-copy-hint">
          Could not copy automatically. The value is selected; press Ctrl+C or Cmd+C.
        </p>
      )}
    </div>
  );
}

// ConnectionDomains manages the email domains that route "Sign in with SSO"
// to one connection: add, publish a TXT record, verify, remove.
function ConnectionDomains({ connection, onChange }) {
  const base = `/org/oidc-connections/${encodeURIComponent(connection.id)}/domains`;
  const headingId = `sso-domains-${connection.id}`;
  const inputId = `sso-domain-input-${connection.id}`;
  const errorId = `sso-domain-error-${connection.id}`;

  const [domains, setDomains] = useState([]);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const [newDomain, setNewDomain] = useState('');
  const [adding, setAdding] = useState(false);
  const [addError, setAddError] = useState('');
  const [busyId, setBusyId] = useState(null);
  const [rowErrors, setRowErrors] = useState({});
  const [status, setStatus] = useState('');
  const [removeTarget, setRemoveTarget] = useState(null);
  const [removeError, setRemoveError] = useState('');

  async function load() {
    setLoading(true);
    setLoadError('');
    try {
      const data = await get(base);
      setDomains(Array.isArray(data) ? data : []);
    } catch (err) {
      setLoadError(errorText(err, 'Could not load the email domains.'));
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, [connection.id]);

  function setRowError(id, msg) {
    setRowErrors(prev => ({ ...prev, [id]: msg }));
  }

  async function add(e) {
    e.preventDefault();
    if (adding) return;
    // Accept a pasted email or URL and keep only the host part; the server
    // validates and normalizes the result.
    let value = newDomain.trim();
    if (value.includes('@')) value = value.slice(value.lastIndexOf('@') + 1);
    value = value.replace(/^[a-z]+:\/\//i, '').split('/')[0];
    if (!value) {
      setAddError('Enter a domain such as example.com.');
      return;
    }
    setAdding(true);
    setAddError('');
    try {
      const d = await post(base, { domain: value });
      setDomains(prev => [...prev, d]);
      setNewDomain('');
      setStatus(`${d.domain} is added. Publish the TXT record, then choose Check DNS.`);
    } catch (err) {
      setAddError(errorText(err, 'Could not add the domain. Try again in a moment.'));
      const input = document.getElementById(inputId);
      if (input) input.focus();
    }
    setAdding(false);
  }

  async function verify(d) {
    if (busyId) return;
    setBusyId(d.id);
    setRowError(d.id, '');
    setStatus(`Checking DNS for ${d.domain}...`);
    try {
      const updated = await post(`${base}/${encodeURIComponent(d.id)}/verify`, {});
      setDomains(prev => prev.map(x => (x.id === d.id ? updated : x)));
      if (onChange) onChange();
      setStatus(`${d.domain} is verified. People with an @${d.domain} email can now sign in with SSO.`);
    } catch (err) {
      // The row error has role=alert, so do not also put it in the status region.
      setRowError(d.id, errorText(err, 'Could not check DNS. Try again in a moment.'));
      setStatus('');
    }
    setBusyId(null);
  }

  function openRemove(d) {
    setRemoveTarget(d);
    setRemoveError('');
  }

  function closeRemove() {
    if (busyId) return;
    setRemoveTarget(null);
  }

  async function confirmRemove() {
    const d = removeTarget;
    if (!d || busyId) return;
    setBusyId(d.id);
    setRemoveError('');
    try {
      await del(`${base}/${encodeURIComponent(d.id)}`);
      setDomains(prev => prev.filter(x => x.id !== d.id));
      setRemoveTarget(null);
      setStatus(`${d.domain} is removed.`);
      if (onChange) onChange();
      const heading = document.getElementById(headingId);
      if (heading) heading.focus();
    } catch (err) {
      if (err?.status === 404) {
        setDomains(prev => prev.filter(x => x.id !== d.id));
        setRemoveTarget(null);
        setStatus(`${d.domain} was already removed.`);
      } else {
        setRemoveError(errorText(err, 'Could not remove the domain. Try again in a moment.'));
      }
    }
    setBusyId(null);
  }

  return (
    <section class="sso-domains" aria-labelledby={headingId}>
      <h5 id={headingId} class="sso-domains-title" tabIndex={-1}>Email domains</h5>
      {!connection.enabled && (
        <p class="sso-domains-note">
          This connection is disabled, so its domains do not start SSO sign-in. Edit it and
          turn on Enabled when you are ready.
        </p>
      )}

      {connection.enabled && !loading && !loadError && domains.length > 0 && !domains.some(d => d.verified) && (
        <p class="sso-domains-note">
          No verified domain yet, so nobody can reach {connection.provider} from the sign-in
          page. Publish the TXT record below and choose Check DNS.
        </p>
      )}

      <p class="sso-sr-only" role="status" aria-live="polite">{status}</p>

      {loading ? (
        <p class="text-muted" role="status">Loading domains...</p>
      ) : loadError ? (
        <div>
          <p class="form-error" role="alert">{loadError}</p>
          <button type="button" class="btn btn-xs btn-outline" onClick={load}>Try again</button>
        </div>
      ) : domains.length === 0 ? (
        <p class="text-muted sso-domains-empty">
          No domains yet. Add your company email domain (for example, example.com) so people
          can sign in with their work email.
        </p>
      ) : (
        <ul class="sso-domain-list">
          {domains.map(d => {
            const busy = busyId === d.id;
            const rowErr = rowErrors[d.id];
            return (
              <li key={d.id} class="sso-domain">
                <div class="sso-domain-head">
                  <span class="sso-domain-name">{d.domain}</span>
                  {d.verified ? (
                    <span class="sso-badge sso-badge-ok">Verified</span>
                  ) : (
                    <span class="sso-badge sso-badge-pending">Not verified</span>
                  )}
                  <span class="sso-domain-actions">
                    {!d.verified && (
                      <button type="button" class="btn btn-xs btn-primary" onClick={() => verify(d)}
                              disabled={!!busyId} aria-busy={busy ? 'true' : 'false'}
                              aria-label={`Check DNS for ${d.domain}`}>
                        {busy ? 'Checking...' : 'Check DNS'}
                      </button>
                    )}
                    <button type="button" class="btn btn-xs btn-outline" onClick={() => openRemove(d)}
                            disabled={!!busyId} aria-label={`Remove ${d.domain}`}>
                      Remove
                    </button>
                  </span>
                </div>
                {d.verified ? (
                  <p class="text-muted text-xs sso-domain-meta">
                    Verified {d.verified_at ? formatTime(d.verified_at) : ''}. Sign-ins from
                    @{d.domain} and its subdomains go to {connection.provider}. You can delete
                    the TXT record now.
                  </p>
                ) : (
                  <div class="sso-domain-steps">
                    <p class="text-xs">
                      Add this TXT record at your DNS provider, then choose Check DNS. DNS
                      changes can take a few minutes to appear.
                    </p>
                    <CopyValue id={`sso-txt-name-${d.id}`} label="Record name" value={d.txt_name} onCopied={setStatus} />
                    <CopyValue id={`sso-txt-value-${d.id}`} label="Record value" value={d.verification_token} onCopied={setStatus} />
                  </div>
                )}
                {rowErr && <p class="form-error sso-row-error" role="alert">{rowErr}</p>}
              </li>
            );
          })}
        </ul>
      )}

      {!loading && !loadError && (
        <form class="sso-domain-add" onSubmit={add} noValidate>
          <label for={inputId}>Add a domain</label>
          <div class="sso-domain-add-row">
            <input id={inputId} type="text" inputMode="url" autocomplete="off" autocapitalize="none"
                   spellcheck={false} maxLength={253} placeholder="example.com" value={newDomain}
                   aria-invalid={addError ? 'true' : 'false'}
                   aria-describedby={addError ? errorId : undefined}
                   onInput={e => { setNewDomain(e.target.value); if (addError) setAddError(''); }} />
            <button type="submit" class="btn btn-sm btn-outline" disabled={adding}
                    aria-busy={adding ? 'true' : 'false'}>
              {adding ? 'Adding...' : 'Add domain'}
            </button>
          </div>
          {addError && <p id={errorId} class="form-error sso-row-error" role="alert">{addError}</p>}
        </form>
      )}

      <Modal open={!!removeTarget} onClose={closeRemove} title={`Remove ${removeTarget?.domain || 'domain'}?`}>
        <p class="sso-modal-intro">
          {removeTarget?.verified
            ? `People with an @${removeTarget?.domain} email can no longer start SSO sign-in by entering their email. To add it back you need to verify it again.`
            : 'The pending verification is cancelled. You can add the domain again later.'}
        </p>
        {removeError && <p class="form-error" role="alert">{removeError}</p>}
        <div class="btn-group">
          <button type="button" class="btn btn-danger" onClick={confirmRemove}
                  disabled={!!busyId} aria-busy={busyId ? 'true' : 'false'}>
            {busyId ? 'Removing...' : 'Remove domain'}
          </button>
          <button type="button" class="btn btn-outline" onClick={closeRemove} disabled={!!busyId}>Cancel</button>
        </div>
      </Modal>
    </section>
  );
}
