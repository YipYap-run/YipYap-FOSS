import { useState, useEffect } from 'preact/hooks';
import { get, post, patch } from '../../api/client';
import { currentUser } from '../../state/auth';
import { PageHeader, Card, LoadingPage, ErrorMessage, relativeTime } from '../../components/ui';

const STATUS_COLORS = {
  open:    { bg: '#27ae60', label: 'OPEN' },
  pending: { bg: '#f39c12', label: 'PENDING' },
  closed:  { bg: '#6b7280', label: 'CLOSED' },
};

const PRIORITY_COLORS = {
  low:    { bg: '#6b7280', label: 'LOW' },
  normal: { bg: '#3b82f6', label: 'NORMAL' },
  high:   { bg: '#e67e22', label: 'HIGH' },
  urgent: { bg: '#e74c3c', label: 'URGENT' },
};

function Pill({ config, value }) {
  const c = config[value] || { bg: 'var(--color-muted)', label: value?.toUpperCase() || 'UNKNOWN' };
  return (
    <span style={{
      display: 'inline-block',
      padding: '3px 10px',
      borderRadius: 9999,
      background: c.bg,
      color: '#fff',
      fontSize: '0.6875rem',
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.5px',
    }}>{c.label}</span>
  );
}

const messageBubbleBase = {
  padding: '12px 16px',
  borderRadius: 8,
  marginBottom: 12,
  maxWidth: '85%',
};

const userMsgStyle = {
  ...messageBubbleBase,
  background: 'var(--color-bg-secondary, #0f172a)',
  border: '1px solid var(--color-border, #334155)',
  alignSelf: 'flex-start',
};

const staffMsgStyle = {
  ...messageBubbleBase,
  background: 'rgba(59,130,246,0.12)',
  border: '1px solid rgba(59,130,246,0.25)',
  alignSelf: 'flex-end',
};

const systemMsgStyle = {
  ...messageBubbleBase,
  background: 'transparent',
  textAlign: 'center',
  alignSelf: 'center',
  color: 'var(--color-text-muted, #94a3b8)',
  fontStyle: 'italic',
  fontSize: '0.8125rem',
  maxWidth: '100%',
};

