import { currentUser } from '../../state/auth';

// Org-wide switch for resolving an alert while its monitor is still failing
// (org setting allow_resolve_while_down, owner/admin only on the server).
// The copy says what actually happens: the checker opens a new alert only
// when a monitor goes from up to down, so a resolve mid-outage means nobody
// is paged again until the monitor recovers and fails again.
export function AllowResolveWhileDownSetting({ checked, onToggle }) {
  const role = currentUser.value?.role;
  const canEdit = role === 'owner' || role === 'admin';
  const helpId = 'allow-resolve-while-down-help';
  return (
    <div class="form-group" style="margin-top: 16px; margin-bottom: 0">
      <label style={`display: flex; align-items: center; gap: 8px; margin: 0; cursor: ${canEdit ? 'pointer' : 'default'}`}>
        <input type="checkbox" checked={checked} onChange={onToggle}
          disabled={!canEdit} aria-describedby={helpId} />
        Allow resolving alerts while the monitor is still down
      </label>
      <div id={helpId} class="text-muted" style="font-size: 0.8125rem; margin: 4px 0 0 26px; max-width: 62ch">
        <p style="margin: 0 0 4px 0">
          Off (default): Resolve is refused until the monitor passes a check. People are
          pointed to Acknowledge, which stops paging, or Mute, which keeps checks running but stops its notifications.
          The alert resolves on its own when the monitor recovers.
        </p>
        <p style="margin: 0">
          On: anyone who can act on alerts can resolve during an outage. The alert then stays
          closed for the rest of that outage, and no one is paged again until the monitor
          recovers and fails again.
          {!canEdit && ' Only owners and admins can change this.'}
        </p>
      </div>
    </div>
  );
}
