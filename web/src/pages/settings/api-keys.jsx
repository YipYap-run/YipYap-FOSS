import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, del } from '../../api/client';
import { Modal, LoadingPage, ErrorMessage, formatTime, useToast, ToastContainer } from '../../components/ui';
import {
  PRESET_READ_ONLY, PRESET_FULL_ACCESS, MODE_CUSTOM, EXPIRY_CHOICES,
  defaultExpiryChoice, toggleGrant, setAllGrants, buildScopes, expiryPayload, expiryPreview,
  customDateBounds, expiryStatus, scopeSummary, creatorLabel, validateCreate, formatDate,
  keyLabel, revokeConsequence, shouldConfirmDiscard,
} from './api-keys-view.mjs';

function secretOf(created) {
  return typeof created === 'string' ? created : created?.key || '';
}

const PAGE_SIZE = 100;
const MAX_PAGES = 20;

// Keys are listed 100 at a time by the server; fetch every page so an org
// with many keys still sees all of them.
async function loadAllKeys() {
  const all = [];
  for (let page = 0; page < MAX_PAGES; page++) {
    const data = await get(`/org/api-keys?limit=${PAGE_SIZE}&offset=${page * PAGE_SIZE}`);
    const batch = Array.isArray(data) ? data : (data?.api_keys || []);
    all.push(...batch);
    if (batch.length < PAGE_SIZE) break;
  }
  return all;
}

const TONE_BADGE = { expired: 'badge-down', soon: 'badge-warning', ok: 'badge-muted', never: 'badge-info' };

