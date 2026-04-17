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
        setMessage('Your account has been re-enabled. You can now log in.');
      })
      .catch(err => {
        setStatus('error');
        setMessage(err.message || 'Invalid or expired recovery link.');
      });
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>{status === 'loading' ? 'Recovering...' : status === 'success' ? 'Account Restored' : status === 'error' ? 'Error' : 'Recover Your Account'}</p>
        </div>
        {status === 'idle' && (
          <>
            <p style="color: var(--color-text-secondary); font-size: 0.875rem">Click below to recover your account and re-enable access.</p>
            <button class="btn btn-primary" style="width: 100%" onClick={handleRecover}>
              Recover Account
            </button>
          </>
        )}
        {status === 'loading' && <div class="spinner" />}
        {status === 'success' && (
          <p style="color: var(--color-up); font-size: 0.875rem">{message}</p>
        )}
        {status === 'error' && (
          <div class="form-error">{message}</div>
        )}
        <p class="auth-footer" style="margin-top: 16px">
          <a href="/login">Go to login</a>
        </p>
      </div>
    </div>
  );
}
