import { useEffect, useState } from 'preact/hooks';
import { get, post } from '../../api/client';

const RATINGS = [
  { value: 1, label: 'Bad' },
  { value: 2, label: 'Poor' },
  { value: 3, label: 'Okay' },
  { value: 4, label: 'Good' },
  { value: 5, label: 'Great' },
];

export function SupportFeedbackPage() {
  const params = new URLSearchParams(window.location.search);
  const token = params.get('token') || '';
  const initialRating = Number(params.get('rating')) || 0;

  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [context, setContext] = useState(null);
  const [rating, setRating] = useState(initialRating >= 1 && initialRating <= 5 ? initialRating : 0);
  const [comment, setComment] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [submitted, setSubmitted] = useState(false);

  useEffect(() => {
    if (!token) {
      setError('This survey link is missing its token.');
      setLoading(false);
      return;
    }
    get(`/support/survey?token=${encodeURIComponent(token)}`, { noRedirect: true })
      .then((data) => {
        setContext(data);
        if (data.submitted) {
          setSubmitted(true);
          setRating(data.rating || 0);
          setComment(data.comment || '');
        }
      })
      .catch((err) => setError(err.message || 'This survey link is invalid or expired.'))
      .finally(() => setLoading(false));
  }, [token]);

  async function submit(e) {
    e.preventDefault();
    if (!rating) {
      setError('Pick a rating first.');
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await post('/support/survey', { token, rating, comment }, { noRedirect: true });
      setSubmitted(true);
    } catch (err) {
      setError(err.message || 'Could not submit feedback.');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div class="survey-page">
      <div class="survey-card">
        <div class="survey-brand">
          <span class="survey-wordmark">YipYap</span>
          <span class="survey-badge">Support</span>
        </div>

        {loading && <p class="survey-muted">Loading...</p>}

        {!loading && error && !submitted && (
          <div class="survey-error">{error}</div>
        )}

        {!loading && context && submitted && (
          <div>
            <h1 class="survey-title">Thanks for the feedback</h1>
            <p class="survey-muted">
              {context.subject ? `Survey for "${context.subject}".` : 'Your response has been recorded.'}
            </p>
            {rating > 0 && (
              <div class="survey-stars" aria-label={`Rated ${rating} out of 5`}>
                {RATINGS.map((r) => (
                  <span key={r.value} class={`survey-star ${r.value <= rating ? 'selected' : ''}`}>★</span>
                ))}
              </div>
            )}
            {comment && <p class="survey-comment">“{comment}”</p>}
          </div>
        )}

        {!loading && context && !submitted && (
          <form onSubmit={submit}>
            <h1 class="survey-title">How did we do?</h1>
            <p class="survey-muted">
              {context.subject ? `Support ticket: ${context.subject}` : 'Rate the support you received.'}
            </p>

            <div class="survey-stars" role="radiogroup" aria-label="Rating">
              {RATINGS.map((r) => (
                <button
                  key={r.value}
                  type="button"
                  class={`survey-star ${rating >= r.value ? 'selected' : ''}`}
                  title={`${r.value} - ${r.label}`}
                  aria-label={`${r.value} ${r.label}`}
                  aria-checked={rating === r.value}
                  role="radio"
                  onClick={() => setRating(r.value)}
                >
                  ★
                </button>
              ))}
            </div>
            <p class="survey-rating-label">{rating ? RATINGS[rating - 1].label : 'Pick a rating'}</p>

            <label class="survey-label" for="survey-comment">Anything else? (optional)</label>
            <textarea
              id="survey-comment"
              rows="4"
              maxlength="2000"
              value={comment}
              onInput={(e) => setComment(e.target.value)}
              placeholder="What worked, what did not..."
            />

            {error && <div class="survey-error">{error}</div>}

            <button type="submit" class="survey-submit" disabled={submitting || !rating}>
              {submitting ? 'Sending...' : 'Submit feedback'}
            </button>
          </form>
        )}

        {!loading && !context && !error && (
          <p class="survey-muted">This survey is not available.</p>
        )}

        <p class="survey-footer">YipYap support · This link is tied to your ticket.</p>
      </div>
    </div>
  );
}
