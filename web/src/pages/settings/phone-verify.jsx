import { useState, useEffect, useRef } from 'preact/hooks';
import { post } from '../../api/client';
import {
  CODE_LENGTH,
  LIVE_COUNTDOWN_MAX_SECONDS,
  defaultMethod,
  normalizeCode,
  formatWait,
  formatCountdown,
  interpretSendError,
  interpretConfirmError,
  isUnverified,
} from './phone-verify-state.mjs';
import './phone-verify.css';

export { isPhoneChannel, isUnverified } from './phone-verify-state.mjs';

/* Small amber pill shown next to an SMS or voice channel that gets no
   alerts until its number is confirmed. */
export function UnverifiedTag({ channel }) {
  if (!isUnverified(channel)) return null;
  return (
    <span class="label-tag tag-unverified" title="This number gets no alerts until it is verified">
      Unverified
    </span>
  );
}

/* Notice for the edit form of an unverified phone channel. */
export function UnverifiedNotice({ onVerify }) {
  return (
    <div class="phone-verify-notice" role="note">
      <p>
        <strong>This number is not verified.</strong> It gets no alerts or test messages
        until someone with access to the phone enters the code we send.
      </p>
      {onVerify && (
        <button type="button" class="btn btn-sm btn-primary" onClick={onVerify}>
          Verify number
        </button>
      )}
    </div>
  );
}

function nowSecs() {
  return Date.now() / 1000;
}

/* PhoneVerifyPanel runs the send code / enter code flow for one channel.
   It owns no modal: the caller places it in the channel modal. */
