/** Shared UI primitives. */
import { useState, useCallback } from 'preact/hooks';

export function StatusBadge({ status, size = 'sm', stateName, stateColor }) {
  // If a custom state is provided, render with its color and name.
  if (stateName && stateColor) {
    return (
      <span class={`badge badge-${size}`} style={{
        background: stateColor,
        color: '#fff',
      }}>
        {stateName}
      </span>
    );
  }
  const display = status === 'unknown' ? 'pending' : status;
  const colors = {
    up: 'badge-up',
    down: 'badge-down',
    degraded: 'badge-warning',
    paused: 'badge-muted',
    pending: 'badge-muted',
    firing: 'badge-down',
    acknowledged: 'badge-warning',
    resolved: 'badge-up',
    active: 'badge-up',
    critical: 'badge-down',
    warning: 'badge-warning',
    info: 'badge-info',
  };
  return (
    <span class={`badge ${colors[display] || 'badge-muted'} badge-${size}`}>
      {display}
    </span>
  );
}

export function StatusDot({ status, stateColor }) {
  const colors = {
    up: 'var(--color-up)',
    down: 'var(--color-down)',
    degraded: 'var(--color-warning)',
    paused: 'var(--color-muted)',
    pending: 'var(--color-muted)',
    unknown: 'var(--color-muted)',
  };
  return (
    <span style={{
      display: 'inline-block',
      width: 10,
      height: 10,
      borderRadius: '50%',
      background: stateColor || colors[status] || colors.unknown,
      flexShrink: 0,
    }} />
  );
}

export function SeverityBadge({ severity, size = 'sm' }) {
  return <StatusBadge status={severity} size={size} />;
}

export function SeverityIcon({ severity, size = 32 }) {
  const icons = {
    critical: (
      <svg viewBox="0 0 24 24" width={size} height={size} fill="none" style="flex-shrink: 0">
        <circle cx="12" cy="12" r="11" fill="var(--color-down)" />
        <path d="M12 7v6" stroke="white" stroke-width="2.5" stroke-linecap="round" />
        <circle cx="12" cy="17" r="1.5" fill="white" />
      </svg>
    ),
    warning: (
      <svg viewBox="0 0 24 24" width={size} height={size} fill="none" style="flex-shrink: 0">
        <path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" fill="var(--color-warning)" />
        <path d="M12 9v4" stroke="white" stroke-width="2" stroke-linecap="round" />
        <circle cx="12" cy="17" r="1" fill="white" />
      </svg>
    ),
    info: (
      <svg viewBox="0 0 24 24" width={size} height={size} fill="none" style="flex-shrink: 0">
        <circle cx="12" cy="12" r="11" fill="var(--color-info)" />
        <circle cx="12" cy="8" r="1.5" fill="white" />
        <path d="M12 11v6" stroke="white" stroke-width="2.5" stroke-linecap="round" />
      </svg>
    ),
  }
  return icons[severity] || icons.info
}

export function AlertStatusPill({ status }) {
  const config = {
    firing: { bg: 'var(--color-down)', color: 'white', label: 'FIRING' },
    acknowledged: { bg: 'var(--color-warning)', color: 'white', label: 'ACKED' },
    resolved: { bg: 'var(--color-up)', color: 'white', label: 'RESOLVED' },
  }
  const c = config[status] || { bg: 'var(--color-muted)', color: 'white', label: status }
  return (
    <span style={{
      display: 'inline-block',
      padding: '3px 10px',
      borderRadius: 9999,
      background: c.bg,
      color: c.color,
      fontSize: '0.6875rem',
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.5px',
    }}>{c.label}</span>
  )
}

export function Card({ children, class: cls = '', ...props }) {
  return <div class={`card ${cls}`} {...props}>{children}</div>;
}

export function PageHeader({ title, subtitle, actions }) {
  return (
    <header class="page-header">
      <div>
        <h2 class="page-title">{title}</h2>
        {subtitle && <p class="page-subtitle">{subtitle}</p>}
      </div>
      {actions && <div class="page-actions">{actions}</div>}
    </header>
  );
}

