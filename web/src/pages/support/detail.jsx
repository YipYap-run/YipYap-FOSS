import { useState, useEffect, useLayoutEffect, useRef } from 'preact/hooks';
import { get, post, patch } from '../../api/client';
import { openerName, FORMER_MEMBER } from './people';
import { loadSupportImpacts, impactLabel, rateLimitText, fieldErrorOf, charCount } from '../../api/support';
import {
  openSupportFollowUp, reopenDeadline, reopenWindowPassed, deadlineText,
  REOPEN_DAYS, TICKET_POLL_MS, TICKET_POLL_CLOSED_MS,
} from '../../state/support';
import { RATINGS, RATING_COMMENT_MAX } from './ratings';
import { PageHeader, Card, LoadingPage, ErrorMessage, EmptyState, relativeTime, formatTime } from '../../components/ui';
import { StatusPill } from './pills';

// Matches SupportBodyMaxChars on the server (support_intake.go).
const REPLY_MAX = 10000;

// A friendlier message for the errors the ticket endpoints return. The server
// answers 404 for tickets the caller cannot see, so "not found" and "no
// access" read the same on purpose.
function ticketErrorText(err, fallback) {
  if (err?.readonly) return err.message;
  if (err?.status === 404) return 'This ticket does not exist or you do not have access to it.';
  if (err?.status === 403) return err.message || 'You do not have permission to do that on this ticket.';
  return err?.message || fallback;
}

function Timestamp({ ts }) {
  if (!ts) return null;
  return <time dateTime={ts} title={formatTime(ts)}>{relativeTime(ts)}</time>;
}

// Name shown on a message. "You" only for the caller's own messages; the
// server sends teammates by name and staff as "Sam, YipYap Support".
function authorOf(msg) {
  if (msg.sender_deleted) return 'Deleted user';
  if (msg.is_you) return 'You';
  if (msg.author_display) return msg.author_display;
  return msg.sender_type === 'staff' ? 'YipYap Support' : FORMER_MEMBER;
}

// Text announced when a poll finds staff replies or a status change that the
// page did not show yet.
function pollAnnouncement(prev, next) {
  if (!prev || !next) return '';
  const seen = new Set((prev.messages || []).map(m => m.id));
  const fresh = (next.messages || []).filter(m => !seen.has(m.id) && m.sender_type === 'staff');
  const parts = [];
  if (fresh.length === 1) parts.push(`New reply from ${authorOf(fresh[0])}.`);
  else if (fresh.length > 1) parts.push(`${fresh.length} new replies from support.`);
  if (prev.status !== next.status && next.status === 'closed') parts.push('This ticket was closed.');
  return parts.join(' ');
}