export function SupportDetailPage({ id }) {
  const [ticket, setTicket] = useState(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);
  const [replyBody, setReplyBody] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [closing, setClosing] = useState(false);
  const [csatRating, setCsatRating] = useState(0);
  const [csatComment, setCsatComment] = useState('');
  const [csatSubmitting, setCsatSubmitting] = useState(false);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get(`/support/tickets/${id}`);
      setTicket(data);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, [id]);

  async function sendReply(e) {
    e.preventDefault();
    if (!replyBody.trim()) return;
    setSubmitting(true);
    try {
      await post(`/support/tickets/${id}/messages`, { body: replyBody.trim() });
      setReplyBody('');
      load();
    } catch (err) {
      alert(err.message || 'Failed to send reply');
    } finally {
      setSubmitting(false);
    }
  }

  async function closeTicket() {
    if (!confirm('Close this ticket?')) return;
    setClosing(true);
    try {
      await patch(`/support/tickets/${id}`, { status: 'closed' });
      load();
    } catch (err) {
      alert(err.message || 'Failed to close ticket');
    } finally {
      setClosing(false);
    }
  }

  async function handleCSAT() {
    if (!csatRating) return;
    setCsatSubmitting(true);
    try {
      const payload = { rating: csatRating };
      if (csatComment.trim()) payload.comment = csatComment.trim();
      await post(`/support/tickets/${id}/feedback`, payload);
      load();
    } catch (err) {
      alert(err.message || 'Failed to submit feedback');
    } finally {
      setCsatSubmitting(false);
    }
  }

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;
  if (!ticket) return <ErrorMessage error="Ticket not found" />;

  const messages = ticket.messages || [];
  const isClosed = ticket.status === 'closed';

  return (
    <div class="support-detail">
      <PageHeader
        title={ticket.subject}
        subtitle={
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 8 }}>
            <Pill config={STATUS_COLORS} value={ticket.status} />
            <Pill config={PRIORITY_COLORS} value={ticket.priority} />
          </span>
        }
        actions={
          <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            {!isClosed && (
              <button class="btn btn-sm" onClick={closeTicket} disabled={closing}>
                {closing ? 'Closing...' : 'Close Ticket'}
              </button>
            )}
            <a href="/support" class="btn btn-sm">Back</a>
          </div>
        }
      />

      {/* Messages thread */}
      <Card style={{ marginBottom: '1.5rem' }}>
        <h3 style={{
          margin: '0 0 1rem',
          fontSize: '0.9375rem',
          color: 'var(--color-text-muted)',
          textTransform: 'uppercase',
          letterSpacing: '0.5px',
        }}>Conversation</h3>

        {messages.length === 0 ? (
          <p style={{ color: 'var(--color-text-muted, #94a3b8)' }}>No messages yet.</p>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 4 }}>
            {messages.map(msg => {
              const isSystem = msg.sender_type === 'system';
              const isStaff = msg.sender_type === 'staff';
              const style = isSystem ? systemMsgStyle : isStaff ? staffMsgStyle : userMsgStyle;

              return (
                <div key={msg.id} style={style}>
                  {!isSystem && (
                    <div style={{
                      display: 'flex',
                      alignItems: 'center',
                      gap: 8,
                      marginBottom: 6,
                      fontSize: '0.8125rem',
                    }}>
                      <span style={{ fontWeight: 600 }}>
                        {msg.sender_email || (isStaff ? 'Support Staff' : 'You')}
                      </span>
                      <span style={{ color: 'var(--color-text-muted, #94a3b8)', fontSize: '0.75rem' }}>
                        {relativeTime(msg.created_at)}
                      </span>
                    </div>
                  )}
                  <div style={{ whiteSpace: 'pre-wrap', fontSize: '0.875rem', lineHeight: 1.5 }}>
                    {msg.body}
                  </div>
                  {isSystem && (
                    <div style={{ fontSize: '0.75rem', marginTop: 4, color: 'var(--color-text-muted, #64748b)' }}>
                      {relativeTime(msg.created_at)}
                    </div>
                  )}
                </div>
              );
            })}
          </div>
        )}
      </Card>

      {/* Reply form */}
      {!isClosed && (
        <Card>
          <h4 style={{ margin: '0 0 0.75rem', fontSize: '0.9375rem' }}>Reply</h4>
          <form onSubmit={sendReply}>
            <div class="form-group">
              <textarea
                rows="4"
                value={replyBody}
                onInput={e => setReplyBody(e.target.value)}
                placeholder="Type your reply..."
                required
              />
            </div>
            <button type="submit" class="btn btn-primary" disabled={submitting}>
              {submitting ? 'Sending...' : 'Send Reply'}
            </button>
          </form>
        </Card>
      )}

      {isClosed && (
        <div style={{
          textAlign: 'center',
          padding: '20px',
          color: 'var(--color-text-muted, #94a3b8)',
          fontSize: '0.875rem',
        }}>
          This ticket is closed. Create a new ticket if you need further help.
        </div>
      )}

      {isClosed && !ticket.csat_rating && (
        <Card>
          <h4 style={{ margin: '0 0 0.75rem', fontSize: '0.9375rem' }}>How was your experience?</h4>
          <div style={{ display: 'flex', gap: 8, margin: '12px 0' }}>
            {[1, 2, 3, 4, 5].map(n => (
              <button
                key={n}
                onClick={() => setCsatRating(n)}
                class={`btn ${csatRating === n ? 'btn-primary' : ''}`}
                style={{ minWidth: 40 }}
              >
                {n}
              </button>
            ))}
          </div>
          <div class="form-group">
            <textarea
              rows="3"
              value={csatComment}
              onInput={e => setCsatComment(e.target.value)}
              placeholder="Any additional feedback? (optional)"
            />
          </div>
          <button class="btn btn-primary" onClick={handleCSAT} disabled={!csatRating || csatSubmitting}>
            {csatSubmitting ? 'Submitting...' : 'Submit Feedback'}
          </button>
        </Card>
      )}
    </div>
  );
}