export function PhoneVerifyPanel({ channel, onVerified, onLater }) {
  const preferred = defaultMethod(channel.type);
  const [sent, setSent] = useState(null); // last 202 body
  const [showCode, setShowCode] = useState(false);
  const [code, setCode] = useState('');
  const [sending, setSending] = useState(null); // 'sms' | 'voice' | null
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState('');
  const [unavailable, setUnavailable] = useState(false);
  const [resendAt, setResendAt] = useState(0); // epoch seconds
  const [now, setNow] = useState(nowSecs());
  const [announce, setAnnounce] = useState('');
  const codeRef = useRef(null);
  const codeId = 'phone-verify-code-' + channel.id;
  const errId = 'phone-verify-error-' + channel.id;
  const helpId = 'phone-verify-help-' + channel.id;

  const wait = Math.max(0, Math.ceil(resendAt - now));
  const coolingDown = wait > 0;

  // Tick once a second while a cooldown runs.
  useEffect(() => {
    if (!coolingDown) return undefined;
    const t = setInterval(() => setNow(nowSecs()), 1000);
    return () => clearInterval(t);
  }, [coolingDown]);

  // Tell screen reader users once when Resend comes back, not every second.
  const wasCooling = useRef(false);
  useEffect(() => {
    if (wasCooling.current && !coolingDown) setAnnounce('You can request a new code now.');
    wasCooling.current = coolingDown;
  }, [coolingDown]);

  useEffect(() => {
    if (showCode && codeRef.current) codeRef.current.focus();
  }, [showCode, sent]);

  function startCooldown(secs) {
    if (secs > 0) {
      setNow(nowSecs());
      setResendAt(nowSecs() + secs);
    }
  }

  async function send(method) {
    setSending(method);
    setError('');
    setAnnounce('');
    try {
      const res = await post('/notification-channels/' + channel.id + '/verify/send', { method });
      setSent(res);
      setCode('');
      setShowCode(true);
      const cooldown = Number(res.resend_after_seconds) || 60;
      startCooldown(cooldown);
      const how = res.method === 'voice' ? 'We are calling' : 'We sent a text to';
      setAnnounce(how + ' the number ending in ' + (res.phone_last4 || '') +
        '. Enter the 6-digit code. You can request another code in ' + formatWait(cooldown) + '.');
    } catch (e) {
      const r = interpretSendError(e);
      if (r.alreadyVerified) {
        onVerified && onVerified({ ...channel, verification: { status: 'verified' } });
        return;
      }
      setError(r.message);
      setUnavailable(r.unavailable);
      startCooldown(r.retryAfter);
    } finally {
      setSending(null);
    }
  }

  async function confirm(e) {
    e && e.preventDefault();
    if (checking) return;
    const c = normalizeCode(code);
    if (c.length !== CODE_LENGTH) {
      setError('Enter the 6-digit code.');
      codeRef.current && codeRef.current.focus();
      return;
    }
    setChecking(true);
    setError('');
    try {
      const updated = await post('/notification-channels/' + channel.id + '/verify/confirm', { code: c });
      onVerified && onVerified(updated);
    } catch (err) {
      const r = interpretConfirmError(err);
      setError(r.message);
      if (r.needNewCode) {
        setCode('');
        setSent(null);
      } else {
        codeRef.current && codeRef.current.select();
      }
    } finally {
      setChecking(false);
    }
  }

  const number = (channel.config && channel.config.to) || 'this number';
  const busy = !!sending || checking;
  const sendDisabled = busy || coolingDown || unavailable;
  const longWait = wait > LIVE_COUNTDOWN_MAX_SECONDS;
  const waitLabel = longWait ? 'in ' + formatWait(wait) : formatCountdown(wait);

  function sendLabel(method) {
    if (sending === method) return method === 'voice' ? 'Calling…' : 'Sending…';
    const verb = sent
      ? (method === 'voice' ? 'Call again' : 'Resend text')
      : (method === 'voice' ? 'Call with a code' : 'Text a code');
    return coolingDown ? verb + ' (' + waitLabel + ')' : verb;
  }

  const methods = preferred === 'voice' ? ['voice', 'sms'] : ['sms', 'voice'];

  return (
    <div class="phone-verify">
      <p class="phone-verify-lead">
        Confirm <strong class="phone-verify-number">{number}</strong> before it gets alerts. We send a
        6-digit code to the number. The code expires after 10 minutes.
      </p>
      {preferred === 'voice' && !sent && (
        <p class="form-help">Voice channels default to a call, since the number may not receive texts.</p>
      )}

      {sent && (
        <p class="phone-verify-status">
          {sent.method === 'voice' ? 'Calling' : 'Code sent by text to'} the number ending in{' '}
          <strong>{sent.phone_last4}</strong>.
        </p>
      )}

      {!unavailable && (
        <div class="btn-group phone-verify-send">
          {methods.map((m, i) => (
            <button
              key={m}
              type="button"
              class={'btn btn-sm' + (i === 0 && !sent ? ' btn-primary' : '')}
              onClick={() => send(m)}
              disabled={sendDisabled}
            >
              {sendLabel(m)}
            </button>
          ))}
        </div>
      )}

      {!showCode && !unavailable && (
        <button type="button" class="btn-link phone-verify-have-code" onClick={() => setShowCode(true)}>
          I already have a code
        </button>
      )}

      {showCode && (
        <form class="phone-verify-form" onSubmit={confirm} noValidate>
          <div class={'form-group' + (error ? ' has-error' : '')}>
            <label for={codeId}>Verification code</label>
            <div class="phone-verify-row">
              <input
                id={codeId}
                ref={codeRef}
                class="phone-verify-input"
                type="text"
                inputmode="numeric"
                autocomplete="one-time-code"
                pattern="[0-9]*"
                maxLength={CODE_LENGTH + 2}
                value={code}
                aria-invalid={error ? 'true' : undefined}
                aria-describedby={(error ? errId + ' ' : '') + helpId}
                onInput={e => { setCode(normalizeCode(e.target.value)); if (error) setError(''); }}
              />
              <button type="submit" class="btn btn-primary" disabled={checking || code.length !== CODE_LENGTH}>
                {checking ? 'Checking…' : 'Verify'}
              </button>
            </div>
            <div id={helpId} class="form-help">
              {sent && sent.attempts_remaining
                ? 'You have ' + sent.attempts_remaining + ' tries before you need a new code.'
                : 'Enter the 6 digits from the text or call.'}
            </div>
          </div>
        </form>
      )}

      <div id={errId} class="field-error phone-verify-error" role="alert">{error}</div>
      <div class="sr-only" aria-live="polite">{announce}</div>

      {onLater && (
        <div class="phone-verify-footer">
          <button type="button" class="btn btn-sm" onClick={onLater} disabled={checking}>
            Verify later
          </button>
          <span class="form-help">The channel stays inactive until the number is verified.</span>
        </div>
      )}
    </div>
  );
}
