import { useState, useEffect, useRef } from 'preact/hooks';
import { get, put } from '../../api/client';
import { Card, LoadingPage } from '../../components/ui';
import { currentUser } from '../../state/auth';
import {
  ROLE_ORDER, ROLE_LABELS, normalizePermissions, toggleRole, lockReason,
  samePermissions, selfLockoutWarning, firstEnabledCheckbox,
} from './channel-permissions-logic.mjs';
import './channel-permissions.css';

function RoleGroup({ legend, help, list, perms, editable, onToggle }) {
  const key = list === 'manage' ? 'manage_roles' : 'test_roles';
  const helpId = 'chanperm-' + list + '-help';
  return (
    <fieldset class="chanperm-group" aria-describedby={helpId}>
      <legend>{legend}</legend>
      <p class="chanperm-help" id={helpId}>{help}</p>
      <ul class="chanperm-roles">
        {ROLE_ORDER.map(role => {
          const reason = lockReason(perms, list, role);
          const id = 'chanperm-' + list + '-' + role;
          return (
            <li key={role}>
              <input id={id} type="checkbox" checked={perms[key].includes(role)}
                disabled={!editable || !!reason}
                aria-describedby={reason ? id + '-why' : undefined}
                onChange={() => onToggle(list, role)} />
              <label for={id}>{ROLE_LABELS[role]}</label>
              {reason && <span class="chanperm-reason" id={id + '-why'}>{reason}</span>}
            </li>
          );
        })}
      </ul>
    </fieldset>
  );
}

// ChannelPermissionsCard lets owners and admins pick which roles may
// manage and test notification channels. Other roles see it read-only.
export function ChannelPermissionsCard({ toast }) {
  const [loaded, setLoaded] = useState(null);
  const [perms, setPerms] = useState(null);
  const [saving, setSaving] = useState(false);
  const [loadError, setLoadError] = useState('');
  const [error, setError] = useState('');
  const formRef = useRef(null);

  // Save, Discard and Use defaults disable or remove themselves once used.
  // Put focus back on the first role so keyboard and screen reader users
  // are not dropped at the top of the page.
  function keepFocus() {
    setTimeout(() => firstEnabledCheckbox(formRef.current)?.focus(), 0);
  }

  function load() {
    setLoadError('');
    get('/org/notification-permissions').then(p => {
      setLoaded(p);
      setPerms(normalizePermissions(p));
    }).catch(err => setLoadError(err?.message || 'Could not load channel permissions.'));
  }
  useEffect(load, []);

  if (loadError) {
    return (
      <Card>
        <p class="field-error" role="alert">{loadError}</p>
        <button type="button" class="btn btn-sm" onClick={load}>Try again</button>
      </Card>
    );
  }
  if (!perms) return <Card><LoadingPage /></Card>;

  const editable = !!loaded?.can_configure;
  const dirty = !samePermissions(perms, loaded);
  const defaults = loaded?.defaults ? normalizePermissions(loaded.defaults) : null;
  const atDefaults = !defaults || samePermissions(perms, defaults);
  // Warn only when this save would take away access the caller has now.
  const lockout = loaded?.can_manage ? selfLockoutWarning(perms, currentUser.value?.role) : '';
  const toggle = (l, r) => { setError(''); setPerms(p => toggleRole(p, l, r)); };

  async function save(e) {
    e.preventDefault();
    setSaving(true);
    setError('');
    try {
      const updated = await put('/org/notification-permissions', perms);
      setLoaded(updated);
      setPerms(normalizePermissions(updated));
      toast && toast('Channel permissions saved');
      keepFocus();
    } catch (err) {
      setError(err?.message || 'Could not save channel permissions.');
    }
    setSaving(false);
  }

  return (
    <Card class="chanperm">
      <form ref={formRef} onSubmit={save} aria-busy={saving ? 'true' : undefined}>
        <p class="chanperm-intro">
          Choose which roles can change notification channels and send test messages.
          Owners always can. API keys are controlled by their scopes instead.
        </p>
        <div class="chanperm-groups">
          <RoleGroup list="manage" perms={perms} editable={editable} onToggle={toggle}
            legend="Who can add, edit and delete channels"
            help="Channels hold delivery targets and credentials. A wrong change can stop alerts from reaching people." />
          <RoleGroup list="test" perms={perms} editable={editable} onToggle={toggle}
            legend="Who can send test notifications"
            help="A test sends a real message. SMS and voice tests count toward your usage." />
        </div>
        {editable && lockout && <p class="chanperm-warning" role="status">{lockout}</p>}
        {error && <p class="field-error" role="alert">{error}</p>}
        {editable ? (
          <div class="chanperm-actions">
            <button type="submit" class="btn btn-primary" disabled={saving || !dirty}>
              {saving ? 'Saving...' : 'Save permissions'}
            </button>
            {dirty && !saving && (
              <button type="button" class="btn btn-sm" onClick={() => { setError(''); setPerms(normalizePermissions(loaded)); keepFocus(); }}>
                Discard changes
              </button>
            )}
            {!atDefaults && (
              <button type="button" class="btn btn-sm" onClick={() => { setError(''); setPerms(defaults); keepFocus(); }}>
                Use defaults
              </button>
            )}
          </div>
        ) : (
          <p class="chanperm-help">Only owners and admins can change these settings.</p>
        )}
      </form>
    </Card>
  );
}