export function SupportDetailPage({ id }) {
  const [ticket, setTicket] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [replyBody, setReplyBody] = useState('');
  const [replyError, setReplyError] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [closing, setClosing] = useState(false);
  const [closeError, setCloseError] = useState('');
  const [confirmingClose, setConfirmingClose] = useState(false);
  const [csatRating, setCsatRating] = useState(0);
  const [csatComment, setCsatComment] = useState('');
  const [csatError, setCsatError] = useState('');
  const [csatSubmitting, setCsatSubmitting] = useState(false);
  // True while the opener is changing a rating they already gave.
  const [csatEditing, setCsatEditing] = useState(false);
  const [csatStatus, setCsatStatus] = useState('');
  const csatScaleRef = useRef(null);
  const csatChangeRef = useRef(null);
  const closeBtnRef = useRef(null);
  const confirmRef = useRef(null);
  const closedNoticeRef = useRef(null);
  const [impacts, setImpacts] = useState(null);
  // Announced after a reply, a close, or a poll that found new replies.
  const [liveStatus, setLiveStatus] = useState('');
  // Set when the server refused a reply because the reopen window passed;
  // the unsent text stays in replyBody so it can move to a follow-up.
  const [replyRefused, setReplyRefused] = useState(false);
  // Latest ticket and "a write is in flight", for the poller.
  const ticketRef = useRef(null);
  const busyRef = useRef(false);
  ticketRef.current = ticket;
  busyRef.current = submitting || closing || csatSubmitting;

  // Focus to move once the element exists. A setTimeout alone can run
  // before Preact renders the new state (the confirm section, the Close
  // button coming back), which drops focus to the page body. The layout effect
  // below runs after every render and retries until the element is there.
  // A request older than two seconds is dropped so a late render (a poll)
  // cannot pull focus away from where the user has moved on to.
  const pendingFocus = useRef(null);
  function focusAfterRender(getEl) {
    pendingFocus.current = { getEl, at: Date.now() };
    setTimeout(runPendingFocus, 0);
  }
  function runPendingFocus() {
    const req = pendingFocus.current;
    if (!req) return;
    if (Date.now() - req.at > 2000) {
      pendingFocus.current = null;
      return;
    }
    const el = req.getEl();
    if (el && el.isConnected) {
      pendingFocus.current = null;
      el.focus();
    }
  }
  useLayoutEffect(runPendingFocus);

  async function load({ quiet = false } = {}) {
    if (!quiet) setLoading(true);
    setError(null);
    try {
      const data = await get(`/support/tickets/${id}`);
      setTicket(data);
    } catch (err) {
      setError(err);
    } finally {
      if (!quiet) setLoading(false);
    }
  }

  useEffect(() => {
    setLiveStatus('');
    setReplyRefused(false);
    setReplyBody('');
    setConfirmingClose(false);
    setCsatEditing(false);
    setCsatStatus('');
    setCsatRating(0);
    setCsatComment('');
    load();
  }, [id]);

  // Poll for staff replies while the page is visible. A closed ticket can
  // still change (reopened by a colleague, rated), so it is checked less
  // often rather than not at all. Drafts are kept: only the ticket is
  // replaced.
  const polledStatus = ticket?.status;
  useEffect(() => {
    if (!polledStatus) return undefined;
    let stopped = false;
    async function poll() {
      if (stopped || busyRef.current || document.visibilityState !== 'visible') return;
      try {
        const data = await get(`/support/tickets/${id}`);
        if (stopped) return;
        const note = pollAnnouncement(ticketRef.current, data);
        if (note) setLiveStatus(note);
        setTicket(data);
      } catch (err) {
        // Access removed or ticket deleted: show that. Anything else is
        // likely a network blip; the next poll retries.
        if (!stopped && err?.status === 404) setError(err);
      }
    }
    const timer = setInterval(poll, polledStatus === 'closed' ? TICKET_POLL_CLOSED_MS : TICKET_POLL_MS);
    function onVisible() {
      if (document.visibilityState === 'visible') poll();
    }
    document.addEventListener('visibilitychange', onVisible);
    return () => {
      stopped = true;
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
    };
  }, [id, polledStatus]);

  // Labels for the impact the opener picked. Without them the raw value is
  // shown in readable form, so a failed request needs no error state.
  useEffect(() => {
    if (!ticket?.impact || impacts) return;
    loadSupportImpacts().then(setImpacts).catch(() => {});
  }, [ticket?.impact]);

  async function sendReply(e) {
    e.preventDefault();
    const text = replyBody.trim();
    if (!text) {
      setReplyError('Write a message before sending.');
      return;
    }
    const wasClosed = ticket?.status === 'closed';
    setSubmitting(true);
    setReplyError('');
    setLiveStatus('');
    try {
      await post(`/support/tickets/${id}/messages`, { body: text });
      setReplyBody('');
      setLiveStatus(wasClosed ? 'Ticket reopened. Support has been told about your reply.' : 'Reply sent.');
      await load({ quiet: true });
    } catch (err) {
      const fe = fieldErrorOf(err);
      if (err?.status === 409 && err?.body?.code === 'ticket_closed_new_required') {
        // The window ended while the page was open. Keep the text for a
        // follow-up ticket and refresh so the page shows the closed state.
        setReplyRefused(true);
        load({ quiet: true });
      } else if (err?.status === 429) {
        setReplyError(rateLimitText(err, 'message'));
      } else if (fe?.field === 'body') {
        setReplyError(`Your reply ${fe.message}.`);
      } else {
        setReplyError(ticketErrorText(err, 'Your reply was not sent. Try again.'));
        // Access or status may have changed under us; refresh what we show.
        if (err?.status === 403 || err?.status === 400) load({ quiet: true });
      }
    } finally {
      setSubmitting(false);
    }
  }

  function askClose() {
    setCloseError('');
    setConfirmingClose(true);
    focusAfterRender(() => confirmRef.current);
  }

  function cancelClose() {
    setConfirmingClose(false);
    focusAfterRender(() => closeBtnRef.current);
  }

  async function closeTicket() {
    setClosing(true);
    setCloseError('');
    try {
      await patch(`/support/tickets/${id}`, { status: 'closed' });
      setConfirmingClose(false);
      setLiveStatus('Ticket closed.');
      await load({ quiet: true });
      // The Close button is gone; move focus to the notice that says what
      // happens next.
      focusAfterRender(() => closedNoticeRef.current);
    } catch (err) {
      setCloseError(ticketErrorText(err, 'The ticket was not closed. Try again.'));
    } finally {
      setClosing(false);
    }
  }

  function csatScaleInput() {
    const root = csatScaleRef.current;
    return root?.querySelector('input:checked') || root?.querySelector('input') || null;
  }

  function focusCsatScale() {
    csatScaleInput()?.focus();
  }

  function startCsatEdit() {
    setCsatRating(ticket?.csat_rating || 0);
    setCsatComment(ticket?.csat_comment || '');
    setCsatError('');
    setCsatStatus('');
    setCsatEditing(true);
    focusAfterRender(csatScaleInput);
  }

  function cancelCsatEdit() {
    setCsatEditing(false);
    setCsatError('');
    focusAfterRender(() => csatChangeRef.current);
  }

  async function handleCSAT(e) {
    e.preventDefault();
    if (!csatRating) {
      setCsatError('Pick a rating from 1 to 5.');
      focusCsatScale();
      return;
    }
    const rated = csatRating;
    setCsatSubmitting(true);
    setCsatError('');
    try {
      // Always send the comment: an empty one clears an earlier comment when
      // the rating is changed.
      await post(`/support/tickets/${id}/feedback`, { rating: rated, comment: csatComment.trim() });
      await load({ quiet: true });
      setCsatEditing(false);
      setCsatStatus(`Thanks, you rated this ${rated}/5.`);
      // The form closes, so return focus to the control that reopens it.
      focusAfterRender(() => csatChangeRef.current);
    } catch (err) {
      setCsatError(ticketErrorText(err, 'Your rating was not saved. Try again.'));
    } finally {
      setCsatSubmitting(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) {
    if (error.status === 404) {
      return (
        <EmptyState
          title="Ticket not available"
          description={ticketErrorText(error)}
          action={<a href="/support" class="btn btn-primary">Back to tickets</a>}
        />
      );
    }
    return <ErrorMessage error={error} onRetry={() => load()} />;
  }
  if (!ticket) return <ErrorMessage error="Ticket not found" />;

  const messages = ticket.messages || [];
  const isClosed = ticket.status === 'closed';
  const perms = ticket.permissions || {};
  const opener = openerName(ticket, { sentence: true });
  const openerTitle = openerName(ticket);
  const impactText = impactLabel(impacts, ticket.impact);
  const deadline = reopenDeadline(ticket);
  const windowPassed = reopenWindowPassed(ticket);
  const reopening = isClosed && perms.can_reply;

  function startFollowUp(e) {
    openSupportFollowUp({
      relatedTicketId: ticket.id,
      relatedSubject: ticket.subject,
      body: replyBody.trim(),
      returnFocus: e?.currentTarget || null,
    });
  }

  // What a closed ticket allows, and how to continue the conversation.
  let closedText = '';
  let offerFollowUp = false;
  if (isClosed && replyRefused) {
    closedText = 'This ticket can no longer be reopened, so your reply was not sent. ' +
      'Start a new ticket instead. Your text is copied over and the new ticket links back to this one.';
    offerFollowUp = true;
  } else if (isClosed && windowPassed) {
    const when = ticket.closed_at ? ` on ${deadlineText(new Date(ticket.closed_at))}` : '';
    closedText = `This ticket was closed${when}. Tickets can be reopened by reply for ${REOPEN_DAYS} days after closing. ` +
      'For more help, start a new ticket. It links back to this one.';
    offerFollowUp = true;
  } else if (isClosed && reopening) {
    closedText = `This ticket is closed. Replying reopens this ticket. You can reply until ${deadlineText(deadline)}.`;
  } else if (isClosed) {
    const who = openerTitle === FORMER_MEMBER ? 'Owners and admins' : `${openerTitle}, owners and admins`;
    closedText = `This ticket is closed. ${who} can reopen it by replying before ` +
      `${deadlineText(deadline)}. You can start a new ticket that links back to this one.`;
    offerFollowUp = true;
  }

  // Explain why controls are missing for someone viewing a colleague's ticket.
  let accessNotice = '';
  if (!ticket.is_mine && !isClosed) {
    if (!perms.can_reply) {
      accessNotice = `You can read this ticket. Only ${opener}, owners and admins can reply or close it.`;
    } else if (!perms.can_close) {
      accessNotice = `You can read and reply to this ticket. Only ${opener}, owners and admins can close it.`;
    }
  }

  const ratedText = ticket.csat_rating != null
    ? (ticket.is_mine ? `You rated this ${ticket.csat_rating}/5.` : `${openerTitle} rated this ${ticket.csat_rating}/5.`)
    : '';

  return (
    <div class="support-detail">
      <PageHeader
        title={ticket.subject}
        subtitle={
          <span class="support-meta-row">
            <StatusPill value={ticket.status} />
            <span>Opened by {opener} <Timestamp ts={ticket.created_at} /></span>
          </span>
        }
        actions={
          <div class="support-header-actions">
            {perms.can_close && !confirmingClose && (
              <button ref={closeBtnRef} type="button" class="btn btn-sm" onClick={askClose}>
                Close ticket
              </button>
            )}
            <a href="/support" class="btn btn-sm">All tickets</a>
          </div>
        }
      />

      {confirmingClose && perms.can_close && (
        <section
          ref={confirmRef}
          class="support-confirm"
          tabIndex={-1}
          aria-labelledby="support-confirm-title"
          aria-describedby="support-confirm-text"
          onKeyDown={e => { if (e.key === 'Escape') { e.preventDefault(); cancelClose(); } }}
        >
          <h3 id="support-confirm-title">Close this ticket?</h3>
          <p id="support-confirm-text">
            Support will treat the issue as resolved. You can reopen it by replying within {REOPEN_DAYS} days.
          </p>
          {closeError && <div class="form-error" role="alert">{closeError}</div>}
          <div class="support-confirm-actions">
            <button type="button" class="btn btn-primary" onClick={closeTicket} disabled={closing}>
              {closing ? 'Closing...' : 'Close ticket'}
            </button>
            <button type="button" class="btn" onClick={cancelClose} disabled={closing}>
              Keep it open
            </button>
          </div>
        </section>
      )}

      {accessNotice && <p class="support-access-notice">{accessNotice}</p>}

      {ticket.related_ticket_id && (
        <p class="support-meta-line">
          <span class="support-meta-label">Follows up on </span>
          <a href={`/support/${encodeURIComponent(ticket.related_ticket_id)}`}>an earlier ticket</a>
        </p>
      )}

      {impactText && (
        <p class="support-meta-line">
          <span class="support-meta-label">What is affected: </span>{impactText}
        </p>
      )}

      <Card class="support-section">
        <h3 id="support-conversation" class="support-section-title">Conversation</h3>

        {messages.length === 0 ? (
          <p class="support-muted">No messages yet.</p>
        ) : (
          <ol aria-labelledby="support-conversation" class="support-thread">
            {messages.map(msg => {
              const isSystem = msg.sender_type === 'system';
              const isStaff = msg.sender_type === 'staff';
              const kind = isSystem ? 'system' : isStaff ? 'staff' : msg.is_you ? 'own' : 'user';

              return (
                <li key={msg.id} class={`support-msg support-msg-${kind}`}>
                  {!isSystem && (
                    <div class="support-msg-meta">
                      <span class="support-msg-author">{authorOf(msg)}</span>
                      <span class="support-msg-time">
                        <Timestamp ts={msg.created_at} />
                      </span>
                    </div>
                  )}
                  <div class="support-msg-body">{msg.body}</div>
                  {isSystem && (
                    <div class="support-msg-time">
                      <Timestamp ts={msg.created_at} />
                    </div>
                  )}
                </li>
              );
            })}
          </ol>
        )}
      </Card>

      <div role="status" aria-live="polite">
        {liveStatus && <p class="support-closed-notice"><span>{liveStatus}</span></p>}
      </div>

      {closedText && (
        <div
          ref={closedNoticeRef}
          tabIndex={-1}
          class="support-closed-notice"
          role={replyRefused ? 'alert' : undefined}
        >
          <p>{closedText}</p>
          {offerFollowUp && (
            <button type="button" class="btn btn-primary btn-sm" onClick={startFollowUp} aria-haspopup="dialog">
              Start a new ticket
            </button>
          )}
        </div>
      )}

      {perms.can_reply && (
        <Card class="support-section">
          <form onSubmit={sendReply} noValidate>
            <div class={`form-group ${replyError ? 'has-error' : ''}`}>
              <label for="support-reply" class="support-field-title">
                Reply
              </label>
              {reopening && (
                <p id="support-reply-reopen" class="support-reopen-hint">Replying reopens this ticket.</p>
              )}
              <textarea
                id="support-reply"
                rows="4"
                value={replyBody}
                maxLength={REPLY_MAX}
                onInput={e => { setReplyBody(e.target.value); if (replyError) setReplyError(''); }}
                placeholder="Type your reply"
                aria-invalid={replyError ? 'true' : undefined}
                aria-describedby={[reopening && 'support-reply-reopen', replyError && 'support-reply-error', 'support-reply-count'].filter(Boolean).join(' ')}
                required
              />
              <div id="support-reply-count" class="support-field-hint">{charCount(replyBody, REPLY_MAX)}</div>
            </div>
            {replyError && (
              <div id="support-reply-error" class="form-error" role="alert">{replyError}</div>
            )}
            <div class="support-reply-actions">
              <button type="submit" class="btn btn-primary" disabled={submitting}>
                {submitting ? 'Sending...' : reopening ? 'Send and reopen' : 'Send reply'}
              </button>
            </div>
          </form>
        </Card>
      )}

      {isClosed && ticket.csat_rating != null && !csatEditing && (
        <Card>
          <h3 class="support-card-title">Rating</h3>
          <p id="support-csat-rated" role="status" aria-live="polite" class="support-rated">
            {csatStatus || ratedText}
          </p>
          {ticket.csat_comment && (
            <p class="support-rated-comment">
              {ticket.csat_comment}
            </p>
          )}
          {perms.can_rate && (
            <button
              type="button"
              ref={csatChangeRef}
              aria-describedby="support-csat-rated"
              class="btn support-rate-change"
              onClick={startCsatEdit}
            >
              Change rating or comment
            </button>
          )}
        </Card>
      )}

      {perms.can_rate && (ticket.csat_rating == null || csatEditing) && (
        <Card>
          <form onSubmit={handleCSAT} noValidate>
            <fieldset
              aria-describedby={['support-csat-scale', csatError && 'support-csat-error'].filter(Boolean).join(' ')}
              class="support-rating-fieldset"
            >
              <legend class="support-field-title">
                {csatEditing ? 'Change your rating' : 'How was your experience?'}
              </legend>
              <p id="support-csat-scale" class="support-rating-help">
                1 is bad, 5 is great.
              </p>
              <div ref={csatScaleRef} class="support-rating-scale">
                {RATINGS.map(r => {
                  const rid = `support-csat-${r.value}`;
                  return (
                    <label key={r.value} for={rid} class={`support-rating-option ${csatRating === r.value ? 'selected' : ''}`}>
                      <input
                        id={rid}
                        type="radio"
                        name="support-csat"
                        value={r.value}
                        checked={csatRating === r.value}
                        onChange={() => { setCsatRating(r.value); setCsatError(''); }}
                      />
                      <span class="support-rating-num">{r.value}</span>
                      <span class="support-rating-word">{r.label}</span>
                    </label>
                  );
                })}
              </div>
            </fieldset>
            <div class="form-group support-rating-comment">
              <label for="support-csat-comment">
                Comment <span class="form-optional">(optional)</span>
              </label>
              <textarea
                id="support-csat-comment"
                rows="3"
                maxLength={RATING_COMMENT_MAX}
                value={csatComment}
                aria-describedby="support-csat-count"
                onInput={e => setCsatComment(e.target.value)}
              />
              <div id="support-csat-count" class="support-field-hint">{charCount(csatComment, RATING_COMMENT_MAX)}</div>
            </div>
            {csatError && <div id="support-csat-error" class="form-error" role="alert">{csatError}</div>}
            <div class="support-reply-actions">
              <button type="submit" class="btn btn-primary" disabled={csatSubmitting}>
                {csatSubmitting ? 'Saving...' : csatEditing ? 'Save rating' : 'Send rating'}
              </button>
              {csatEditing && (
                <button type="button" class="btn" onClick={cancelCsatEdit} disabled={csatSubmitting}>
                  Cancel
                </button>
              )}
            </div>
          </form>
        </Card>
      )}
    </div>
  );
}
