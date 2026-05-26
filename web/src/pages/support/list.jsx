import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, LoadingPage, ErrorMessage, EmptyState, relativeTime } from '../../components/ui';

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

export function SupportListPage() {
  const [tickets, setTickets] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get('/support/tickets');
      setTickets(Array.isArray(data.tickets) ? data.tickets : []);
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  return (
    <div class="support-page">
      <PageHeader
        title="Support Tickets"
        subtitle={`${tickets.length} ticket${tickets.length !== 1 ? 's' : ''}`}
      />

      {tickets.length === 0 ? (
        <EmptyState
          title="No tickets yet"
          description="Use the help button to create one."
        />
      ) : (
        <div class="alert-list">
          {tickets.map(ticket => (
            <a key={ticket.id} href={`/support/${ticket.id}`} class="alert-card">
              <div class="alert-card-left">
                <div class="alert-card-info">
                  <h4 style={{ margin: 0 }}>{ticket.subject}</h4>
                  <p class="alert-card-detail" style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    {ticket.message_count != null && (
                      <span>{ticket.message_count} message{ticket.message_count !== 1 ? 's' : ''}</span>
                    )}
                  </p>
                </div>
              </div>
              <div class="alert-card-right" style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <Pill config={STATUS_COLORS} value={ticket.status} />
                <Pill config={PRIORITY_COLORS} value={ticket.priority} />
                <span class="alert-card-time">{relativeTime(ticket.updated_at || ticket.created_at)}</span>
              </div>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
