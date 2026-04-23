import { useState, useEffect } from 'preact/hooks';
import QRCode from 'qrcode';
import { get, post, api } from '../../api/client';
import { Modal } from '../../components/ui';
import { currentUser } from '../../state/auth';
import { prepareCreationOptions, serializeCredential } from '../../utils/webauthn';

/* ─── MFATab ─── */

export function MFATab() {
  const user = currentUser.value;

  // TOTP state
  const [totpSetup, setTotpSetup] = useState(null);      // { secret, uri, backup_codes }
  const [totpLoading, setTotpLoading] = useState(false);
  const [totpCode, setTotpCode] = useState('');
  const [totpVerifying, setTotpVerifying] = useState(false);
  const [totpEnabled, setTotpEnabled] = useState(user?.mfa_app_enabled || false);
  const [totpError, setTotpError] = useState('');
  const [qrDataUrl, setQrDataUrl] = useState('');
  const [showDisableTotp, setShowDisableTotp] = useState(false);
  const [disableTotpPassword, setDisableTotpPassword] = useState('');
  const [disableTotpError, setDisableTotpError] = useState('');
  const [disableTotpLoading, setDisableTotpLoading] = useState(false);

  // WebAuthn state
  const [credentials, setCredentials] = useState([]);
  const [credsLoading, setCredsLoading] = useState(true);
  const [registerError, setRegisterError] = useState('');
  const [deleteCredId, setDeleteCredId] = useState(null);
  const [deleteCredPassword, setDeleteCredPassword] = useState('');
  const [deleteCredError, setDeleteCredError] = useState('');
  const [deleteCredLoading, setDeleteCredLoading] = useState(false);

  // Shared password-prompt for register flows (totp setup, add passkey, add security key)
  const [pwPromptKind, setPwPromptKind] = useState(null); // 'totp' | 'passkey' | 'key'
  const [pwPromptValue, setPwPromptValue] = useState('');
  const [pwPromptError, setPwPromptError] = useState('');
  const [pwPromptBusy, setPwPromptBusy] = useState(false);

  useEffect(() => {
    loadCredentials();
  }, []);

  async function loadCredentials() {
    setCredsLoading(true);
    try {
      const data = await get('/auth/mfa/webauthn');
      setCredentials(data || []);
    } catch (_) {
      setCredentials([]);
    }
    setCredsLoading(false);
  }

  /* ─── TOTP ─── */

  function startTotpSetup() {
    setTotpError('');
    setPwPromptKind('totp');
    setPwPromptValue('');
    setPwPromptError('');
  }

  async function doStartTotpSetup(currentPassword) {
    setTotpLoading(true);
    try {
      const data = await post('/auth/mfa/totp/setup', { current_password: currentPassword });
      setTotpSetup(data);
      setTotpCode('');
      const url = await QRCode.toDataURL(data.uri, { width: 200 });
      setQrDataUrl(url);
      setPwPromptKind(null);
    } catch (e) {
      setPwPromptError(e?.message || 'Failed to start setup');
    }
    setTotpLoading(false);
  }

  async function verifyTotp() {
    if (totpCode.length !== 6) {
      setTotpError('Enter a 6-digit code');
      return;
    }
    setTotpVerifying(true);
    setTotpError('');
    try {
      await post('/auth/mfa/totp/verify', { code: totpCode });
      setTotpEnabled(true);
      setTotpSetup(null);
      setQrDataUrl('');
      // Update currentUser signal
      if (currentUser.value) {
        currentUser.value = { ...currentUser.value, mfa_app_enabled: true };
      }
    } catch (e) {
      setTotpError(e?.message || 'Invalid code, try again');
    }
    setTotpVerifying(false);
  }

  function cancelTotpSetup() {
    setTotpSetup(null);
    setQrDataUrl('');
    setTotpCode('');
    setTotpError('');
  }

  async function disableTotp() {
    if (!disableTotpPassword) {
      setDisableTotpError('Password is required');
      return;
    }
    setDisableTotpLoading(true);
    setDisableTotpError('');
    try {
      await api('/auth/mfa/totp', {
        method: 'DELETE',
        body: JSON.stringify({ current_password: disableTotpPassword }),
      });
      setTotpEnabled(false);
      setShowDisableTotp(false);
      setDisableTotpPassword('');
      if (currentUser.value) {
        currentUser.value = { ...currentUser.value, mfa_app_enabled: false };
      }
    } catch (e) {
      setDisableTotpError(e?.message || 'Failed to disable');
    }
    setDisableTotpLoading(false);
  }

  function copyAllBackupCodes() {
    if (!totpSetup?.backup_codes) return;
    navigator.clipboard.writeText(totpSetup.backup_codes.join('\n'));
  }

  function downloadBackupCodes() {
    if (!totpSetup?.backup_codes) return;
    const blob = new Blob([totpSetup.backup_codes.join('\n')], { type: 'text/plain' });
    const url = URL.createObjectURL(blob);
    const a = document.createElement('a');
    a.href = url;
    a.download = 'backup-codes.txt';
    a.click();
    URL.revokeObjectURL(url);
  }

  /* ─── WebAuthn ─── */

  function registerKey(isPasskey) {
    setRegisterError('');
    setPwPromptKind(isPasskey ? 'passkey' : 'key');
    setPwPromptValue('');
    setPwPromptError('');
  }

  async function doRegisterKey(isPasskey, currentPassword) {
    const name = prompt('Name for this credential:') || (isPasskey ? 'Passkey' : 'Security Key');
    try {
      const options = await post('/auth/mfa/webauthn/register/begin', {
        name,
        discoverable: isPasskey,
        current_password: currentPassword,
      });
      setPwPromptKind(null);
      const publicKey = prepareCreationOptions(options.publicKey || options);
      const credential = await navigator.credentials.create({ publicKey });
      const serialized = serializeCredential(credential);
      await post('/auth/mfa/webauthn/register/finish', serialized);
      loadCredentials();
    } catch (e) {
      if (pwPromptKind) {
        setPwPromptError(e?.message || 'Registration failed');
      } else if (e?.name === 'NotAllowedError') {
        setRegisterError('Registration was cancelled or timed out.');
      } else {
        setRegisterError(e?.message || 'Registration failed');
      }
    }
  }

  async function submitPasswordPrompt() {
    if (!pwPromptValue) {
      setPwPromptError('Password is required');
      return;
    }
    setPwPromptBusy(true);
    setPwPromptError('');
    const kind = pwPromptKind;
    const pw = pwPromptValue;
    if (kind === 'totp') {
      await doStartTotpSetup(pw);
    } else if (kind === 'passkey') {
      await doRegisterKey(true, pw);
    } else if (kind === 'key') {
      await doRegisterKey(false, pw);
    }
    setPwPromptBusy(false);
  }

  async function deleteCredential() {
    if (!deleteCredPassword) {
      setDeleteCredError('Password is required');
      return;
    }
    setDeleteCredLoading(true);
    setDeleteCredError('');
    try {
      await api('/auth/mfa/webauthn/' + deleteCredId, {
        method: 'DELETE',
        body: JSON.stringify({ current_password: deleteCredPassword }),
      });
      setDeleteCredId(null);
      setDeleteCredPassword('');
      loadCredentials();
    } catch (e) {
      setDeleteCredError(e?.message || 'Failed to remove credential');
    }
    setDeleteCredLoading(false);
  }

  return (
    <div>
      {/* ─── TOTP Section ─── */}
      <div class="card">
        <div class="section-header">
          <h3>Authenticator App</h3>
        </div>

        {!totpEnabled && !totpSetup && (
          <div style="padding: 16px 0">
            <p style="margin-bottom: 12px; color: var(--color-text-muted)">
              Use an authenticator app like Google Authenticator, Authy, or 1Password to generate one-time codes.
            </p>
            <button class="btn btn-primary" onClick={startTotpSetup} disabled={totpLoading}>
              {totpLoading ? 'Setting up\u2026' : 'Set up authenticator app'}
            </button>
            {totpError && <p class="form-error" style="margin-top: 8px">{totpError}</p>}
          </div>
        )}

        {totpSetup && (
          <div style="padding: 16px 0">
            <p style="margin-bottom: 16px">
              Scan this QR code with your authenticator app, then enter the 6-digit code to confirm.
            </p>

            <div style="display: flex; gap: 24px; flex-wrap: wrap; margin-bottom: 16px">
              {qrDataUrl && (
                <div>
                  <img src={qrDataUrl} alt="TOTP QR Code" style="width: 200px; height: 200px; display: block; border-radius: 4px" />
                </div>
              )}
              <div style="flex: 1; min-width: 200px">
                <label class="form-label">Manual entry key</label>
                <div style="display: flex; align-items: center; gap: 8px">
                  <code style="font-family: var(--font-mono); background: var(--bg-subtle); padding: 6px 10px; border-radius: 4px; font-size: 13px; letter-spacing: 1px; word-break: break-all">
                    {totpSetup.secret}
                  </code>
                  <button class="btn btn-xs btn-outline" onClick={() => navigator.clipboard.writeText(totpSetup.secret)}>
                    Copy
                  </button>
                </div>
              </div>
            </div>

            {totpSetup.backup_codes?.length > 0 && (
              <div class="backup-codes" style="margin-bottom: 16px; padding: 16px; background: var(--bg-subtle); border-radius: 6px; border: 1px solid var(--border)">
                <p style="margin-bottom: 12px"><strong>Save these backup codes.</strong> They cannot be shown again.</p>
                <div style="display: grid; grid-template-columns: repeat(2, 1fr); gap: 8px; font-family: var(--font-mono); font-size: 13px; margin-bottom: 12px">
                  {totpSetup.backup_codes.map(code => (
                    <span key={code} style="background: var(--bg); padding: 4px 8px; border-radius: 4px; border: 1px solid var(--border)">{code}</span>
                  ))}
                </div>
                <div class="btn-group">
                  <button class="btn btn-outline" onClick={copyAllBackupCodes}>Copy all</button>
                  <button class="btn btn-outline" onClick={downloadBackupCodes}>Download</button>
                </div>
              </div>
            )}

            <div class="form-group" style="max-width: 240px">
              <label class="form-label">Verification code</label>
              <input
                class="form-input"
                type="text"
                inputmode="numeric"
                autocomplete="one-time-code"
                maxLength={6}
                placeholder="000000"
                value={totpCode}
                onInput={e => { setTotpCode(e.target.value.replace(/\D/g, '')); setTotpError(''); }}
                onKeyDown={e => e.key === 'Enter' && verifyTotp()}
              />
            </div>

            {totpError && <p class="form-error" style="margin-top: 4px">{totpError}</p>}

            <div class="btn-group" style="margin-top: 12px">
              <button class="btn btn-primary" onClick={verifyTotp} disabled={totpVerifying || totpCode.length !== 6}>
                {totpVerifying ? 'Verifying\u2026' : 'Verify & enable'}
              </button>
              <button class="btn btn-outline" onClick={cancelTotpSetup} disabled={totpVerifying}>
                Cancel
              </button>
            </div>
          </div>
        )}

        {totpEnabled && !totpSetup && (
          <div style="padding: 16px 0; display: flex; align-items: center; gap: 12px">
            <span class="badge badge-success">Authenticator app enabled</span>
            <button class="btn btn-danger btn-sm" onClick={() => { setShowDisableTotp(true); setDisableTotpPassword(''); setDisableTotpError(''); }}>
              Disable
            </button>
          </div>
        )}
      </div>

      {/* ─── WebAuthn Section ─── */}
      <div class="card" style="margin-top: 16px">
        <div class="section-header">
          <h3>Security Keys &amp; Passkeys</h3>
          <div class="btn-group">
            <button class="btn btn-outline" onClick={() => registerKey(false)}>Add security key</button>
            <button class="btn btn-outline" onClick={() => registerKey(true)}>Add passkey</button>
          </div>
        </div>

        {registerError && (
          <p class="form-error" style="padding: 0 0 12px">{registerError}</p>
        )}

        {credsLoading ? (
          <p style="color: var(--color-text-muted); padding: 16px 0">Loading\u2026</p>
        ) : credentials.length === 0 ? (
          <p style="color: var(--color-text-muted); padding: 16px 0">No security keys or passkeys registered.</p>
        ) : (
          <div class="settings-list" style="margin-top: 8px">
            {credentials.map(c => (
              <div class="settings-item" key={c.id}>
                <div>
                  <strong>{c.name}</strong>
                  <span class="badge" style="margin-left: 8px">{c.discoverable ? 'Passkey' : 'Security Key'}</span>
                  <span class="text-muted" style="margin-left: 8px">Added {new Date(c.created_at).toLocaleDateString()}</span>
                </div>
                <button class="btn btn-danger btn-sm" onClick={() => { setDeleteCredId(c.id); setDeleteCredPassword(''); setDeleteCredError(''); }}>
                  Remove
                </button>
              </div>
            ))}
          </div>
        )}
      </div>

      {/* ─── Disable TOTP Modal ─── */}
      <Modal open={showDisableTotp} onClose={() => setShowDisableTotp(false)} title="Disable Authenticator App">
        <div class="form-group">
          <label class="form-label">Confirm your password</label>
          <input
            class="form-input"
            type="password"
            autocomplete="current-password"
            placeholder="Current password"
            value={disableTotpPassword}
            onInput={e => { setDisableTotpPassword(e.target.value); setDisableTotpError(''); }}
            onKeyDown={e => e.key === 'Enter' && disableTotp()}
          />
        </div>
        {disableTotpError && <p class="form-error">{disableTotpError}</p>}
        <div class="btn-group" style="margin-top: 16px">
          <button class="btn btn-danger" onClick={disableTotp} disabled={disableTotpLoading}>
            {disableTotpLoading ? 'Disabling\u2026' : 'Disable authenticator'}
          </button>
          <button class="btn btn-outline" onClick={() => setShowDisableTotp(false)} disabled={disableTotpLoading}>
            Cancel
          </button>
        </div>
      </Modal>

      {/* ─── Password Prompt (pre-register) ─── */}
      <Modal
        open={!!pwPromptKind}
        onClose={() => !pwPromptBusy && setPwPromptKind(null)}
        title={
          pwPromptKind === 'totp' ? 'Confirm your password'
            : pwPromptKind === 'passkey' ? 'Add a passkey'
            : 'Add a security key'
        }
      >
        <p style="margin-bottom: 12px">
          Enter your current password to continue.
        </p>
        <div class="form-group">
          <label class="form-label">Current password</label>
          <input
            class="form-input"
            type="password"
            autocomplete="current-password"
            placeholder="Current password"
            value={pwPromptValue}
            onInput={e => { setPwPromptValue(e.target.value); setPwPromptError(''); }}
            onKeyDown={e => e.key === 'Enter' && submitPasswordPrompt()}
          />
        </div>
        {pwPromptError && <p class="form-error">{pwPromptError}</p>}
        <div class="btn-group" style="margin-top: 16px">
          <button class="btn btn-primary" onClick={submitPasswordPrompt} disabled={pwPromptBusy}>
            {pwPromptBusy ? 'Working…' : 'Continue'}
          </button>
          <button class="btn btn-outline" onClick={() => setPwPromptKind(null)} disabled={pwPromptBusy}>
            Cancel
          </button>
        </div>
      </Modal>

      {/* ─── Delete Credential Modal ─── */}
      <Modal open={!!deleteCredId} onClose={() => setDeleteCredId(null)} title="Remove Credential">
        <p style="margin-bottom: 16px">Enter your password to confirm removal.</p>
        <div class="form-group">
          <label class="form-label">Current password</label>
          <input
            class="form-input"
            type="password"
            autocomplete="current-password"
            placeholder="Current password"
            value={deleteCredPassword}
            onInput={e => { setDeleteCredPassword(e.target.value); setDeleteCredError(''); }}
            onKeyDown={e => e.key === 'Enter' && deleteCredential()}
          />
        </div>
        {deleteCredError && <p class="form-error">{deleteCredError}</p>}
        <div class="btn-group" style="margin-top: 16px">
          <button class="btn btn-danger" onClick={deleteCredential} disabled={deleteCredLoading}>
            {deleteCredLoading ? 'Removing\u2026' : 'Remove'}
          </button>
          <button class="btn btn-outline" onClick={() => setDeleteCredId(null)} disabled={deleteCredLoading}>
            Cancel
          </button>
        </div>
      </Modal>
    </div>
  );
}
