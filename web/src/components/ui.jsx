/** Shared UI primitives. */
import { cloneElement } from 'preact';
import { useState, useCallback, useEffect, useRef, useId } from 'preact/hooks';
import { FOCUSABLE, trapTab, tabbable, addToast, toastDuration, toastRole, associateLabels } from './a11y.mjs';
import { readonlyMessage } from '../edition/mfa';
import '../styles/form-feedback.css';

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

// FormError shows a form or field error and announces it to screen readers
// (role="alert"). Pass an id and point the field's aria-describedby at it
// so the error is read with the field. Renders nothing when empty.
export function FormError({ id, children, class: cls = '', ...props }) {
  if (children === undefined || children === null || children === false || children === '') return null;
  return (
    <div id={id} class={'form-error form-feedback' + (cls ? ' ' + cls : '')} role="alert" {...props}>
      {children}
    </div>
  );
}

// FormSuccess is the polite counterpart for confirmations (role="status").
// The live region stays mounted while empty, because some screen readers
// skip a status region whose node is inserted together with its text.
export function FormSuccess({ id, children, class: cls = '', ...props }) {
  if (children === false || children === null || children === undefined) children = '';
  return (
    <div id={id} class={'form-success form-feedback' + (cls ? ' ' + cls : '')} role="status" {...props}>
      {children}
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
        <button key={tab.key} type="button"
                class={`tab ${active === tab.key ? 'active' : ''}`}
                aria-pressed={active === tab.key ? 'true' : 'false'}
                onClick={() => onChange(tab.key)}>
          {tab.label}
          {tab.count != null && <span class="tab-count">{tab.count}</span>}
        </button>
      ))}
    </div>
  );
}

