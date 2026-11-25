import { useState, useEffect } from 'preact/hooks';
import { get } from '../../api/client';
import { openerName } from './people';
import { openSupportDrawer } from '../../state/support';
import { PageHeader, LoadingPage, ErrorMessage, EmptyState, relativeTime, formatTime } from '../../components/ui';
import { StatusPill } from './pills';

export function SupportListPage() {
  const [tickets, setTickets] = useState([]);
  const [scope, setScope] = useState('mine');
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(null);

  async function load() {
    setLoading(true);
    setError(null);
    try {
      const data = await get('/support/tickets');
      setTickets(Array.isArray(data.tickets) ? data.tickets : []);
      setScope(data.scope === 'org' ? 'org' : 'mine');
    } catch (err) {
      setError(err);
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => { load(); }, []);

  if (loading) return <LoadingPage />;
  if (error) return <ErrorMessage error={error} onRetry={load} />;

  const count = `${tickets.length} ticket${tickets.length !== 1 ? 's' : ''}`;
  const subtitle = scope === 'org'
    ? `${count} from everyone in your organization`
    : `${count} you opened. Owners and admins can also see them.`;

  return (
    <div class="support-page">
      <PageHeader
        title={scope === 'org' ? 'Support Tickets' : 'Your Support Tickets'}
        subtitle={subtitle}
        actions={
          <button
            type="button"
            class="btn btn-primary"
            aria-haspopup="dialog"
            onClick={e => openSupportDrawer({ returnFocus: e.currentTarget })}
          >
            New ticket
          </button>
        }
      />

      {tickets.length === 0 ? (
        <EmptyState
          title="No tickets yet"
          description="Select New ticket to contact support."
        />
      ) : (
        <div class="alert-list">
          {tickets.map(ticket => (
            <a key={ticket.id} href={`/support/${encodeURIComponent(ticket.id)}`} class="alert-card">
              <div class="alert-card-left">
                <div class="alert-card-info">
                  <h3 class="support-list-subject">{ticket.subject}</h3>
                  <p class="alert-card-detail support-meta-row">
                    {scope === 'org' && (
                      <span>Opened by {openerName(ticket, { sentence: true })}</span>
                    )}
                    {ticket.message_count != null && (
                      <span>{ticket.message_count} message{ticket.message_count !== 1 ? 's' : ''}</span>
                    )}
                  </p>
                </div>
              </div>
              <div class="alert-card-right support-meta-row">
                <StatusPill value={ticket.status} />
                <span class="alert-card-time">
                  <span class="sr-only">Updated </span>
                  <time dateTime={ticket.updated_at || ticket.created_at} title={formatTime(ticket.updated_at || ticket.created_at)}>
                    {relativeTime(ticket.updated_at || ticket.created_at)}
                  </time>
                </span>
              </div>
            </a>
          ))}
        </div>
      )}
    </div>
  );
}
