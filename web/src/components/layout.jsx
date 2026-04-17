import { currentUser, currentOrg, logout, billingStatus, appMeta } from '../state/auth';
import { wsConnected } from '../api/ws';
import { SupportFAB } from './support-fab';
import { theme, toggleTheme } from '../state/theme';

const NAV_ITEMS = [
  { href: '/', label: 'Dashboard', icon: 'grid' },
  { href: '/monitors', label: 'Monitors', icon: 'activity' },
  { href: '/alerts', label: 'Alerts', icon: 'bell' },
  { href: '/incidents', label: 'Incidents', icon: 'megaphone', saasOnly: true },
  { href: '/oncall', label: 'On-Call', icon: 'phone' },
  { href: '/services', label: 'Services', icon: 'layers', saasOnly: true },
  { href: '/support', label: 'Support', icon: 'help-circle', saasOnly: true },
  { href: '/settings', label: 'Settings', icon: 'settings' },
];

function NavIcon({ icon }) {
  const icons = {
    grid: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/></svg>,
    activity: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="22 12 18 12 15 21 9 3 6 12 2 12"/></svg>,
    bell: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 8A6 6 0 006 8c0 7-3 9-3 9h18s-3-2-3-9"/><path d="M13.73 21a2 2 0 01-3.46 0"/></svg>,
    phone: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M22 16.92v3a2 2 0 01-2.18 2 19.79 19.79 0 01-8.63-3.07 19.5 19.5 0 01-6-6 19.79 19.79 0 01-3.07-8.67A2 2 0 014.11 2h3a2 2 0 012 1.72c.127.96.361 1.903.7 2.81a2 2 0 01-.45 2.11L8.09 9.91a16 16 0 006 6l1.27-1.27a2 2 0 012.11-.45c.907.339 1.85.573 2.81.7A2 2 0 0122 16.92z"/></svg>,
    layers: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/></svg>,
    settings: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-4 0v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83-2.83l.06-.06A1.65 1.65 0 004.68 15a1.65 1.65 0 00-1.51-1H3a2 2 0 010-4h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 012.83-2.83l.06.06A1.65 1.65 0 009 4.68a1.65 1.65 0 001-1.51V3a2 2 0 014 0v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 2.83l-.06.06A1.65 1.65 0 0019.4 9a1.65 1.65 0 001.51 1H21a2 2 0 010 4h-.09a1.65 1.65 0 00-1.51 1z"/></svg>,
    megaphone: <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M18 3v18l-7-4H5a2 2 0 01-2-2V9a2 2 0 012-2h6l7-4z"/><line x1="21" y1="9" x2="21" y2="15"/></svg>,
    'help-circle': <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="10"/><path d="M9.09 9a3 3 0 015.83 1c0 2-3 3-3 3"/><line x1="12" y1="17" x2="12.01" y2="17"/></svg>,
  };
  return <span class="nav-icon">{icons[icon]}</span>;
}

function isActive(href) {
  const path = typeof window !== 'undefined' ? window.location.pathname : '/';
  if (href === '/') return path === '/';
  return path.startsWith(href);
}

export function Layout({ children }) {
  const user = currentUser.value;
  const org = currentOrg.value;
  const items = NAV_ITEMS.filter(item =>
    !item.saasOnly || appMeta.value?.edition !== 'foss'
  );

  return (
    <div class="app-layout">
      <aside class="sidebar">
        <div class="sidebar-header">
          <h1 class="logo">YipYap</h1>
          <span class={`ws-status ${wsConnected.value ? 'connected' : 'disconnected'}`}
                title={wsConnected.value ? 'Connected' : 'Disconnected'} />
        </div>
        {org && <div class="sidebar-org">{org.name}</div>}
        <nav class="sidebar-nav">
          {items.map(item => (
            <a key={item.href} href={item.href}
               class={`nav-item ${isActive(item.href) ? 'active' : ''}`}>
              <NavIcon icon={item.icon} />
              <span class="nav-label">{item.label}</span>
            </a>
          ))}
        </nav>
        <div class="sidebar-footer">
          {user && <span class="user-email">{user.email}</span>}
          <div class="sidebar-footer-actions">
            <button
              class="btn-theme-toggle"
              onClick={toggleTheme}
              title={theme.value === 'light' ? 'Switch to dark mode' : 'Switch to light mode'}
              aria-label="Toggle theme"
            >
              {theme.value === 'light' ? (
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/></svg>
              ) : (
                <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/></svg>
              )}
            </button>
            <button class="btn-logout" onClick={() => { logout(); window.location.href = '/login'; }}>
              Sign Out
            </button>
          </div>
        </div>
      </aside>

      <nav class="bottom-nav">
        {items.map(item => (
          <a key={item.href} href={item.href}
             class={`bottom-nav-item ${isActive(item.href) ? 'active' : ''}`}>
            <NavIcon icon={item.icon} />
            <span>{item.label}</span>
          </a>
        ))}
      </nav>

      <main class="main-content">
        {appMeta.value?.billing_enabled && billingStatus.value?.status === 'past_due' && (
          <div class="payment-banner">
            Payment failed. <a href="/settings/billing">Update your payment method</a> to avoid service interruption.
          </div>
        )}
        {appMeta.value?.promo_expiring && (
          <div class="payment-banner" style="background: #fef3cd; color: #856404; border-bottom: 1px solid #ffeaa7">
            Your promotional account expires in {appMeta.value.promo_days_remaining} days.
            {' '}<a href="/settings/billing" style="color: #856404; font-weight: 600">Upgrade now</a> to keep your features.
          </div>
        )}
        {children}
        <SupportFAB />
      </main>
    </div>
  );
}