/* ─── API Keys ─── */
export function APIKeysTab() {
  const [keys, setKeys] = useState([]);
  const [vocab, setVocab] = useState(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [showCreate, setShowCreate] = useState(false);
  const [createdKey, setCreatedKey] = useState(null);
  const [keyCopied, setKeyCopied] = useState(false);
  const [confirmDiscard, setConfirmDiscard] = useState(false);
  const [revokeTarget, setRevokeTarget] = useState(null);
  const [revoking, setRevoking] = useState(false);
  const { toasts, toast } = useToast();

  // A failed load shows an error with Retry. Only a successful empty
  // response shows the empty state.
  async function load() {
    setLoading(true);
    setLoadError(null);
    try {
      const [list, scopes] = await Promise.all([loadAllKeys(), vocab ? Promise.resolve(vocab) : get('/org/api-keys/scopes')]);
      setKeys(list);
      setVocab(scopes);
    } catch (err) {
      setLoadError(err);
    }
    setLoading(false);
  }
  useEffect(() => { load(); }, []);

  async function reloadKeys() {
    try { setKeys(await loadAllKeys()); } catch (_) { /* the list stays as it was */ }
  }

  // The new key is shown once. Closing the dialog (Escape, X, overlay or
  // Done) before it was copied asks first instead of dropping it.
  function closeCreate(force = false) {
    if (createdKey && shouldConfirmDiscard({ secret: secretOf(createdKey), copied: keyCopied, force })) {
      setConfirmDiscard(true);
      return;
    }
    setShowCreate(false);
    setConfirmDiscard(false);
  }

  function openCreate() {
    setCreatedKey(null);
    setKeyCopied(false);
    setConfirmDiscard(false);
    setShowCreate(true);
  }

  async function revoke() {
    if (!revokeTarget) return;
    setRevoking(true);
    try {
      await del(`/org/api-keys/${encodeURIComponent(revokeTarget.id)}`);
      toast(`Revoked ${revokeTarget.name}`);
      setRevokeTarget(null);
      reloadKeys();
    } catch (err) {
      if (!err.readonly) toast(err.message || 'Could not revoke the API key', 'error');
    } finally {
      setRevoking(false);
    }
  }

  if (loading) return <LoadingPage />;

  const resources = vocab?.resources || [];
  const legacyCount = keys.filter(k => k.unrestricted).length;

  return (
    <div class="api-keys-tab">
      <ToastContainer toasts={toasts} />
      <div class="section-header">
        <h3>API Keys</h3>
        {!loadError && (
          <button class="btn btn-sm btn-primary" onClick={openCreate}>
            Create key
          </button>
        )}
      </div>
      <p class="text-muted api-keys-intro">
        Keys belong to the organization, not to the person who made them. They keep working when that person
        leaves unless you revoke them.
      </p>
      {loadError ? (
        <ErrorMessage error={{ message: `Could not load API keys: ${loadError.message || loadError}` }} onRetry={load} />
      ) : (
        <>
          {legacyCount > 0 && (
            <p class="api-keys-legacy-note" role="note">
              {legacyCount === 1 ? '1 key was' : `${legacyCount} keys were`} created before scopes existed and
              {legacyCount === 1 ? ' has' : ' have'} full access. Create a scoped replacement, then revoke the old key.
            </p>
          )}
          {keys.length === 0 ? (
            <div class="api-keys-empty">
              <p><strong>No API keys yet.</strong></p>
              <p class="text-muted">Create a key to call the YipYap API from scripts, CI or other tools.</p>
            </div>
          ) : (
            <ul class="api-key-list" aria-label="API keys">
              {keys.map(k => <APIKeyRow key={k.id} apiKey={k} resources={resources} onRevoke={() => setRevokeTarget(k)} />)}
            </ul>
          )}
        </>
      )}

      <Modal open={showCreate} onClose={() => closeCreate()} title={createdKey ? 'Copy your new key' : 'Create API key'}>
        {createdKey ? (
          <CreatedKey
            created={createdKey}
            resources={resources}
            confirmDiscard={confirmDiscard}
            onCopied={() => { setKeyCopied(true); setConfirmDiscard(false); }}
            onDone={() => closeCreate()}
            onKeep={() => setConfirmDiscard(false)}
            onDiscard={() => closeCreate(true)}
          />
        ) : (
          <CreateKeyForm
            vocab={vocab}
            onCreated={res => { setKeyCopied(false); setCreatedKey(res); reloadKeys(); }}
            onCancel={() => closeCreate()}
          />
        )}
      </Modal>

      <Modal open={!!revokeTarget} onClose={() => { if (!revoking) setRevokeTarget(null); }} title="Revoke API key">
        {revokeTarget && (
          <div>
            <p>
              Revoke <strong>{revokeTarget.name || 'Unnamed key'}</strong>
              {revokeTarget.prefix && <> (<code>{revokeTarget.prefix}...</code>)</>}? {revokeConsequence(revokeTarget)}
            </p>
            <div class="btn-group modal-actions">
              <button type="button" class="btn btn-danger" onClick={revoke} disabled={revoking} aria-busy={revoking ? 'true' : undefined}>
                {revoking ? 'Revoking...' : 'Revoke key'}
              </button>
              <button type="button" class="btn" data-autofocus onClick={() => setRevokeTarget(null)} disabled={revoking}>Cancel</button>
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}

function APIKeyRow({ apiKey: k, resources, onRevoke }) {
  const status = expiryStatus(k);
  const headingId = `api-key-${k.id}`;
  return (
    <li class={`api-key-item${status.tone === 'expired' ? ' is-expired' : ''}`} aria-labelledby={headingId}>
      <div class="api-key-main">
        <div class="api-key-title">
          <strong id={headingId}>{k.name}</strong>
          {k.prefix && <code class="api-key-prefix" title="Key prefix">{k.prefix}...</code>}
          <span class={`badge badge-sm ${TONE_BADGE[status.tone]}`}>{status.label}</span>
          {k.unrestricted && <span class="badge badge-sm badge-warning">Unscoped</span>}
        </div>
        <dl class="api-key-facts">
          <div><dt>Access</dt><dd>{scopeSummary(k, resources)}</dd></div>
          <div>
            <dt>Created by</dt>
            <dd>
              {creatorLabel(k)}
              {k.creator_removed && <span class="text-muted"> (no longer in this org)</span>}
            </dd>
          </div>
          <div><dt>Created</dt><dd>{formatDate(k.created_at) || '-'}</dd></div>
          <div><dt>Last used</dt><dd>{k.last_used_at ? formatTime(k.last_used_at) : 'Never'}</dd></div>
        </dl>
      </div>
      <button type="button" class="btn btn-xs btn-danger" onClick={onRevoke} aria-label={`Revoke API key ${keyLabel(k)}`}>Revoke</button>
    </li>
  );
}

function CreateKeyForm({ vocab, onCreated, onCancel }) {
  const resources = vocab?.resources || [];
  const presets = vocab?.presets || [];
  const maxDays = vocab?.max_expiry_days || 730;
  const [name, setName] = useState('');
  const [mode, setMode] = useState(PRESET_READ_ONLY);
  const [grants, setGrants] = useState({});
  const [expiry, setExpiry] = useState(defaultExpiryChoice(vocab?.default_expiry_days));
  const [customDate, setCustomDate] = useState('');
  const [errors, setErrors] = useState({});
  const [serverError, setServerError] = useState('');
  const [creating, setCreating] = useState(false);
  const errorRef = useRef(null);
  const bounds = customDateBounds(new Date(), maxDays);

  useEffect(() => {
    if (serverError) errorRef.current?.focus();
  }, [serverError]);

  async function submit(e) {
    e.preventDefault();
    if (creating) return;
    const found = validateCreate({ name, mode, grants, resources, expiry, customDate, maxDays });
    setErrors(found);
    setServerError('');
    if (Object.keys(found).length > 0) {
      const first = found.name ? 'api-key-name' : found.scopes ? 'api-key-access-legend' : 'api-key-expiry';
      document.getElementById(first)?.focus();
      return;
    }
    const body = {
      name: name.trim(),
      scopes: buildScopes(mode, grants, resources),
      expires_at: expiryPayload(expiry, customDate, new Date(), maxDays).value,
    };
    setCreating(true);
    try {
      const res = await post('/org/api-keys', body);
      onCreated(res);
    } catch (err) {
      // Keep the form and its values so the user can fix and retry.
      if (!err.readonly) setServerError(err.message || 'Could not create the API key.');
    } finally {
      setCreating(false);
    }
  }

  const preview = expiryPreview(expiry, customDate);

  return (
    <form onSubmit={submit} noValidate class="api-key-form">
      {serverError && (
        <p class="form-error" role="alert" tabIndex={-1} ref={errorRef}>{serverError}</p>
      )}
      <div class="form-group">
        <label for="api-key-name">Name</label>
        <input id="api-key-name" type="text" value={name} maxLength={100} autoComplete="off"
               onInput={e => setName(e.target.value)} placeholder="e.g. CI pipeline"
               aria-invalid={errors.name ? 'true' : undefined}
               aria-describedby={errors.name ? 'api-key-name-error' : undefined} />
        {errors.name && <p id="api-key-name-error" class="field-error">{errors.name}</p>}
      </div>

      <fieldset class="form-group api-key-fieldset" aria-describedby={errors.scopes ? 'api-key-scopes-error' : undefined}>
        <legend id="api-key-access-legend" tabIndex={-1}>Access</legend>
        {presets.map(p => (
          <label key={p.key} class="api-key-choice">
            <input type="radio" name="api-key-mode" value={p.key} checked={mode === p.key} onChange={() => setMode(p.key)} />
            <span><strong>{p.label}</strong><span class="api-key-choice-help">{p.description}</span></span>
          </label>
        ))}
        <label class="api-key-choice">
          <input type="radio" name="api-key-mode" value={MODE_CUSTOM} checked={mode === MODE_CUSTOM} onChange={() => setMode(MODE_CUSTOM)}
                 aria-controls="api-key-resources" />
          <span><strong>Advanced</strong><span class="api-key-choice-help">Choose read or write for each resource. Write includes read.</span></span>
        </label>
        {errors.scopes && <p id="api-key-scopes-error" class="field-error">{errors.scopes}</p>}
      </fieldset>

      {mode === MODE_CUSTOM && (
        <fieldset id="api-key-resources" class="form-group api-key-fieldset api-key-resources">
          <legend>Resources</legend>
          <div class="api-key-bulk">
            <button type="button" class="btn btn-xs" onClick={() => setGrants(setAllGrants(resources, 'read'))}>Read all</button>
            <button type="button" class="btn btn-xs" onClick={() => setGrants(setAllGrants(resources, 'write'))}>Read and write all</button>
            <button type="button" class="btn btn-xs" onClick={() => setGrants({})}>Clear</button>
          </div>
          <ul class="api-key-resource-list">
            {resources.map(r => {
              const level = grants[r.key];
              const canWrite = (r.actions || []).includes('write');
              const descId = `api-key-res-${r.key}-desc`;
              return (
                <li key={r.key} class="api-key-resource">
                  <div class="api-key-resource-text">
                    <span class="api-key-resource-name" id={`api-key-res-${r.key}`}>{r.label}</span>
                    <span class="api-key-choice-help" id={descId}>{r.description}</span>
                  </div>
                  <div class="api-key-resource-actions" role="group" aria-labelledby={`api-key-res-${r.key}`}>
                    <label>
                      <input type="checkbox" checked={!!level} aria-describedby={descId}
                             onChange={e => setGrants(toggleGrant(grants, r.key, 'read', e.target.checked))} />
                      Read
                    </label>
                    {canWrite ? (
                      <label>
                        <input type="checkbox" checked={level === 'write'}
                               onChange={e => setGrants(toggleGrant(grants, r.key, 'write', e.target.checked))} />
                        Write
                      </label>
                    ) : (
                      <span class="api-key-readonly-tag">Read only</span>
                    )}
                  </div>
                </li>
              );
            })}
          </ul>
        </fieldset>
      )}

      <div class="form-group">
        <label for="api-key-expiry">Expires</label>
        <select id="api-key-expiry" value={expiry} onChange={e => setExpiry(e.target.value)}
                aria-describedby="api-key-expiry-help"
                aria-invalid={errors.expiry && expiry !== 'date' ? 'true' : undefined}>
          {EXPIRY_CHOICES.map(c => <option key={c.value} value={c.value}>{c.label}</option>)}
        </select>
        {expiry === 'date' && (
          <div class="api-key-date">
            <label for="api-key-expiry-date">Valid through</label>
            <input id="api-key-expiry-date" type="date" value={customDate} min={bounds.min} max={bounds.max}
                   onInput={e => setCustomDate(e.target.value)}
                   aria-invalid={errors.expiry ? 'true' : undefined}
                   aria-describedby={errors.expiry ? 'api-key-expiry-error' : 'api-key-expiry-help'} />
          </div>
        )}
        {errors.expiry && <p id="api-key-expiry-error" class="field-error">{errors.expiry}</p>}
        <p id="api-key-expiry-help" class="form-help" aria-live="polite">{preview}</p>
        {expiry === 'never' && (
          <p class="api-key-warning">
            A key with no expiry stays valid if it leaks. Use it only for systems where you rotate keys yourself.
          </p>
        )}
      </div>

      <div class="btn-group modal-actions">
        <button type="submit" class="btn btn-primary" disabled={creating} aria-busy={creating ? 'true' : undefined}>
          {creating ? 'Creating...' : 'Create key'}
        </button>
        <button type="button" class="btn" onClick={onCancel} disabled={creating}>Cancel</button>
      </div>
    </form>
  );
}

function CreatedKey({ created, resources, confirmDiscard, onCopied, onDone, onKeep, onDiscard }) {
  const [copied, setCopied] = useState('');
  const [justCopied, setJustCopied] = useState(false);
  const resetTimer = useRef(null);
  useEffect(() => () => clearTimeout(resetTimer.current), []);
  const codeRef = useRef(null);
  const copyRef = useRef(null);
  const keepRef = useRef(null);
  // The form that had focus is gone, so focus starts on Copy. The discard
  // check replaces the buttons, so focus moves to its safe choice and back
  // to Copy when it goes away.
  useEffect(() => {
    if (confirmDiscard) keepRef.current?.focus();
    else copyRef.current?.focus();
  }, [confirmDiscard]);
  const secret = secretOf(created);

  function markCopied(message) {
    setCopied(message);
    setJustCopied(true);
    clearTimeout(resetTimer.current);
    resetTimer.current = setTimeout(() => setJustCopied(false), 2000);
    onCopied();
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      markCopied('Copied to clipboard.');
    } catch (_) {
      // Clipboard access can be blocked; select the text so Ctrl+C works.
      const sel = window.getSelection();
      const range = document.createRange();
      if (codeRef.current) {
        range.selectNodeContents(codeRef.current);
        sel.removeAllRanges();
        sel.addRange(range);
      }
      setCopied('Could not copy automatically. The key is selected, press Ctrl+C or Cmd+C.');
    }
  }

  const status = expiryStatus(created);
  return (
    <div>
      <p><strong>Copy this key now.</strong> It is not shown again. If you lose it, revoke it and create a new one.</p>
      <code class="api-key-display" ref={codeRef} aria-label="New API key"
            onCopy={() => markCopied('Copied.')}>{secret}</code>
      <dl class="api-key-facts">
        <div><dt>Name</dt><dd>{created?.name}</dd></div>
        <div><dt>Access</dt><dd>{scopeSummary(created, resources)}</dd></div>
        <div><dt>Expiry</dt><dd>{status.label}</dd></div>
      </dl>
      <p class="form-help" role="status" aria-live="polite">{copied}</p>
      {confirmDiscard ? (
        <div class="api-key-discard" role="alert">
          <p><strong>You have not copied this key.</strong> Once you close this dialog it cannot be shown again.</p>
          <div class="btn-group modal-actions">
            <button type="button" class="btn btn-primary" ref={keepRef} onClick={onKeep}>Go back</button>
            <button type="button" class="btn btn-danger" onClick={onDiscard}>Close without copying</button>
          </div>
        </div>
      ) : (
        <div class="btn-group modal-actions">
          <button type="button" class="btn btn-primary" ref={copyRef} onClick={copy}>{justCopied ? 'Copied' : 'Copy key'}</button>
          <button type="button" class="btn" onClick={onDone}>Done</button>
        </div>
      )}
    </div>
  );
}
