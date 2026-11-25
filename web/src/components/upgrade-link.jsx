import { currentUser } from '../state/auth';
import { isOrgAdmin } from '../state/permissions.mjs';

// Links to Billing for owners and admins. Other roles cannot open Billing,
// so they get a plain sentence telling them who can upgrade.
export function UpgradeLink({ children, fallback = 'Ask an owner or admin to upgrade.', ...props }) {
  if (isOrgAdmin(currentUser.value?.role)) {
    return <a href="/settings/billing" {...props}>{children}</a>;
  }
  return <span>{fallback}</span>;
}
