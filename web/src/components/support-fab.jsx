import { useState, useEffect } from 'preact/hooks';
import { appMeta } from '../state/auth';
import { post, get } from '../api/client';

const fabStyle = {
  position: 'fixed',
  bottom: 24,
  right: 24,
  zIndex: 1000,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  gap: 6,
  padding: '10px 18px',
  borderRadius: 9999,
  border: 'none',
  background: 'var(--color-primary, #e67e22)',
  color: '#fff',
  fontSize: '0.875rem',
  fontWeight: 600,
  cursor: 'pointer',
  boxShadow: '0 4px 14px rgba(0,0,0,0.35)',
  transition: 'transform 0.15s, box-shadow 0.15s',
};

const badgeStyle = {
  position: 'absolute',
  top: -4,
  right: -4,
  minWidth: 18,
  height: 18,
  padding: '0 5px',
  borderRadius: 9999,
  background: '#e74c3c',
  color: '#fff',
  fontSize: '0.6875rem',
  fontWeight: 700,
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'center',
  lineHeight: 1,
};

const overlayStyle = {
  position: 'fixed',
  inset: 0,
  zIndex: 1001,
  background: 'rgba(0,0,0,0.4)',
};

const drawerStyle = {
  position: 'fixed',
  top: 0,
  right: 0,
  bottom: 0,
  width: 400,
  maxWidth: '100vw',
  zIndex: 1002,
  background: 'var(--color-bg-card, #1e293b)',
  borderLeft: '1px solid var(--color-border, #334155)',
  display: 'flex',
  flexDirection: 'column',
  boxShadow: '-8px 0 30px rgba(0,0,0,0.3)',
  transition: 'transform 0.25s ease',
};

const drawerHeaderStyle = {
  display: 'flex',
  alignItems: 'center',
  justifyContent: 'space-between',
  padding: '16px 20px',
  borderBottom: '1px solid var(--color-border, #334155)',
};

const closeBtnStyle = {
  background: 'none',
  border: 'none',
  color: 'var(--color-text-muted, #94a3b8)',
  fontSize: '1.25rem',
  cursor: 'pointer',
  padding: '4px 8px',
  borderRadius: 4,
  lineHeight: 1,
};

export function SupportFAB() {
  const [open, setOpen] = useState(false);
  const [openCount, setOpenCount] = useState(0);
  const [subject, setSubject] = useState('');
  const [body, setBody] = useState('');
  const [priority, setPriority] = useState('normal');
  const [submitting, setSubmitting] = useState(false);
  const [success, setSuccess] = useState(false);
  const [error, setError] = useState(null);

  const isFOSS = appMeta.value?.edition === 'foss';

  useEffect(() => {
    if (isFOSS) return;
    get('/support/tickets?limit=1')
      .then(data => {
        setOpenCount(data.open_count ?? 0);
      })
      .catch(() => {});
  }, []);

  // Don't render in FOSS edition
  if (isFOSS) return null;

  function resetForm() {
    setSubject('');
    setBody('');
    setPriority('normal');
    setError(null);
    setSuccess(false);
  }

  function handleOpen() {
    resetForm();
    setOpen(true);
  }

  function handleClose() {
    setOpen(false);
  }

  async function handleSubmit(e) {
    e.preventDefault();
    if (!subject.trim() || !body.trim()) return;

    setSubmitting(true);
    setError(null);

    const context = {
      page: window.location.pathname,
      plan: appMeta.value?.plan,
    };
    const monitorMatch = window.location.pathname.match(/\/monitors\/([^/]+)/);
    if (monitorMatch) context.monitor_id = monitorMatch[1];
    const alertMatch = window.location.pathname.match(/\/alerts\/([^/]+)/);
    if (alertMatch) context.alert_id = alertMatch[1];

    try {
      await post('/support/tickets', {
        subject: subject.trim(),
        body: body.trim(),
        priority,
        context,
      });
      setSuccess(true);
      setOpenCount(c => c + 1);
    } catch (err) {
      setError(err.message || 'Failed to submit ticket');
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <>
      <button
        style={{ ...fabStyle, position: 'fixed' }}
        onClick={handleOpen}
        title="Contact Support"
      >
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5">
          <circle cx="12" cy="12" r="10"/>
          <path d="M9.09 9a3 3 0 015.83 1c0 2-3 3-3 3"/>
          <line x1="12" y1="17" x2="12.01" y2="17"/>
        </svg>
        <span>Help</span>
        {openCount > 0 && (
          <span style={badgeStyle}>{openCount}</span>
        )}
      </button>

      {open && (
        <>
          <div style={overlayStyle} onClick={handleClose} />
          <div style={drawerStyle}>
            <div style={drawerHeaderStyle}>
              <h3 style={{ margin: 0, fontSize: '1.125rem' }}>Contact Support</h3>
              <button style={closeBtnStyle} onClick={handleClose} title="Close">&times;</button>
            </div>

            <div style={{ flex: 1, overflow: 'auto', padding: 20 }}>
              {success ? (
                <div style={{ textAlign: 'center', paddingTop: 40 }}>
                  <div style={{
                    width: 48, height: 48, borderRadius: '50%',
                    background: '#27ae60', color: '#fff',
                    display: 'flex', alignItems: 'center', justifyContent: 'center',
                    margin: '0 auto 16px', fontSize: '1.5rem',
                  }}>&#10003;</div>
                  <h4 style={{ margin: '0 0 8px' }}>Ticket Submitted</h4>
                  <p style={{ color: 'var(--color-text-muted, #94a3b8)', margin: '0 0 20px' }}>
                    We'll get back to you as soon as possible.
                  </p>
                  <a
                    href="/support"
                    class="btn btn-primary"
                    style={{ display: 'inline-block', textDecoration: 'none' }}
                  >
                    View My Tickets
                  </a>
                </div>
              ) : (
                <form onSubmit={handleSubmit}>
                  {error && (
                    <div style={{
                      padding: '10px 14px',
                      borderRadius: 6,
                      background: 'rgba(231,76,60,0.15)',
                      color: '#e74c3c',
                      fontSize: '0.875rem',
                      marginBottom: 16,
                    }}>
                      {error}
                    </div>
                  )}

                  <div class="form-group">
                    <label>Subject</label>
                    <input
                      type="text"
                      value={subject}
                      onInput={e => setSubject(e.target.value)}
                      placeholder="Brief summary of your issue"
                      required
                    />
                  </div>

                  <div class="form-group">
                    <label>Description</label>
                    <textarea
                      rows="5"
                      value={body}
                      onInput={e => setBody(e.target.value)}
                      placeholder="Describe what you're experiencing..."
                      required
                    />
                  </div>

                  <div class="form-group">
                    <label>Priority</label>
                    <select value={priority} onChange={e => setPriority(e.target.value)}>
                      <option value="normal">Normal</option>
                      <option value="high">High</option>
                    </select>
                  </div>

                  <button
                    type="submit"
                    class="btn btn-primary"
                    disabled={submitting}
                    style={{ width: '100%' }}
                  >
                    {submitting ? 'Submitting...' : 'Submit Ticket'}
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
