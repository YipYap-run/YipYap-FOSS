import { useState } from 'preact/hooks';
import { route } from 'preact-router';
import { post, api } from '../../api/client';
import { mfaState, completeLogin } from '../../state/auth';
import { prepareAssertionOptions, serializeAssertion } from '../../utils/webauthn';

export default function MFAChallenge() {
  if (!mfaState.value) {
    route('/login');
    return null;
  }

  const { mfa_token, mfa_methods } = mfaState.value;
  const [method, setMethod] = useState(mfa_methods[0]);
  const [code, setCode] = useState('');
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(false);

  async function submitTOTP(codeValue) {
    setLoading(true);
    setError('');
    try {
      const res = await post('/auth/mfa/challenge', { mfa_token, type: 'totp', code: codeValue });
      mfaState.value = null;
      await completeLogin(res.token, res.user);
      route('/');
    } catch (err) {
      setError(err.message || 'Invalid code');
    } finally {
      setLoading(false);
    }
  }

  async function submitBackupCode() {
    setLoading(true);
    setError('');
    try {
      const res = await post('/auth/mfa/challenge', { mfa_token, type: 'backup_code', code });
      mfaState.value = null;
      await completeLogin(res.token, res.user);
      route('/');
    } catch (err) {
      setError(err.message || 'Invalid backup code');
    } finally {
      setLoading(false);
    }
  }

  async function submitWebAuthn() {
    setLoading(true);
    setError('');
    try {
      const beginRes = await post('/auth/mfa/webauthn/begin', { mfa_token });
      const opts = beginRes.options || beginRes;
      const publicKey = prepareAssertionOptions(opts.publicKey || opts);
      const assertion = await navigator.credentials.get({ publicKey });
      const serialized = serializeAssertion(assertion);

      const res = await api(
        '/auth/mfa/webauthn/finish?session_id=' +
          encodeURIComponent(beginRes.session_id),
        { method: 'POST', body: JSON.stringify({ ...serialized, mfa_token }) }
      );

      mfaState.value = null;
      await completeLogin(res.token, res.user);
      route('/');
    } catch (err) {
      setError(err.message || 'Security key verification failed');
    } finally {
      setLoading(false);
    }
  }

  function handleTOTPInput(e) {
    const v = e.target.value.replace(/\D/g, '').slice(0, 6);
    setCode(v);
    if (v.length === 6) submitTOTP(v);
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h2>Two-Factor Authentication</h2>
          <p>Verify your identity to continue</p>
        </div>

        {mfa_methods.length > 1 && (
          <div class="tabs" style="margin-bottom: 16px">
            {mfa_methods.includes('totp') && (
              <button
                class={'tab' + (method === 'totp' ? ' active' : '')}
                onClick={() => { setMethod('totp'); setCode(''); setError(''); }}
              >
                Authenticator
              </button>
            )}
            {mfa_methods.includes('webauthn') && (
              <button
                class={'tab' + (method === 'webauthn' ? ' active' : '')}
                onClick={() => { setMethod('webauthn'); setCode(''); setError(''); }}
              >
                Security Key
              </button>
            )}
            <button
              class={'tab' + (method === 'backup' ? ' active' : '')}
              onClick={() => { setMethod('backup'); setCode(''); setError(''); }}
            >
              Backup Code
            </button>
          </div>
        )}

        {error && <div class="form-error">{error}</div>}

        {method === 'totp' && (
          <div class="form-group">
            <label>Enter 6-digit code from your authenticator app</label>
            <input
              type="text"
              inputMode="numeric"
              pattern="[0-9]*"
              maxLength="6"
              value={code}
              onInput={handleTOTPInput}
              autoFocus
              disabled={loading}
              style="font-size: 1.5rem; text-align: center; letter-spacing: 0.5em"
            />
          </div>
        )}

        {method === 'webauthn' && (
          <button class="btn btn-primary btn-full" onClick={submitWebAuthn} disabled={loading}>
            {loading ? 'Waiting for key...' : 'Use Security Key'}
          </button>
        )}

        {method === 'backup' && (
          <form onSubmit={e => { e.preventDefault(); submitBackupCode(); }}>
            <div class="form-group">
              <label>Enter a backup code</label>
              <input
                type="text"
                value={code}
                onInput={e => setCode(e.target.value)}
                autoFocus
                disabled={loading}
                style="font-family: var(--font-mono)"
              />
            </div>
            <button type="submit" class="btn btn-primary btn-full" disabled={loading || !code}>
              {loading ? 'Verifying...' : 'Verify'}
            </button>
          </form>
        )}

        <p class="auth-footer" style="margin-top: 16px">
          <a href="/login" onClick={() => { mfaState.value = null; }}>Back to login</a>
        </p>
      </div>
    </div>
  );
}
