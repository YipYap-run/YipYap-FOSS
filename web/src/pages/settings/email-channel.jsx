import { useState, useEffect, useRef } from 'preact/hooks';
import {
  EMAIL_MAX_RECIPIENTS_DEFAULT, splitRecipients, joinRecipients, addRecipients, isLikelyEmail,
} from './email-channel-logic.mjs';
import './email-channel.css';

// The API returns this in place of a stored secret. Sending it back unchanged
// keeps the saved password.
const REDACTED = '•'.repeat(8);

const fid = key => 'channel-field-' + key;

// aria wiring shared by every input: error text wins over help text.
function describe(key, errors, hasHelp) {
  if (errors[key]) return { 'aria-invalid': 'true', 'aria-describedby': fid(key) + '-error' };
  return hasHelp ? { 'aria-describedby': fid(key) + '-help' } : {};
}

function FieldMessage({ name, errors, help }) {
  if (errors[name]) return <div class="field-error" id={fid(name) + '-error'}>{errors[name]}</div>;
  if (help) return <div class="form-help" id={fid(name) + '-help'}>{help}</div>;
  return null;
}

function RecipientsInput({ value, onChange, errors, max }) {
  const list = splitRecipients(value);
  const [draft, setDraft] = useState('');
  const inputRef = useRef(null);

  function commit(text) {
    const entries = splitRecipients(text);
    if (entries.length) onChange(joinRecipients(addRecipients(list, entries)));
    setDraft('');
  }

  function remove(i) {
    onChange(joinRecipients(list.filter((_, j) => j !== i)));
    inputRef.current?.focus();
  }

  function onKeyDown(e) {
    if ((e.key === 'Enter' || e.key === ',' || e.key === ';') && draft.trim()) {
      e.preventDefault();
      commit(draft);
    } else if (e.key === 'Enter') {
      // An empty Enter would submit the whole channel form by accident.
      e.preventDefault();
    } else if (e.key === 'Backspace' && !draft && list.length) {
      e.preventDefault();
      setDraft(list[list.length - 1]);
      onChange(joinRecipients(list.slice(0, -1)));
    }
  }

  function onInput(e) {
    const v = e.target.value;
    if (/[,;\n]/.test(v)) commit(v);
    else setDraft(v);
  }

  const over = list.length > max;
  return (
    <div class={'form-group' + (errors.to ? ' has-error' : '')}>
      <label for={fid('to')} class="required">Recipients</label>
      <div class={'recipient-input' + (errors.to ? ' is-invalid' : '')} onClick={() => inputRef.current?.focus()}>
        {list.length > 0 && (
          <ul class="recipient-chips" aria-label="Recipients added">
            {list.map((addr, i) => {
              const ok = isLikelyEmail(addr);
              return (
                <li key={addr} class={'recipient-chip' + (ok ? '' : ' is-invalid')}>
                  <span class="recipient-chip-text">{addr}</span>
                  {!ok && <span class="sr-only"> (not a valid address)</span>}
                  <button type="button" class="recipient-chip-remove" aria-label={'Remove ' + addr}
                    onClick={e => { e.stopPropagation(); remove(i); }}>
                    <span aria-hidden="true">&times;</span>
                  </button>
                </li>
              );
            })}
          </ul>
        )}
        <input ref={inputRef} id={fid('to')} type="text" inputmode="email" autocomplete="off" autocapitalize="none" spellcheck={false}
          aria-required="true" {...describe('to', errors, true)}
          placeholder={list.length ? 'Add another' : 'oncall@company.com'}
          value={draft} onInput={onInput} onKeyDown={onKeyDown} onBlur={() => draft.trim() && commit(draft)}
          onPaste={e => {
            const text = e.clipboardData?.getData('text') || '';
            if (/[,;\n]/.test(text)) { e.preventDefault(); commit(draft + text); }
          }} />
      </div>
      <FieldMessage name="to" errors={errors}
        help={'Press Enter or a comma after each address. Up to ' + max + '.'} />
      <div class={'recipient-count' + (over ? ' is-over' : '')} aria-live="polite">
        {list.length} of {max}<span class="sr-only"> recipients</span>
      </div>
    </div>
  );
}

function PasswordInput({ value, onInput, errors, isEdit }) {
  const [show, setShow] = useState(false);
  const saved = isEdit && value === REDACTED;
  return (
    <div class={'form-group' + (errors.password ? ' has-error' : '')}>
      <label for={fid('password')} class="required">Password</label>
      <div class="secret-field">
        <input id={fid('password')} type={show && !saved ? 'text' : 'password'} value={value || ''}
          autocomplete="new-password" aria-required="true" {...describe('password', errors, true)}
          placeholder="App password or SMTP password"
          onFocus={e => saved && e.target.select()} onInput={onInput} />
        {!saved && (
          <button type="button" class="secret-toggle" aria-pressed={show ? 'true' : 'false'}
            aria-label={show ? 'Hide password' : 'Show password'} onClick={() => setShow(!show)}>
            {show ? 'Hide' : 'Show'}
          </button>
        )}
      </div>
      <FieldMessage name="password" errors={errors}
        help={saved ? 'Saved. Leave it as is to keep the current password.' : 'Many providers want an app password here, not your login password.'} />
    </div>
  );
}

