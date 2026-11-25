import { useState, useRef, useEffect } from 'preact/hooks';
import { post } from '../../api/client';
import {
  TIMESTAMP_HEADER,
  SIGNATURE_HEADER,
  REPLAY_WINDOW_MINUTES,
  isUnsignedWebhook,
  rotatePrompt,
  interpretRotateError,
} from './webhook-signing-state.mjs';
import './webhook-signing.css';

export { isUnsignedWebhook, applyRotation, withoutSecret, shouldWarnBeforeClose } from './webhook-signing-state.mjs';

/* Signing secret for webhook channels. The server generates the secret and
   returns it only in the create and rotate-secret responses, so this shows
   it once and never fetches it. The caller owns the secret state so it
   survives list reloads and can warn before the modal closes. Spec:
   docs/webhooks.md. */

/* Pill in the channel list for a webhook created before signing existed. */
export function UnsignedTag({ channel }) {
  if (!isUnsignedWebhook(channel)) return null;
  return (
    <span class="label-tag tag-unsigned" title="Requests to this webhook are not signed. Open the channel to generate a signing secret.">
      Unsigned
    </span>
  );
}

/* Short note in the create form so the one-time secret is not a surprise. */
export function SigningCreateNote() {
  return (
    <p class="form-help webhook-signing-note">
      Saving creates a signing secret for this webhook. It is shown once, so have
      somewhere ready to store it.
    </p>
  );
}

function VerifyHelp() {
  return (
    <details class="webhook-signing-help">
      <summary>How to verify a request</summary>
      <ol>
        <li>
          Read <code>{TIMESTAMP_HEADER}</code> (Unix seconds). Reject the request if it is
          more than {REPLAY_WINDOW_MINUTES} minutes from your clock.
        </li>
        <li>
          Compute HMAC-SHA256 of <code>timestamp + "." + raw body</code>, keyed with the
          whole secret including <code>whsec_</code>.
        </li>
        <li>
          Compare the hex digest with the <code>v1=</code> value in{' '}
          <code>{SIGNATURE_HEADER}</code> using a constant-time compare. Ignore other
          schemes in that header.
        </li>
      </ol>
      <p>Use the raw request bytes. Parsing and re-encoding the JSON changes the signature.</p>
    </details>
  );
}

/* SigningSecretReveal shows a secret that was just issued.
   shown: { secret, copied }. warn: the user tried to close before copying. */
export function SigningSecretReveal({ shown, onCopied, warn, onDone, doneLabel = 'Done', idSuffix = 'new', hideLabel = false }) {
  const [copyFailed, setCopyFailed] = useState(false);
  const inputRef = useRef(null);
  const copyRef = useRef(null);
  const id = 'webhook-signing-secret-' + idSuffix;
  const secret = shown.secret;

  useEffect(() => {
    // Move focus to the secret so keyboard and screen reader users land on it.
    if (inputRef.current) {
      inputRef.current.focus();
      inputRef.current.select();
    }
  }, [secret]);

  useEffect(() => {
    if (warn && copyRef.current) copyRef.current.focus();
  }, [warn]);

  async function copy() {
    try {
      await navigator.clipboard.writeText(secret);
      setCopyFailed(false);
      if (onCopied) onCopied();
    } catch (_) {
      // Clipboard blocked (insecure context or denied). Select the text so
      // Ctrl+C or Cmd+C works, and say so.
      setCopyFailed(true);
      if (inputRef.current) {
        inputRef.current.focus();
        inputRef.current.select();
      }
    }
  }

  // Copying by hand (keyboard shortcut or context menu) counts too.
  function onManualCopy() {
    if (onCopied) onCopied();
  }

  const status = shown.copied ? 'Secret copied to the clipboard.'
    : copyFailed ? 'Copy is blocked in this browser. The secret is selected, press Ctrl+C or Cmd+C.'
    : '';

  return (
    <div class="webhook-signing webhook-signing-reveal">
      <p class="webhook-signing-lead" id={id + '-help'}>
        <strong>Copy this secret now.</strong> It is not shown again. Your receiver uses it
        to check the <code>{SIGNATURE_HEADER}</code> header on each request.
      </p>
      <label for={id} class={hideLabel ? 'sr-only' : 'webhook-signing-label'}>Signing secret</label>
      <div class="webhook-signing-row">
        <input id={id} ref={inputRef} type="text" readOnly value={secret}
          class="webhook-signing-value" aria-describedby={id + '-help'}
          autocomplete="off" autocapitalize="off" spellcheck={false}
          onFocus={e => e.target.select()} onCopy={onManualCopy} />
        <button type="button" ref={copyRef} class={'btn btn-sm' + (shown.copied ? '' : ' btn-primary')} onClick={copy}>
          {shown.copied ? 'Copied' : 'Copy'}
        </button>
      </div>
      <p class="webhook-signing-status" role="status">{status}</p>
      {warn && (
        <div class="webhook-signing-warn" role="alert">
          You have not copied the secret. If you close now, you will need to rotate it to
          get a new one.
        </div>
      )}
      <VerifyHelp />
      {onDone && (
        <div class="webhook-signing-actions">
          <button type="button" class={'btn' + (warn ? ' btn-danger' : ' btn-primary')} onClick={onDone}>
            {warn ? 'Close without copying' : doneLabel}
          </button>
        </div>
      )}
    </div>
  );
}

