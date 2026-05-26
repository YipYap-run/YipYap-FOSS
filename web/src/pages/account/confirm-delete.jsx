import { useState } from 'preact/hooks';
import { post } from '../../api/client';

export function ConfirmDeletePage() {
  const [status, setStatus] = useState('idle');
  const [message, setMessage] = useState('');

  const params = new URLSearchParams(window.location.search);
  const token = params.get('token');

  function handleConfirm() {
    if (!token) {
      setStatus('error');
      setMessage('Missing confirmation token.');
      return;
    }
    setStatus('loading');
    post('/auth/confirm-delete', { token })
      .then(() => {
        setStatus('success');
        setMessage('Your account has been disabled. It will be permanently deleted in 96 hours. If you change your mind, log in to recover your account.');
      })
      .catch(err => {
        setStatus('error');
        setMessage(err.message || 'Invalid or expired confirmation link.');
      });
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>{status === 'loading' ? 'Confirming...' : status === 'success' ? 'Account Disabled' : status === 'error' ? 'Error' : 'Confirm Account Deletion'}</p>
        </div>
        {status === 'idle' && (
          <>
            <p style="color: var(--color-text-secondary); font-size: 0.875rem">Click below to confirm account deletion. Your account will be disabled and permanently deleted after 96 hours.</p>
            <button class="btn btn-primary" style="background: var(--color-down); border-color: var(--color-down); width: 100%" onClick={handleConfirm}>
              Confirm Deletion
            </button>
          </>
        )}
        {status === 'loading' && <div class="spinner" />}
        {status === 'success' && (
          <p style="color: var(--color-text-secondary); font-size: 0.875rem">{message}</p>
        )}
        {status === 'error' && (
          <div class="form-error">{message}</div>
        )}
        <p class="auth-footer" style="margin-top: 16px">
          <a href="/login">Back to login</a>
        </p>
      </div>
    </div>
  );
}
