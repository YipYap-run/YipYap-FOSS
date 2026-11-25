import { canSeeSettingsTab } from '../../state/permissions.mjs';

export const SETTING_TABS = [
  { key: 'account', label: 'Account' },
  { key: 'sessions', label: 'Sessions' },
  { key: 'contact-methods', label: 'Contact Methods' },
  { key: 'channels', label: 'Channels' },
  { key: 'escalation', label: 'Escalation' },
  { key: 'integrations', label: 'Integrations' },
  { key: 'teams', label: 'Teams' },
  { key: 'monitor-states', label: 'Monitor States' },
  { key: 'maintenance', label: 'Maintenance' },
  { key: 'status-page', label: 'Status Pages' },
  { key: 'api-keys', label: 'API Keys' },
  { key: 'sso', label: 'SSO' },
  { key: 'members', label: 'Members' },
  { key: 'support', label: 'Support' },
  { key: 'billing', label: 'Billing' },
  { key: 'security', label: 'Security' },
  { key: 'import', label: 'Import' },
  { key: 'data', label: 'Data' },
  { key: 'org', label: 'Organization' },
];

export function availableSettingsTabs(meta, paid) {
  const isFoss = meta?.edition === 'foss';
  return SETTING_TABS.filter(t => {
    if (t.key === 'billing' && (isFoss || !meta?.billing_enabled)) return false;
    if (isFoss && ['teams', 'status-page', 'sso', 'integrations'].includes(t.key)) return false;
    if (!paid && ['sso', 'integrations'].includes(t.key)) return false;
    if (t.key === 'security' && isFoss) return false;
    if (t.key === 'support' && isFoss) return false;
    return true;
  });
}

export function visibleSettingsTabs(meta, paid, role) {
  return availableSettingsTabs(meta, paid).filter(t => canSeeSettingsTab(role, t.key));
}
