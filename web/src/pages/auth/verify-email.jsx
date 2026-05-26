import { useEffect, useState } from 'preact/hooks';
import { verifyEmail, resendVerification } from '../../state/auth';

export function VerifyEmailPage() {
  const [state, setState] = useState('verifying'); // verifying | ok | error
  const [message, setMessage] = useState('');
  const [resendEmail, setResendEmail] = useState('');
  const [resendStatus, setResendStatus] = useState(null);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const token = params.get('token');
    if (!token) {
      setState('error');
      setMessage('Missing verification token.');
      return;
    }
    verifyEmail(token).then(
      (res) => {
        if (res && res.status === 'already_verified') {
          setState('ok');
          setMessage('Your email was already verified. You can sign in.');
        } else {
          setState('ok');
          setMessage('Email verified! You can now sign in.');
        }
      },
      (err) => {
        setState('error');
        setMessage(err.message || 'Verification failed.');
      }
    );
  }, []);

  async function handleResend(e) {
    e.preventDefault();
    setResendStatus(null);
    try {
      const res = await resendVerification(resendEmail);
      if (res && res.status === 'already_verified') {
        setResendStatus('Your email is already verified. You can sign in.');
      } else {
        setResendStatus('A new verification email has been sent.');
      }
    } catch (err) {
      setResendStatus(err.message || 'Failed to send verification email.');
    }
  }

  return (
    <div class="auth-page">
      <div class="auth-card">
        <div class="auth-header">
          <h1 class="logo">YipYap</h1>
          <p>Email verification</p>
        </div>

        {state === 'verifying' && <p>Verifying...</p>}

        {state === 'ok' && (
          <>
            <div class="form-success">{message}</div>
            <a href="/login" class="btn btn-primary btn-full">Go to sign in</a>
          </>
        )}

        {state === 'error' && (
          <>
            <div class="form-error">{message}</div>
            <p class="auth-hint">
              The link may have expired. Enter your email below to get a new one.
            </p>
            <form onSubmit={handleResend}>
              <div class="form-group">
                <label for="resend-email">Email</label>
                <input
                  id="resend-email"
                  type="email"
                  required
                  value={resendEmail}
                  onInput={(e) => setResendEmail(e.target.value)}
                  placeholder="you@company.com"
                />
              </div>
              <button type="submit" class="btn btn-primary btn-full">
                Send new verification email
              </button>
            </form>
            {resendStatus && <p class="auth-footer">{resendStatus}</p>}
          </>
        )}

        <p class="auth-footer">
          <a href="/login">Back to sign in</a>
        </p>
      </div>
    </div>
  );
}
