import { useState, useEffect, useRef } from 'preact/hooks';
import { appMeta } from '../state/auth';
import { post } from '../api/client';
import { loadSupportImpacts, rateLimitText, fieldErrorOf, charCount } from '../api/support';
import { supportFollowUp, supportDrawerRequest } from '../state/support';
import '../styles/support.css';

// Limits match the server (internal/api/handlers/support_intake.go). The
// server is the authority; these only stop the form from sending text it
// would reject.
const SUBJECT_MAX = 200;
const BODY_MAX = 10000;

const FIELD_LABELS = { impact: 'What is affected', subject: 'Subject', body: 'Description' };

const FOCUSABLE = 'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])';

export function SupportFAB() {
  const [open, setOpen] = useState(false);
  const [impact, setImpact] = useState('');
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [created, setCreated] = useState(null);
  const [error, setError] = useState('');
  const [fieldErrors, setFieldErrors] = useState({});
  const [impacts, setImpacts] = useState(null);
  const [impactsError, setImpactsError] = useState(false);
  // The earlier ticket this one follows up on: { id, subject } or null.
  const [related, setRelated] = useState(null);
  const fabRef = useRef(null);
  const drawerRef = useRef(null);
  const subjectRef = useRef(null);
  const bodyRef = useRef(null);
  const impactRef = useRef(null);
  // Where focus goes when the drawer closes: the control that opened it
  // (for example the Support page "New ticket" button), else the Help button.
  const returnFocusRef = useRef(null);

  async function fetchImpacts() {
    setImpactsError(false);
    try {
      setImpacts(await loadSupportImpacts());
    } catch {
      setImpactsError(true);
    }
  }

  // While open: close on Escape, keep Tab inside the dialog, and return
  // focus to the Help button on close.
  useEffect(() => {
    if (!open) return;
    function onKeyDown(e) {
      if (e.key === 'Escape') {
        e.preventDefault();
        setOpen(false);
        return;
      }
      if (e.key !== 'Tab' || !drawerRef.current) return;
      const items = [...drawerRef.current.querySelectorAll(FOCUSABLE)]
        .filter(el => el.offsetParent !== null);
      if (items.length === 0) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && (document.activeElement === first || document.activeElement === drawerRef.current)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
    document.addEventListener('keydown', onKeyDown);
    drawerRef.current?.focus();
    const fab = fabRef.current;
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      const back = returnFocusRef.current;
      returnFocusRef.current = null;
      if (back && back.isConnected) back.focus();
      else fab?.focus();
    };
  }, [open]);

  // A page asked for a follow-up to a closed ticket. Link it and fill in
  // the subject and description only where the customer has not typed yet,
  // so an unsent draft is never thrown away.
  const followUp = supportFollowUp.value;
  useEffect(() => {
    if (!followUp) return;
    supportFollowUp.value = null;
    returnFocusRef.current = followUp.returnFocus || null;
    const fresh = !!created;
    if (fresh) resetForm();
    setRelated({ id: followUp.relatedTicketId, subject: followUp.relatedSubject || '' });
    if (followUp.relatedSubject && (fresh || !subject.trim())) {
      setSubject(`Follow-up: ${followUp.relatedSubject}`.slice(0, SUBJECT_MAX));
    }
    if (followUp.body && (fresh || !body.trim())) {
      setBody(followUp.body.slice(0, BODY_MAX));
    }
    setError('');
    setFieldErrors({});
    if (!impacts) fetchImpacts();
    setOpen(true);
  }, [followUp]);

  // A page asked to open the drawer for a new ticket.
  const drawerRequest = supportDrawerRequest.value;
  useEffect(() => {
    if (!drawerRequest) return;
    supportDrawerRequest.value = null;
    if (open) {
      drawerRef.current?.focus();
      return;
    }
    returnFocusRef.current = drawerRequest.returnFocus || null;
    handleOpen();
  }, [drawerRequest]);

  const isFOSS = appMeta.value?.edition === 'foss';
  if (isFOSS) return null;

  function resetForm() {
    setImpact('');
    setSubject('');
    setBody('');
    setError('');
    setFieldErrors({});
    setRelated(null);
    setCreated(null);
  }

  function openFromFab() {
    returnFocusRef.current = null;
    handleOpen();
  }

  function handleOpen() {
    // Keep an unsent draft if the drawer was closed by accident. Start fresh
    // only after a ticket went through.
    if (created) resetForm();
    if (!impacts) fetchImpacts();
    setOpen(true);
  }

  function clearFieldError(field) {
    if (fieldErrors[field]) setFieldErrors(prev => ({ ...prev, [field]: '' }));
  }

  function focusField(field) {
    const el = field === 'impact'
      ? impactRef.current?.querySelector('input:checked, input')
      : field === 'subject' ? subjectRef.current : bodyRef.current;
    el?.focus();
  }

  function validate() {
    const errs = {};
    if (impacts && impacts.length > 0 && !impact) errs.impact = 'Pick the option that fits best.';
    if (!subject.trim()) errs.subject = 'Add a short summary.';
    if (!body.trim()) errs.body = 'Describe what is happening.';
    return errs;
  }

  async function handleSubmit(e) {
    e.preventDefault();
    const errs = validate();
    setFieldErrors(errs);
    setError('');
    const firstBad = ['impact', 'subject', 'body'].find(f => errs[f]);
    if (firstBad) {
      focusField(firstBad);
      return;
    }

    setSubmitting(true);

    const context = { page: window.location.pathname, plan: appMeta.value?.plan };
    const monitorMatch = window.location.pathname.match(/\/monitors\/([^/]+)/);
    if (monitorMatch) context.monitor_id = monitorMatch[1];
    const alertMatch = window.location.pathname.match(/\/alerts\/([^/]+)/);
    if (alertMatch) context.alert_id = alertMatch[1];

    const payload = { subject: subject.trim(), body: body.trim(), context };
    // If the options failed to load, send without an impact rather than
    // block the customer; the server files it at normal priority.
    if (impact) payload.impact = impact;
    if (related) payload.related_ticket_id = related.id;

    try {
      let ticket;
      try {
        ticket = await post('/support/tickets', payload);
      } catch (err) {
        // The page details are only a help for staff. If the server refuses
        // them, send the ticket without them instead of showing the customer
        // an error about data they never typed.
        if (fieldErrorOf(err)?.field !== 'context') throw err;
        delete payload.context;
        ticket = await post('/support/tickets', payload);
      }
      setCreated(ticket || {});
    } catch (err) {
      const fe = fieldErrorOf(err);
      if (err?.status === 429) {
        setError(rateLimitText(err, 'ticket'));
      } else if (fe?.field === 'related_ticket_id') {
        // The earlier ticket was deleted or the caller lost access to it.
        // Drop the link and let them send without it.
        setRelated(null);
        setError('The earlier ticket could not be linked, so the link was removed. Send again to open this ticket on its own.');
      } else {
        if (fe && FIELD_LABELS[fe.field]) {
          if (fe.field === 'impact') {
            // The option list changed under us; reload it.
            setFieldErrors({ impact: 'That option is no longer available. Pick another one.' });
            setImpact('');
            fetchImpacts();
          } else {
            setFieldErrors({ [fe.field]: `${FIELD_LABELS[fe.field]} ${fe.message}.` });
          }
          focusField(fe.field);
        } else {
          setError(err?.readonly ? err.message : (err?.message || 'Your ticket was not sent. Try again.'));
        }
      }
    } finally {
      setSubmitting(false);
    }
  }

  const describe = (...ids) => ids.filter(Boolean).join(' ') || undefined;

  return (
    <>
      <button
        ref={fabRef}
        type="button"
        class="support-fab"
        onClick={openFromFab}
        aria-haspopup="dialog"
        aria-expanded={open ? 'true' : 'false'}
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" aria-hidden="true" focusable="false">
          <circle cx="12" cy="12" r="10"/>
          <path d="M9.09 9a3 3 0 015.83 1c0 2-3 3-3 3"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        <span>Help</span>
      </button>

      {open && (
        <>
          <div class="support-overlay" onClick={() => setOpen(false)} />
          <div
            ref={drawerRef}
            class="support-drawer"
            role="dialog"
            aria-modal="true"
            aria-labelledby="support-fab-title"
            tabIndex={-1}
          >
            <div class="support-drawer-header">
              <h2 id="support-fab-title">Contact support</h2>
              <button type="button" class="support-drawer-close" onClick={() => setOpen(false)} aria-label="Close">
                <span aria-hidden="true">&times;</span>
              </button>
            </div>

            <div class="support-drawer-body">
              {created ? (
                <div class="support-success" role="status">
                  <div class="support-success-mark" aria-hidden="true">&#10003;</div>
                  <h3>Ticket sent</h3>
                  <p>
                    We will reply on the ticket and by email.
                    {created.related_ticket_id ? ' Support can see the earlier ticket it follows up on.' : ''}
                  </p>
                  <div class="support-success-actions">
                    {created.id ? (
                      <a href={`/support/${encodeURIComponent(created.id)}`} class="btn btn-primary" onClick={() => setOpen(false)}>
                        View ticket
                      </a>
                    ) : null}
                    <a href="/support" class="btn" onClick={() => setOpen(false)}>All tickets</a>
                  </div>
                </div>
              ) : (
                <form onSubmit={handleSubmit} noValidate>
                  {error && <div class="form-error" role="alert">{error}</div>}

                  {related && (
                    <div class="support-related">
                      <p>
                        <span class="support-related-label">Follow-up to </span>
                        <a href={`/support/${encodeURIComponent(related.id)}`} onClick={() => setOpen(false)}>
                          {related.subject || 'an earlier ticket'}
                        </a>
                      </p>
                      <button type="button" class="btn btn-sm" onClick={() => setRelated(null)}>
                        Remove link
                      </button>
                    </div>
                  )}

                  <fieldset
                    ref={impactRef}
                    class={`support-impact ${fieldErrors.impact ? 'has-error' : ''}`}
                    aria-describedby={describe('support-impact-help', fieldErrors.impact && 'support-impact-error')}
                  >
                    <legend>What is affected?</legend>
                    <p id="support-impact-help" class="support-impact-help">
                      This tells us how fast to respond.
                    </p>
                    {impacts === null && !impactsError && (
                      <p class="support-loading-line" role="status">Loading options...</p>
                    )}
                    {impactsError && (
                      <div class="support-loading-line">
                        <p style={{ margin: '0 0 8px' }}>
                          The options did not load. You can still send the ticket, or try loading them again.
                        </p>
                        <button type="button" class="btn btn-sm" onClick={fetchImpacts}>Try again</button>
                      </div>
                    )}
                    {impacts && impacts.length > 0 && (
                      <div class="support-impact-list">
                        {impacts.map(opt => {
                          const id = `support-impact-${opt.value}`;
                          return (
                            <label key={opt.value} class="support-impact-option" for={id}>
                              <input
                                id={id}
                                type="radio"
                                name="support-impact"
                                value={opt.value}
                                checked={impact === opt.value}
                                onChange={() => { setImpact(opt.value); clearFieldError('impact'); }}
                                aria-describedby={opt.hint ? `${id}-hint` : undefined}
                                required
                              />
                              <span>
                                <span class="support-impact-label">{opt.label}</span>
                                {opt.hint && <span id={`${id}-hint`} class="support-impact-hint">{opt.hint}</span>}
                              </span>
                            </label>
                          );
                        })}
                      </div>
                    )}
                    {fieldErrors.impact && (
                      <div id="support-impact-error" class="support-field-error">{fieldErrors.impact}</div>
                    )}
                  </fieldset>

                  <div class={`form-group ${fieldErrors.subject ? 'has-error' : ''}`}>
                    <label for="support-subject">Subject</label>
                    <input
                      id="support-subject"
                      ref={subjectRef}
                      type="text"
                      value={subject}
                      maxLength={SUBJECT_MAX}
                      autoComplete="off"
                      onInput={e => { setSubject(e.target.value); clearFieldError('subject'); }}
                      placeholder="For example: SMS alerts stopped this morning"
                      aria-invalid={fieldErrors.subject ? 'true' : undefined}
                      aria-describedby={describe(fieldErrors.subject && 'support-subject-error', 'support-subject-count')}
                      required
                    />
                    <div id="support-subject-count" class="support-field-hint">{charCount(subject, SUBJECT_MAX)}</div>
                    {fieldErrors.subject && (
                      <div id="support-subject-error" class="support-field-error">{fieldErrors.subject}</div>
                    )}
                  </div>

                  <div class={`form-group ${fieldErrors.body ? 'has-error' : ''}`}>
                    <label for="support-body">Description</label>
                    <textarea
                      id="support-body"
                      ref={bodyRef}
                      rows="6"
                      value={body}
                      maxLength={BODY_MAX}
                      onInput={e => { setBody(e.target.value); clearFieldError('body'); }}
                      placeholder="What happened, when it started, and which monitors or channels are involved."
                      aria-invalid={fieldErrors.body ? 'true' : undefined}
                      aria-describedby={describe(fieldErrors.body && 'support-body-error', 'support-body-count')}
                      required
                    />
                    <div id="support-body-count" class="support-field-hint">{charCount(body, BODY_MAX)}</div>
                    {fieldErrors.body && (
                      <div id="support-body-error" class="support-field-error">{fieldErrors.body}</div>
                    )}
                  </div>

                  <p class="support-note">
                    Your org's owners and admins can also see this ticket, plus any roles
                    they have given support access in Settings.
                  </p>

                  <button
                    type="submit"
                    class="btn btn-primary"
                    disabled={submitting}
                    style={{ width: '100%', minHeight: 44 }}
                  >
                    {submitting ? 'Sending...' : 'Send ticket'}
                  </button>
                </form>
              )}
            </div>
          </div>
        </>
      )}
    </>
  );
}