// Email channel fields. Recipients are always asked for; "Use my own SMTP
// server" reveals the server settings, which are then all required. When the
// server has no platform mailer (platformEmail false) that section is the only
// option and stays open.
export function EmailChannelFields({ config, setConfig, clearErrors = () => {}, errors, platformEmail, maxRecipients, edition, isEdit }) {
  const max = maxRecipients || EMAIL_MAX_RECIPIENTS_DEFAULT;
  const ownSMTP = config.mode === 'smtp';
  const forced = platformEmail === false;

  // New channels start in platform mode; a server without a mailer can only
  // offer the SMTP section. A forced switch keeps the "mode" error, which is
  // what tells the user why the section opened.
  useEffect(() => {
    if (forced && config.mode !== 'smtp') setMode(true, true);
    else if (!config.mode) setMode(false);
  }, [forced, config.mode]);

  // The checkbox is disabled when forced, so it cannot take focus. Send focus
  // to the first server field so the user lands where the fix is.
  useEffect(() => {
    if (forced && ownSMTP && errors.mode) document.getElementById(fid('host'))?.focus();
  }, [forced, ownSMTP, errors.mode]);

  function setMode(on, keepModeError = false) {
    // Turning the section off hides the server fields, so their errors go too.
    if (!keepModeError) clearErrors(on ? ['mode'] : ['mode', 'host', 'port', 'tls_mode', 'username', 'password', 'from']);
    setConfig(c => on
      ? { ...c, mode: 'smtp', port: c.port || 587, tls_mode: c.tls_mode || 'starttls' }
      : { ...c, mode: 'platform' });
  }

  function set(key, value) {
    clearErrors([key]);
    setConfig(c => ({ ...c, [key]: value }));
  }

  // Keep the port in step with the encryption choice when it still holds the
  // other mode's usual port.
  function setTLSMode(v) {
    clearErrors(['tls_mode', 'port']);
    setConfig(c => {
      const port = String(c.port || '');
      const next = { ...c, tls_mode: v };
      if (v === 'tls' && (port === '587' || port === '')) next.port = 465;
      if (v === 'starttls' && (port === '465' || port === '')) next.port = 587;
      return next;
    });
  }

  const sentBy = edition === 'foss' ? "this server's mail settings" : 'the YipYap mail service';
  const text = (key, label, props = {}) => (
    <div class={'form-group' + (errors[key] ? ' has-error' : '')}>
      <label for={fid(key)} class="required">{label}</label>
      <input id={fid(key)} type="text" autocomplete="off" spellcheck={false} autocapitalize="none"
        aria-required="true" {...describe(key, errors, !!props.help)}
        value={config[key] ?? ''} placeholder={props.placeholder} inputmode={props.inputmode}
        onInput={e => set(key, e.target.value)} />
      <FieldMessage name={key} errors={errors} help={props.help} />
    </div>
  );

  return (
    <div class="email-channel">
      <RecipientsInput value={config.to} onChange={v => set('to', v)} errors={errors} max={max} />

      <div class={'email-smtp' + (ownSMTP ? ' is-open' : '')}>
        <div class="form-check email-smtp-toggle">
          <label>
            <input type="checkbox" id={fid('mode')} checked={ownSMTP} disabled={forced}
              aria-controls="own-smtp-fields" {...describe('mode', errors, true)}
              onChange={e => setMode(e.target.checked)} />
            <span>Use my own SMTP server</span>
          </label>
        </div>
        <FieldMessage name="mode" errors={errors} help={forced
          ? 'No mail service is set up on this server, so email channels send through your own SMTP server.'
          : ownSMTP
            ? 'Alerts go out through your mail server. Every field below is required.'
            : 'Alerts are sent by ' + sentBy + '. Turn this on to send through your own mail server instead.'} />

        {ownSMTP && (
          <fieldset id="own-smtp-fields" class="email-smtp-fields">
            <legend>Your SMTP server</legend>
            {text('host', 'SMTP host', { placeholder: 'smtp.company.com', help: 'Host name only, without smtp:// or a port.' })}
            <div class="email-smtp-row">
              <div class={'form-group' + (errors.tls_mode ? ' has-error' : '')}>
                <label for={fid('tls_mode')} class="required">Encryption</label>
                <select id={fid('tls_mode')} aria-required="true" {...describe('tls_mode', errors, false)}
                  value={config.tls_mode || ''} onChange={e => setTLSMode(e.target.value)}>
                  <option value="" disabled>Choose one</option>
                  <option value="starttls">STARTTLS (usually port 587)</option>
                  <option value="tls">TLS on connect (usually port 465)</option>
                </select>
                <FieldMessage name="tls_mode" errors={errors} />
              </div>
              {text('port', 'Port', { placeholder: '587', inputmode: 'numeric' })}
            </div>
            {text('username', 'Username', { placeholder: 'alerts@company.com' })}
            <PasswordInput value={config.password} isEdit={isEdit} errors={errors}
              onInput={e => set('password', e.target.value)} />
            {text('from', 'From address', { placeholder: 'alerts@company.com', help: 'Your server must be allowed to send as this address.' })}
          </fieldset>
        )}
      </div>
    </div>
  );
}
