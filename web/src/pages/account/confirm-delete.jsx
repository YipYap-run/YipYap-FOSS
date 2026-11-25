import { useState } from 'preact/hooks';
import { post } from '../../api/client';
import { currentUser } from '../../state/auth';

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
        // The server signed this browser out; drop the cached user too.
        currentUser.value = null;
        setStatus('success');
        setMessage('Your account is disabled and will be deleted in 4 days. To keep it, sign in during that time and we will email you a link to restore it.');
      })
      .catch(err => {
        setStatus('error');
        if (err?.status === 404 || err?.status === 410) {
          setMessage('This account was already deleted.');
        } else if (err?.status === 409) {
          setMessage('You own this organization and it still has other members. Transfer ownership in Settings, then request deletion again.');
        } else if (err?.status === 401) {
          setMessage('This link has expired (links work for 1 hour), was already used, or your email or password changed since it was sent. Request deletion again from Settings.');
        } else {
          setMessage(err.message || 'Could not confirm the deletion. Try again.');
        }
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
            <p style="color: var(--color-text-secondary); font-size: 0.875rem">Your account will be disabled right away and deleted after 4 days. To keep it, sign in during that time and we will email you a link to restore it.</p>
            <button type="button" class="btn btn-primary" style="background: var(--color-down); border-color: var(--color-down); width: 100%" onClick={handleConfirm}>
              Confirm Deletion
            </button>
          </>
        )}
        {status === 'loading' && <div class="spinner" role="status" aria-label="Confirming" />}
        {status === 'success' && (
          <p role="status" style="color: var(--color-text-secondary); font-size: 0.875rem">{message}</p>
        )}
        {status === 'error' && (
          <div class="form-error" role="alert">{message}</div>
        )}
        <p class="auth-footer" style="margin-top: 16px">
          <a href="/login">Back to sign in</a>
        </p>
      </div>
    </div>
  );
}