export function EmptyState({ icon, title, description, action }) {
  return (
    <div class="empty-state">
      {icon && <div class="empty-state-icon">{icon}</div>}
      <h3>{title}</h3>
      {description && <p>{description}</p>}
      {action}
    </div>
  );
}

export function Spinner() {
  return <div class="spinner" />;
}

export function LoadingPage() {
  return (
    <div class="loading-page">
      <Spinner />
    </div>
  );
}

export function ErrorMessage({ error, onRetry }) {
  return (
    <div class="error-message">
      <p>{error?.message || String(error)}</p>
      {onRetry && <button class="btn btn-sm" onClick={onRetry}>Retry</button>}
    </div>
  );
}

export function Tabs({ tabs, active, onChange }) {
  return (
    <div class="tabs">
      {tabs.map(tab => (
        <button key={tab.key}
                class={`tab ${active === tab.key ? 'active' : ''}`}
                onClick={() => onChange(tab.key)}>
          {tab.label}
          {tab.count != null && <span class="tab-count">{tab.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function SearchInput({ value, onInput, placeholder = 'Search...' }) {
  return (
    <div class="search-input">
      <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/>
      </svg>
      <input type="text" value={value} onInput={e => onInput(e.target.value)}
             placeholder={placeholder} />
    </div>
  );
}

export function UptimeBar({ checks = [], period = '24h' }) {
  // checks is an array of { ok: boolean } ordered oldest-first.
  // We render up to 90 bars.
  const count = period === '90d' ? 90 : period === '30d' ? 30 : period === '7d' ? 7 : 24;
  const bars = [];
  for (let i = 0; i < count; i++) {
    const c = checks[i];
    const cls = !c ? 'bar-empty' : c.ok ? 'bar-up' : 'bar-down';
    bars.push(<div key={i} class={`uptime-bar-segment ${cls}`} />);
  }

  const totalOk = checks.filter(c => c?.ok).length;
  const pct = checks.length > 0 ? ((totalOk / checks.length) * 100).toFixed(2) : '-';

  return (
    <div class="uptime-bar-container">
      <div class="uptime-bar">{bars}</div>
      <div class="uptime-bar-label">
        <span>{period}</span>
        <span>{pct}%</span>
      </div>
    </div>
  );
}

export function MiniUptimeBar({ checks = [] }) {
  if (checks.length === 0) return null;
  return (
    <div class="mini-uptime-bar">
      {checks.map((c, i) => (
        <div key={i} class={`mini-bar-seg ${c.status === 'up' ? 'bar-up' : c.status === 'degraded' ? 'bar-degraded' : c.status === 'down' ? 'bar-down' : 'bar-empty'}`} />
      ))}
    </div>
  );
}

export function formatDuration(ms) {
  if (!ms || ms < 0) return '-';
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ${m % 60}m`;
  const d = Math.floor(h / 24);
  return `${d}d ${h % 24}h`;
}

export function formatTime(ts) {
  if (!ts) return '-';
  const d = new Date(ts);
  return d.toLocaleString();
}

export function relativeTime(ts) {
  if (!ts) return '-';
  const now = Date.now();
  const diff = now - new Date(ts).getTime();
  if (diff < 0) return 'just now';
  return formatDuration(diff) + ' ago';
}

export function Modal({ open, onClose, title, children }) {
  if (!open) return null;
  return (
    <div class="modal-overlay" onClick={onClose}>
      <div class="modal" onClick={e => e.stopPropagation()}>
        <div class="modal-header">
          <h3>{title}</h3>
          <button class="modal-close" onClick={onClose}>&times;</button>
        </div>
        <div class="modal-body">{children}</div>
      </div>
    </div>
  );
}

export function useToast() {
  const [toasts, setToasts] = useState([]);

  const toast = useCallback((message, type = 'success') => {
    const id = Date.now();
    setToasts(t => [...t, { id, message, type }]);
    setTimeout(() => setToasts(t => t.filter(x => x.id !== id)), 3000);
  }, []);

  return { toasts, toast };
}

export function ToastContainer({ toasts }) {
  if (!toasts || toasts.length === 0) return null;
  return (
    <div class="toast-container">
      {toasts.map(t => (
        <div key={t.id} class={`toast toast-${t.type}`}>{t.message}</div>
      ))}
    </div>
  );
}
