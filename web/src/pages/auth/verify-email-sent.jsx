import { useState, useEffect } from 'preact/hooks';
import { resendVerification } from '../../state/auth';

const COOLDOWN_SECS = 30;

export function VerifyEmailSentPage() {
  const params = new URLSearchParams(window.location.search);
  const email = params.get('email') || '';

  const [cooldown, setCooldown] = useState(COOLDOWN_SECS);
  const [status, setStatus] = useState(null);
  const [error, setError] = useState(null);
  const [sending, setSending] = useState(false);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setTimeout(() => setCooldown(c => c - 1), 1000);
    return () => clearTimeout(t);
  }, [cooldown]);

  async function handleResend() {
    setError(null);
    setStatus(null);
    setSending(true);
    try {
      const res = await resendVerification(email);
      if (res && res.status === 'already_verified') {
        setStatus('Your email is already verified. You can log in now.');
      } else {
        setStatus('Verification email sent. Please check your inbox.');
      }
      setCooldown(COOLDOWN_SECS);
    } catch (err) {
      if (err.status === 429 && err.body?.retry_after) {
        setCooldown(err.body.retry_after);
        setError(err.body.error || 'Please wait before requesting another email.');
      } else {
        setError(err.message || 'Failed to send verification email.');
      }
    } finally {
      setSending(false);
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Check your email</p>
        </div>
        <p>
          We sent a verification link to <strong>{email || 'your email'}</strong>.
          Click the link to activate your account.
        </p>
        <p class="auth-hint">
          Didn't get it? Check your spam folder, or use the button below to resend.
        </p>

        {status && <div class="form-success">{status}</div>}
        {error && <div class="form-error">{error}</div>}

        <button
          type="button"
          class="btn btn-secondary btn-full"
          onClick={handleResend}
          disabled={sending || cooldown > 0}
        >
          {sending
            ? 'Sending...'
            : cooldown > 0
              ? `Resend in ${cooldown}s`
              : 'Resend verification email'}
        </button>

        <p class="auth-footer">
          <a href="/login">Back to sign in</a>
        </p>
      </div>
    </div>
  );
}
