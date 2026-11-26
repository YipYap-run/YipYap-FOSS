import { useState } from 'preact/hooks';
import { post } from '../../api/client';

export function ConfirmRecoverPage() {
  const [status, setStatus] = useState('idle');
  const [message, setMessage] = useState('');

  const params = new URLSearchParams(window.location.search);
  const token = params.get('token');

  function handleRecover() {
    if (!token) {
      setStatus('error');
      setMessage('Missing recovery token.');
      return;
    }
    setStatus('loading');
    post('/auth/confirm-recover', { token })
      .then(() => {
        setStatus('success');
        setMessage('Your account is restored and will not be deleted. You can sign in again.');
      })
      .catch(err => {
        setStatus('error');
        if (err?.status === 401) {
          setMessage('This link has expired (links work for 1 hour) or was already used. Sign in to get a new one.');
        } else if (err?.status === 404 || err?.status === 410) {
          setMessage('This account no longer exists. It was deleted after the 4-day window ended.');
        } else {
          setMessage(err.message || 'Could not restore your account. Try again.');
        }
      });
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>{status === 'loading' ? 'Restoring...' : status === 'success' ? 'Account restored' : status === 'error' ? 'Could not restore your account' : 'Restore your account'}</p>
        </div>
        {status === 'idle' && (
          <>
            <p style="color: var(--color-text-secondary); font-size: 0.875rem">Restore your account to cancel the deletion and sign in again.</p>
            <button type="button" class="btn btn-primary" style="width: 100%" onClick={handleRecover}>
              Restore my account
            </button>
          </>
        )}
        {status === 'loading' && <div class="spinner" role="status" aria-label="Restoring" />}
        {status === 'success' && (
          <p role="status" style="color: var(--color-up-text); font-size: 0.875rem">{message}</p>
        )}
        {status === 'error' && (
          <div class="form-error" role="alert">{message}</div>
        )}
        <p class="auth-footer" style="margin-top: 16px">
          <a href="/login">Go to sign in</a>
        </p>
      </div>
    </div>
  );
}
