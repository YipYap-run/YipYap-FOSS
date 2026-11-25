import { useState, useEffect, useRef } from 'preact/hooks';
import { signal } from '@preact/signals';
import { get, post } from '../../api/client';
import {
  POLL_INTERVAL_MS,
  isTelegramUnlinked,
  usesOwnBot,
  telegramChatLabel,
  secondsLeft,
  formatMinutesLeft,
  formatCountdown,
  expiresSoon,
  isFinalStatus,
  statusMessage,
  linkCodeRequest,
  interpretLinkCodeError,
  safeTelegramLink,
  pollFailure,
  nameChangedSinceCode,
  linkedAnnouncement,
} from './telegram-link-state.mjs';
import './telegram-link.css';

export { isTelegramUnlinked, telegramSummary, NAME_MAX as TELEGRAM_NAME_MAX } from './telegram-link-state.mjs';

// Text for TelegramLinkAnnouncer. Module level so the message survives the
// channel modal closing on link.
const announcement = signal('');
let announceTimer = null;

/* announceTelegramLinked puts "Linked to <chat title>." in the page live
   region. It clears first so linking a second chat with the same title is
   still read out. */
export function announceTelegramLinked(res) {
  const msg = linkedAnnouncement(res);
  announcement.value = '';
  clearTimeout(announceTimer);
  announceTimer = setTimeout(() => { announcement.value = msg; }, 50);
}

/* TelegramLinkAnnouncer is a polite live region that stays mounted outside
   the channel modal. Render it once per page that links chats. */
export function TelegramLinkAnnouncer() {
  return <div class="telegram-link-sr" role="status" aria-live="polite">{announcement.value}</div>;
}

/* Amber pill for a Telegram channel that has no chat yet. */
export function TelegramNotLinkedTag({ channel }) {
  if (!isTelegramUnlinked(channel)) return null;
  return (
    <span class="label-tag tag-telegram-unlinked" title="No chat is linked, so this channel gets no alerts">
      Not linked
    </span>
  );
}

/* TelegramLinkPanel gets a one-time code and waits for a chat to send it
   to the bot. For a new channel pass name (the channel is created when the
   chat sends the code); for an existing one pass channelId.
   onNeedName is called when a new channel has no name yet. startRef, when
   given, is set to the start function so the form's Enter key can ask for
   a code. onCancel shows a Cancel button before a code exists. */
