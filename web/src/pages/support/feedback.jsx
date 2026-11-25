import { useEffect, useRef, useState } from 'preact/hooks';
import { post } from '../../api/client';
import { RATINGS, RATING_COMMENT_MAX as COMMENT_MAX } from './ratings';
import {
  describeSurveyError as describeError,
  endsSurvey,
  readLinkParams,
  runWhenVisible,
  savedRatingNotice,
} from './survey-link.mjs';

// Wait before recording a rating from a numbered email link, so link
// scanners that load the page without a person do not vote. The wait only
// counts while the page is visible (see runWhenVisible).
const RECORD_DELAY_MS = 1500;

function formatDate(value) {
  if (!value) return '';
  const d = new Date(value);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
}

export function SupportFeedbackPage({ id }) {
  const [link] = useState(() => readLinkParams(window.location));
  const token = link.token;

  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [reloadKey, setReloadKey] = useState(0);
  const [context, setContext] = useState(null);
  const [rating, setRating] = useState(link.rating);
  const [savedRating, setSavedRating] = useState(0);
  const [comment, setComment] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [pendingLinkVote, setPendingLinkVote] = useState(false);
  const [formError, setFormError] = useState('');
  const [notice, setNotice] = useState('');
  const timerRef = useRef(null);
  // Bumped by every save, so a slow link-vote response cannot overwrite the
  // result of a Submit the person made after it.
  const saveSeq = useRef(0);

  function cancelLinkVote() {
    if (timerRef.current) {
      timerRef.current();
      timerRef.current = null;
    }
    setPendingLinkVote(false);
  }

  useEffect(() => {
    // The token now lives only in memory. Drop it (and any legacy ?token=)
    // from the address bar so it is not kept in history, bookmarks or
    // copied links.
    try {
      window.history.replaceState(window.history.state, '', window.location.pathname);
    } catch {
      // Ignore: the page still works with the token in the address bar.
    }
    document.title = 'Rate your support | YipYap';
  }, []);

  useEffect(() => {
    if (!token) {
      setLoadError({ kind: 'expired', text: 'This survey link is missing part of its address. Open it again from the email.' });
      setLoading(false);
      return undefined;
    }
    let cancelled = false;
    setLoading(true);
    setLoadError(null);
    post('/support/survey/view', { token }, { noRedirect: true })
      .then((data) => {
        if (cancelled) return;
        setContext(data);
        setComment(data.comment || '');
        if (data.submitted) setSavedRating(data.rating || 0);
        if (link.rating && reloadKey === 0) {
          // A numbered email link: record it after a short delay, unless the
          // person picks something else first.
          setRating(link.rating);
          if (data.submitted && data.rating === link.rating) {
            setNotice(savedRatingNotice(link.rating));
            return;
          }
          setPendingLinkVote(true);
          setNotice(`Saving your rating of ${link.rating} out of 5...`);
          timerRef.current = runWhenVisible(document, RECORD_DELAY_MS, () => {
            timerRef.current = null;
            const seq = ++saveSeq.current;
            const stale = () => cancelled || seq !== saveSeq.current;
            post('/support/survey', { token, rating: link.rating }, { noRedirect: true })
              .then((saved) => {
                if (stale()) return;
                setSavedRating(saved?.rating || link.rating);
                setNotice(savedRatingNotice(saved?.rating || link.rating));
              })
              .catch((err) => {
                if (stale()) return;
                setNotice('');
                const info = describeError(err, 'Your rating was not saved.');
                if (endsSurvey(info)) {
                  setContext(null);
                  setLoadError(info);
                } else {
                  setFormError(`${info.text} Pick a rating and select Submit feedback.`);
                }
              })
              .finally(() => !stale() && setPendingLinkVote(false));
          });
        } else if (data.submitted) {
          setRating(data.rating || 0);
          setNotice(savedRatingNotice(data.rating));
        }
      })
      .catch((err) => !cancelled && setLoadError(describeError(err, 'This survey could not be loaded.')))
      .finally(() => !cancelled && setLoading(false));
    return () => {
      cancelled = true;
      if (timerRef.current) {
        timerRef.current();
        timerRef.current = null;
      }
    };
  }, [token, reloadKey]);

  function pick(value) {
    // A manual choice wins over the rating from the email link.
    if (pendingLinkVote) {
      cancelLinkVote();
      setNotice('');
    }
    setRating(value);
    setFormError('');
  }

  async function submit(e) {
    e.preventDefault();
    if (!rating) {
      setFormError('Pick a rating from 1 to 5.');
      return;
    }
    cancelLinkVote();
    const seq = ++saveSeq.current;
    setSubmitting(true);
    setFormError('');
    setNotice('');
    try {
      const saved = await post('/support/survey', { token, rating, comment: comment.trim() }, { noRedirect: true });
      if (seq !== saveSeq.current) return;
      setSavedRating(saved?.rating || rating);
      setComment(saved?.comment ?? comment.trim());
      setNotice(savedRating ? 'Your feedback is updated. Thank you.' : 'Thanks, your feedback is saved.');
    } catch (err) {
      if (seq !== saveSeq.current) return;
      const info = describeError(err, 'Your feedback was not saved.');
      if (endsSurvey(info)) {
        setContext(null);
        setLoadError(info);
      } else {
        setFormError(info.text);
      }
    } finally {
      setSubmitting(false);
    }
  }

  const ticketId = context?.ticket_id || id;
  const expires = formatDate(context?.expires_at);
  const current = RATINGS.find((r) => r.value === rating);
  const submitLabel = submitting ? 'Saving...' : savedRating ? 'Update feedback' : 'Submit feedback';

  return (
    <main class="survey-page">
      <div class="survey-card">
        <div class="survey-brand">
          <span class="survey-wordmark">YipYap</span>
          <span class="survey-badge">Support</span>
        </div>

        {loading && (
          <p class="survey-muted" role="status">Loading your survey...</p>
        )}

        {!loading && loadError && (
          <div>
            <h1 class="survey-title">
              {loadError.kind === 'reopened' ? 'Survey closed' : endsSurvey(loadError) ? 'Link not valid' : 'Survey not loaded'}
            </h1>
            <div class="survey-error" role="alert">{loadError.text}</div>
            {!endsSurvey(loadError) && token && (
              <button type="button" class="survey-submit" onClick={() => setReloadKey((k) => k + 1)}>
                Try again
              </button>
            )}
            {loadError.kind === 'reopened' && ticketId && (
              <p class="survey-muted"><a href={`/support/${encodeURIComponent(ticketId)}`}>View the ticket</a> (sign in required)</p>
            )}
            {loadError.kind === 'expired' && (
              <p class="survey-muted">
                Survey links work for 30 days. To tell us how we did, <a href="/support">open your support tickets</a> (sign in required).
              </p>
            )}
          </div>
        )}

        {!loading && !loadError && context && (
          <form onSubmit={submit} noValidate>
            <h1 class="survey-title">How did we do?</h1>
            <p class="survey-muted survey-subject">
              {context.subject ? <>Ticket: <strong>{context.subject}</strong></> : 'Rate the support you received.'}
            </p>

            <fieldset class="survey-fieldset" aria-describedby="survey-scale">
              <legend class="survey-label">Your rating</legend>
              <p id="survey-scale" class="survey-hint">1 is bad, 5 is great.</p>
              <div class="survey-stars">
                {RATINGS.map((r) => (
                  <label key={r.value} class={`survey-star ${rating >= r.value ? 'filled' : ''} ${rating === r.value ? 'selected' : ''}`}>
                    <input
                      type="radio"
                      name="survey-rating"
                      class="survey-sr-only"
                      value={r.value}
                      checked={rating === r.value}
                      onChange={() => pick(r.value)}
                    />
                    <span class="survey-star-icon" aria-hidden="true">{rating >= r.value ? '★' : '☆'}</span>
                    <span class="survey-star-num" aria-hidden="true">{r.value}</span>
                    <span class="survey-sr-only">{`${r.value} out of 5, ${r.label}`}</span>
                  </label>
                ))}
              </div>
              <p class="survey-rating-label" aria-hidden="true">{current ? `${current.value} out of 5: ${current.label}` : 'No rating yet'}</p>
            </fieldset>

            <label class="survey-label" for="survey-comment">
              Comment <span class="survey-optional">(optional)</span>
            </label>
            <textarea
              id="survey-comment"
              rows="4"
              maxLength={COMMENT_MAX}
              value={comment}
              aria-describedby="survey-comment-count"
              onInput={(e) => setComment(e.target.value)}
            />
            <p id="survey-comment-count" class="survey-hint survey-count">
              {comment.length} of {COMMENT_MAX} characters
            </p>

            <p class="survey-notice" role="status" aria-live="polite">{notice}</p>
            {formError && <div class="survey-error" role="alert">{formError}</div>}

            <button type="submit" class="survey-submit" disabled={submitting} aria-disabled={submitting ? 'true' : 'false'}>
              {submitLabel}
            </button>
            {expires && (
              <p class="survey-hint survey-expiry">You can change your answer until {expires}.</p>
            )}
          </form>
        )}

        <p class="survey-footer">This page is tied to one support ticket.</p>
      </div>
    </main>
  );
}