export function SearchInput({ value, onInput, placeholder = 'Search...', label }) {
  return (
    <div class="search-input">
      <svg class="search-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <circle cx="11" cy="11" r="8"/><path d="M21 21l-4.35-4.35"/>
      </svg>
      <input type="text" value={value} onInput={e => onInput(e.target.value)}
             placeholder={placeholder} aria-label={label || placeholder.replace(/\.+$/, '')} />
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

// Open modals, innermost last. Only the top one reacts to Escape and Tab so
// a confirm opened from another dialog does not close both.
const modalStack = [];
let fieldSeq = 0;
function nextFieldId() {
  fieldSeq += 1;
  return `field-${fieldSeq}`;
}

// useAutoLabels links sibling <label>/<input> pairs under ref (see
// associateLabels) and keeps doing so as the subtree re-renders. New forms
// should use FormGroup instead; this covers the existing settings forms.
export function useAutoLabels(ref, active = true) {
  useEffect(() => {
    const el = ref.current;
    if (!active || !el || typeof MutationObserver === 'undefined') return undefined;
    associateLabels(el, nextFieldId);
    let queued = false;
    const obs = new MutationObserver(() => {
      if (queued) return;
      queued = true;
      queueMicrotask(() => { queued = false; associateLabels(el, nextFieldId); });
    });
    obs.observe(el, { childList: true, subtree: true });
    return () => obs.disconnect();
  }, [active]);
}

// FormGroup renders a labeled field. The child control gets a generated id
// (unless it has one) and the label points at it. `hint` and `error` are
// tied to the control with aria-describedby; an error also sets aria-invalid.
export function FormGroup({ label, required, hint, error, class: cls = '', children }) {
  const baseId = useId();
  const child = Array.isArray(children) ? null : children;
  const id = (child && child.props && child.props.id) || `${baseId}-input`;
  const hintId = hint ? `${id}-hint` : undefined;
  const errorId = error ? `${id}-error` : undefined;
  const describedBy = [child?.props?.['aria-describedby'], hintId, errorId].filter(Boolean).join(' ') || undefined;
  const control = child && child.props
    ? cloneElement(child, { id, 'aria-describedby': describedBy, 'aria-invalid': error ? 'true' : child.props['aria-invalid'] })
    : children;
  return (
    <div class={`form-group${error ? ' has-error' : ''}${cls ? ` ${cls}` : ''}`}>
      <label for={child ? id : undefined} class={required ? 'required' : undefined}>{label}</label>
      {control}
      {hint && <p id={hintId} class="form-help">{hint}</p>}
      {error && <p id={errorId} class="form-error">{error}</p>}
    </div>
  );
}

export function Modal({ open, onClose, title, children }) {
  const titleId = useId();
  const dialogRef = useRef(null);
  const bodyRef = useRef(null);
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useAutoLabels(bodyRef, open);
  // Move focus into the dialog when it opens, keep Tab inside it, close on
  // Escape, and give focus back to whatever opened it when it closes.
  useEffect(() => {
    if (!open) return undefined;
    const opener = document.activeElement;
    // If the opener is gone on close (the row it sat in was removed), focus
    // goes to the heading of the area it was in instead of the page body.
    const area = opener?.closest?.('.settings-main, main');
    const el = dialogRef.current;
    modalStack.push(el);
    // A dialog can mark its safe starting point with data-autofocus (a
    // Cancel button on a destructive confirm, so Enter does not revoke).
    const first = el?.querySelector('.modal-body [data-autofocus]:not([disabled]), .modal-body [autofocus]:not([disabled])')
      || el?.querySelector('.modal-body input:not([type=hidden]):not([disabled]), .modal-body select:not([disabled]), .modal-body textarea:not([disabled]), .modal-body button:not([disabled]), .modal-body [href]');
    (first || el)?.focus();
    function onKey(e) {
      if (modalStack[modalStack.length - 1] !== el) return;
      if (e.key === 'Escape') {
        e.preventDefault();
        e.stopPropagation();
        closeRef.current?.();
        return;
      }
      if (e.key !== 'Tab' || !el) return;
      const items = tabbable(el.querySelectorAll(FOCUSABLE)).filter(n => n.offsetParent !== null || n === document.activeElement);
      const next = trapTab(items.length, items.indexOf(document.activeElement), e.shiftKey);
      if (next === -1) return;
      e.preventDefault();
      if (next === -2) el.focus();
      else items[next].focus();
    }
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('keydown', onKey);
      const i = modalStack.lastIndexOf(el);
      if (i >= 0) modalStack.splice(i, 1);
      if (opener && typeof opener.focus === 'function' && document.contains(opener)) {
        opener.focus();
      } else if (area && document.contains(area)) {
        const heading = area.querySelector('h1, h2, h3');
        const target = heading || area;
        if (!target.hasAttribute('tabindex')) target.setAttribute('tabindex', '-1');
        target.focus();
      }
    };
  }, [open]);
  if (!open) return null;
  return (
    <div class="modal-overlay" onClick={onClose}>
      <div class="modal" ref={dialogRef} role="dialog" aria-modal="true" aria-labelledby={titleId} tabIndex={-1} onClick={e => e.stopPropagation()}>
        <div class="modal-header">
          <h3 id={titleId}>{title}</h3>
          <button type="button" class="modal-close" aria-label="Close" onClick={onClose}>&times;</button>
        </div>
        <div class="modal-body" ref={bodyRef}>{children}</div>
      </div>
    </div>
  );
}

export function useToast() {
  const [toasts, setToasts] = useState([]);

  const dismiss = useCallback((id) => {
    setToasts(t => t.filter(x => x.id !== id));
  }, []);

  const toast = useCallback((message, type = 'success') => {
    // The global toast host already shows the read-only warning when the API
    // client blocks a write. Skip the duplicate from page-level catches.
    if (message === readonlyMessage.value) return;
    // Two toasts can be added in the same millisecond (member removal adds
    // one per outcome), so the id needs more than the timestamp.
    const id = `${Date.now()}-${Math.random()}`;
    // Each toast runs its own timer (see Toast) so it can pause on hover or
    // focus; errors have no timer and stay until dismissed.
    // The same message is not stacked twice, and the oldest goes once the
    // limit is reached (see addToast).
    setToasts(t => addToast(t, { id, message, type, onDismiss: () => dismiss(id) }));
  }, [dismiss]);

  return { toasts, toast, dismiss };
}

// Toast shows one message. It closes itself after toastDuration (never for
// errors), pauses while hovered or focused, and has a Dismiss button.
function Toast({ toast: t }) {
  const [hidden, setHidden] = useState(false);
  const [paused, setPaused] = useState(false);
  const remaining = useRef(toastDuration(t.message, t.type));
  const close = useCallback(() => {
    if (t.onDismiss) t.onDismiss();
    else setHidden(true);
  }, [t]);
  useEffect(() => {
    if (paused || remaining.current == null) return undefined;
    const started = Date.now();
    const timer = setTimeout(close, remaining.current);
    return () => {
      clearTimeout(timer);
      remaining.current = Math.max(1000, remaining.current - (Date.now() - started));
    };
  }, [paused, close]);
  if (hidden) return null;
  return (
    <div class={`toast toast-${t.type}`}
         onMouseEnter={() => setPaused(true)} onMouseLeave={() => setPaused(false)}
         onFocusCapture={() => setPaused(true)} onBlurCapture={() => setPaused(false)}>
      <span class="toast-message">{t.message}</span>
      <button type="button" class="toast-dismiss" aria-label="Dismiss notification" onClick={close}>&times;</button>
    </div>
  );
}

// ToastContainer holds two live regions that stay in the DOM even when
// empty, so screen readers announce each toast as it is added (WCAG 4.1.3):
// errors go to an assertive alert region, everything else is polite.
export function ToastContainer({ toasts }) {
  const list = toasts || [];
  return (
    <div class="toast-container">
      <div class="toast-region" role="status" aria-live="polite">
        {list.filter(t => toastRole(t.type) === 'status').map(t => <Toast key={t.id} toast={t} />)}
      </div>
      <div class="toast-region" role="alert" aria-live="assertive">
        {list.filter(t => toastRole(t.type) === 'alert').map(t => <Toast key={t.id} toast={t} />)}
      </div>
    </div>
  );
}

// Shared incident/support pill primitives. These live here rather than being
// copy-pasted across the incident and support list/detail pages.

export const INCIDENT_SEVERITY_MAP = {
  critical: 'critical',
  major: 'warning',
  minor: 'info',
};

export const INCIDENT_STATUS_COLORS = {
  investigating: '#e67e22',
  identified:    '#2980b9',
  monitoring:    '#2980b9',
  resolved:      '#27ae60',
  maintenance:   '#2980b9',
};

export function IncidentStatusPill({ status }) {
  const bg = INCIDENT_STATUS_COLORS[status] || 'var(--color-muted)';
  return (
    <span style={{
      display: 'inline-block',
      padding: '3px 10px',
      borderRadius: 9999,
      background: bg,
      color: '#fff',
      fontSize: '0.6875rem',
      fontWeight: 700,
      textTransform: 'uppercase',
      letterSpacing: '0.5px',
    }}>{status?.toUpperCase() || 'UNKNOWN'}</span>
  );
}

export const SUPPORT_STATUS_COLORS = {
  open:    { bg: '#27ae60', label: 'OPEN' },
  pending: { bg: '#f39c12', label: 'PENDING' },
  closed:  { bg: '#6b7280', label: 'CLOSED' },
};

export const SUPPORT_PRIORITY_COLORS = {
  low:    { bg: '#6b7280', label: 'LOW' },
  normal: { bg: '#3b82f6', label: 'NORMAL' },
  high:   { bg: '#e67e22', label: 'HIGH' },
  urgent: { bg: '#e74c3c', label: 'URGENT' },
};

export function ConfigPill({ config, value }) {
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