export function TelegramLinkPanel({ channelId, name, botUsername, onLinked, onNeedName, startLabel, startRef, onCancel }) {
  const [code, setCode] = useState(null); // 201 body
  const [status, setStatus] = useState(null); // last poll body
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [unavailable, setUnavailable] = useState(false);
  const [copied, setCopied] = useState(false);
  const [now, setNow] = useState(Date.now());
  const [codeName, setCodeName] = useState('');
  const [pollNote, setPollNote] = useState('');
  const [gone, setGone] = useState(false);
  const commandRef = useRef(null);
  const idBase = 'telegram-link-' + (channelId || 'new');
  const linkedRef = useRef(false);

  const state = status?.status || code?.status || null;
  const left = code ? secondsLeft(code.expires_at, now) : 0;
  const waiting = !!code && !gone && !isFinalStatus(state);

  // The countdown ticks every second while the code is pending.
  useEffect(() => {
    if (!waiting) return undefined;
    const tick = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(tick);
  }, [waiting, code && code.id]);

  // Poll the code until it is linked, expired or failed.
  useEffect(() => {
    if (!waiting) return undefined;
    let stopped = false;
    let failures = 0;
    const timer = setInterval(async () => {
      try {
        const res = await get('/telegram/link-codes/' + code.id);
        if (stopped) return;
        failures = 0;
        setPollNote('');
        setStatus(res);
        if (res.status === 'linked' && !linkedRef.current) {
          linkedRef.current = true;
          onLinked && onLinked(res);
        }
      } catch (e) {
        if (stopped) return;
        // A blip should not end the wait; a missing code should.
        failures += 1;
        const f = pollFailure(e, failures);
        setPollNote(f.message);
        if (f.stop) setGone(true);
      }
    }, POLL_INTERVAL_MS);
    return () => { stopped = true; clearInterval(timer); };
  }, [waiting, code && code.id]);

  useEffect(() => {
    if (code && commandRef.current) commandRef.current.focus();
  }, [code && code.id]);

  async function start() {
    if (!channelId && !String(name || '').trim()) {
      onNeedName && onNeedName();
      return;
    }
    setBusy(true);
    setError('');
    setCopied(false);
    try {
      const res = await post('/telegram/link-codes', linkCodeRequest({ channelId, name }));
      linkedRef.current = false;
      setStatus(null);
      setPollNote('');
      setGone(false);
      setNow(Date.now());
      setCodeName(channelId ? '' : String(name || '').trim());
      setCode(res);
    } catch (e) {
      const r = interpretLinkCodeError(e);
      setError(r.message);
      setUnavailable(r.unavailable);
    } finally {
      setBusy(false);
    }
  }

  async function copy() {
    try {
      await navigator.clipboard.writeText(code.command);
      setCopied(true);
    } catch (_) {
      if (commandRef.current) {
        commandRef.current.focus();
        commandRef.current.select();
      }
    }
  }

  if (startRef) startRef.current = (!code || gone || isFinalStatus(state)) && !busy && !unavailable ? start : null;

  const bot = botUsername || code?.bot_username || '';
  const botName = bot ? '@' + bot : 'the YipYap bot';

  if (!code) {
    return (
      <div class="telegram-link">
        <p class="form-help">
          YipYap gives you a one-time code. Send it to {botName} in the Telegram chat that should get
          the alerts, and the bot links that chat.
        </p>
        {(!unavailable || onCancel) && (
          <div class="telegram-link-actions">
            {!unavailable && (
              <button type="button" class="btn btn-sm btn-primary" onClick={start} disabled={busy}>
                {busy ? 'Getting a code…' : (startLabel || 'Get link code')}
              </button>
            )}
            {onCancel && (
              <button type="button" class="btn btn-sm" onClick={onCancel} disabled={busy}>
                Keep the current chat
              </button>
            )}
          </div>
        )}
        <div class="field-error" role="alert">{error}</div>
      </div>
    );
  }

  const final = isFinalStatus(state) || gone;
  const linkedTitle = status?.chat_title || '';
  const groupLink = safeTelegramLink(code.group_link);
  const privateLink = safeTelegramLink(code.private_link);
  const renamed = nameChangedSinceCode(codeName, name);
  return (
    <div class="telegram-link">
      {state !== 'linked' && (
        <ol class="telegram-link-steps">
          <li>
            {bot && groupLink && privateLink
              ? <>For a group, add <a href={groupLink} target="_blank" rel="noopener noreferrer">{botName} to the group</a>. For alerts to you alone, <a href={privateLink} target="_blank" rel="noopener noreferrer">open a chat with {botName}</a>.</>
              : <>Add {botName} to the group, or open a private chat with it.</>}
          </li>
          <li>
            <label for={idBase + '-command'}>Send this message in the chat:</label>
            <div class="telegram-link-row">
              <input id={idBase + '-command'} ref={commandRef} class="telegram-link-command" type="text" readOnly
                value={code.command} onFocus={e => e.target.select()}
                aria-describedby={idBase + '-status'}
                autocomplete="off" autocapitalize="off" spellcheck={false} />
              <button type="button" class="btn btn-sm" onClick={copy} disabled={final}>
                {copied ? 'Copied' : 'Copy'}
              </button>
            </div>
          </li>
        </ol>
      )}
      {/* Only the state lives in the status region, so screen readers hear
          "linked" or "expired" once, not every minute tick. */}
      <div id={idBase + '-status'} class="telegram-link-status">
        <p role="status" class={state === 'linked' ? 'is-linked' : (pollNote ? 'telegram-link-warn' : '')}>
          {gone ? pollNote : statusMessage(state, linkedTitle, waiting && expiresSoon(left)) + (pollNote ? ' ' + pollNote : '')}
        </p>
        {/* Outside the live region: sighted users see m:ss, screen readers
            read whole minutes when they reach this line. */}
        {waiting && (
          <p class="telegram-link-countdown">
            The code works once.{' '}
            <span aria-hidden="true">Expires in <time>{formatCountdown(left)}</time></span>
            <span class="telegram-link-sr">{formatMinutesLeft(left)}</span>
          </p>
        )}
      </div>
      {waiting && codeName && (
        <p class="form-help">
          The new channel will be named <strong>{codeName}</strong>. If you close this window, the code still works until it expires. The channel shows up in the list after a reload.
        </p>
      )}
      {waiting && renamed && (
        <div>
          <button type="button" class="btn btn-sm" onClick={start} disabled={busy}>
            {busy ? 'Getting a code…' : 'Get a new code with this name'}
          </button>
        </div>
      )}
      {(state === 'expired' || state === 'failed' || gone) && (
        <div>
          <button type="button" class="btn btn-sm btn-primary" onClick={start} disabled={busy}>
            {busy ? 'Getting a code…' : 'Get a new code'}
          </button>
        </div>
      )}
      <div class="field-error" role="alert">{error}</div>
    </div>
  );
}

/* TelegramChannelSection is the Telegram part of the channel form. */
export function TelegramChannelSection({ channel, isNew, name, canManage, botUsername, onLinked, onNeedName, startRef }) {
  const [relink, setRelink] = useState(false);
  if (isNew) {
    return (
      <div class="form-group">
        <span class="telegram-link-heading">Telegram chat</span>
        <TelegramLinkPanel name={name} botUsername={botUsername} onLinked={onLinked} onNeedName={onNeedName}
          startLabel="Get link code" startRef={startRef} />
        <p class="form-help">The channel is added when the chat sends the code. Only people in that chat see the alerts and can use the Ack and Resolve buttons.</p>
      </div>
    );
  }
  if (!channel) return null;
  if (usesOwnBot(channel.config)) {
    return (
      <div class="form-group">
        <span class="telegram-link-heading">Telegram chat</span>
        <p class="form-help">
          This channel posts through its own bot, set up with the API. Chat: {telegramChatLabel(channel.config) || 'none'}.
        </p>
      </div>
    );
  }
  const unlinked = isTelegramUnlinked(channel);
  return (
    <div class="form-group">
      <span class="telegram-link-heading">Telegram chat</span>
      {unlinked ? (
        <p class="telegram-link-notice" role="note">
          <strong>No chat is linked.</strong> This channel gets no alerts until a chat sends its link code.
        </p>
      ) : (
        <p class="telegram-link-current">Linked to <strong>{telegramChatLabel(channel.config)}</strong></p>
      )}
      {canManage && unlinked ? (
        <TelegramLinkPanel channelId={channel.id} botUsername={botUsername} onLinked={onLinked} />
      ) : canManage && relink ? (
        <>
          <p class="form-help">
            Alerts move to the new chat once it sends the code. Until then, <strong>{telegramChatLabel(channel.config)}</strong> keeps getting them.
          </p>
          <TelegramLinkPanel channelId={channel.id} botUsername={botUsername} onLinked={onLinked}
            onCancel={() => setRelink(false)} />
        </>
      ) : canManage ? (
        <button type="button" class="btn btn-sm" onClick={() => setRelink(true)}>
          Link a different chat
        </button>
      ) : null}
    </div>
  );
}