/* SigningSecretSection goes in the edit form of a webhook channel. It shows
   whether deliveries are signed and lets owners and admins rotate, with an
   inline confirmation (not a second modal on top of the channel modal).
   shown: the rotated secret for this channel, held by the parent. */
export function SigningSecretSection({ channel, canManage, shown, warn, onRotated, onCopied, onDismiss }) {
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const triggerRef = useRef(null);
  const cancelRef = useRef(null);
  const signed = channel.signing_secret_set === true;
  const prompt = rotatePrompt(signed);
  const headingId = 'webhook-signing-heading-' + channel.id;
  const confirmId = 'webhook-signing-confirm-' + channel.id;

  useEffect(() => {
    if (confirming && cancelRef.current) cancelRef.current.focus();
  }, [confirming]);

  function cancel() {
    setConfirming(false);
    // Return focus to the button that opened the confirmation.
    setTimeout(() => triggerRef.current && triggerRef.current.focus(), 0);
  }

  async function rotate() {
    setBusy(true);
    setError('');
    try {
      const res = await post('/notification-channels/' + channel.id + '/rotate-secret', {});
      setConfirming(false);
      if (onRotated) onRotated(res);
    } catch (e) {
      setError(interpretRotateError(e));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section class="form-group webhook-signing" aria-labelledby={headingId}>
      <div class="webhook-signing-head">
        <h4 id={headingId} class="webhook-signing-label">Signing secret</h4>
        <span class={'label-tag ' + (signed ? 'tag-signed' : 'tag-unsigned')}>
          {signed ? 'Signed' : 'Unsigned'}
        </span>
      </div>

      {shown ? (
        <SigningSecretReveal shown={shown} onCopied={onCopied} warn={warn}
          onDone={onDismiss} doneLabel="Hide secret" idSuffix={channel.id} hideLabel />
      ) : (
        <>
          <p class="form-help webhook-signing-text">
            {signed
              ? <>Each request carries <code>{TIMESTAMP_HEADER}</code> and <code>{SIGNATURE_HEADER}</code> headers. The secret was shown once when it was created and cannot be viewed again.</>
              : <>This webhook was created before signing was added, so its requests are not signed. Generate a secret, add it to your receiver, and every request after that is signed.</>}
            {!canManage && ' Only owners and admins can change it.'}
          </p>

          {error && <p class="field-error" role="alert">{error}</p>}

          {canManage && !confirming && (
            <button type="button" ref={triggerRef} class="btn btn-sm"
              onClick={() => { setError(''); setConfirming(true); }}>
              {prompt.confirm}
            </button>
          )}

          {canManage && confirming && (
            <div class="webhook-signing-confirm" role="group" aria-labelledby={confirmId}
              onKeyDown={e => { if (e.key === 'Escape') { e.stopPropagation(); cancel(); } }}>
              <p id={confirmId}><strong>{prompt.title}</strong> {prompt.body}</p>
              <div class="webhook-signing-actions">
                <button type="button" class={'btn btn-sm ' + (prompt.danger ? 'btn-danger' : 'btn-primary')}
                  onClick={rotate} disabled={busy} aria-busy={busy ? 'true' : undefined}>
                  {busy ? 'Working…' : prompt.confirm}
                </button>
                <button type="button" ref={cancelRef} class="btn btn-sm" onClick={cancel} disabled={busy}>
                  Cancel
                </button>
              </div>
            </div>
          )}

          {signed && <VerifyHelp />}
        </>
      )}
    </section>
  );
}
