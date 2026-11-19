import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { PageHeader, LoadingPage, ErrorMessage, EmptyState, relativeTime, ConfigPill, SUPPORT_STATUS_COLORS, SUPPORT_PRIORITY_COLORS } from '../../components/ui';

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
                <ConfigPill config={SUPPORT_STATUS_COLORS} value={ticket.status} />
                <ConfigPill config={SUPPORT_PRIORITY_COLORS} value={ticket.priority} />
                <span class="alert-card-time">{relativeTime(ticket.updated_at || ticket.created_at)}</span>
              </div>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
