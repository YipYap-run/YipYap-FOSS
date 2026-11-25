import { useState, useEffect } from 'preact/hooks';
import { get, patch, put } from '../../api/client';
import { Card, Modal, LoadingPage, useToast, ToastContainer, FormGroup } from '../../components/ui';
import { ChannelPermissionsCard } from './channel-permissions.jsx';
import { ChannelFailureAlertsCard } from './channel-failure-alerts.jsx';
import { currentOrg } from '../../state/auth';
import { SLUG_CHANGE_WARNING, normalizeSlug, slugError, orgPatchBody, slugChanged } from './org-view.mjs';
import { AllowResolveWhileDownSetting } from './alert-rules.jsx';

/* ─── Org Profile ─── */
export function OrgTab() {
  const org = currentOrg.value;
  const [form, setForm] = useState({ name: org?.name || '', slug: org?.slug || '', oncall_display: org?.oncall_display || 'email' });
  const [saving, setSaving] = useState(false);
  const [fieldError, setFieldError] = useState({});
  const [confirmSlug, setConfirmSlug] = useState(false);
  const [orgSettings, setOrgSettings] = useState({});
  const [settingsLoading, setSettingsLoading] = useState(true);
  const { toasts, toast } = useToast();

  useEffect(() => {
    // Best-effort: the org form renders regardless, and the settings section
    // has its own loading spinner. A failure here just shows default toggles.
    get('/org/settings').then(data => {
      setOrgSettings(data || {});
    }).catch(() => {}).finally(() => setSettingsLoading(false));
  }, []);

  const origin = typeof window !== 'undefined' ? window.location.origin : '';
  const previewSlug = normalizeSlug(form.slug) || org?.slug || '';

  function validate() {
    const errs = {};
    if (!form.name.trim()) errs.name = 'Organization name cannot be empty.';
    if (slugChanged(form, currentOrg.value)) {
      const e = slugError(form.slug);
      if (e) errs.slug = e;
    }
    setFieldError(errs);
    return Object.keys(errs).length === 0;
  }

  function submit(e) {
    e.preventDefault();
    if (!validate()) return;
    if (slugChanged(form, currentOrg.value)) {
      setConfirmSlug(true);
      return;
    }
    save();
  }

  async function save() {
    setSaving(true);
    const body = orgPatchBody(form, currentOrg.value);
    try {
      const updated = await patch('/org', body);
      currentOrg.value = { ...currentOrg.value, ...(updated || body) };
      setForm(f => ({ ...f, slug: (updated && updated.slug) || f.slug }));
      setConfirmSlug(false);
      toast('Organization saved');
    } catch (err) {
      setConfirmSlug(false);
      if (err.status === 409 || err.status === 400) {
        // Slug taken or invalid: show it on the field, not only in a toast.
        if ('slug' in body) setFieldError({ slug: err.message });
        else setFieldError({ name: err.message });
      } else {
        toast(err.message || 'Failed to save organization', 'error');
      }
    }
    setSaving(false);
  }

  async function toggleSetting(key) {
    const current = orgSettings[key] === 'true';
    const newVal = current ? 'false' : 'true';
    try {
      const updated = await put('/org/settings', { ...orgSettings, [key]: newVal });
      setOrgSettings(updated || { ...orgSettings, [key]: newVal });
    } catch (err) {
      toast(err.message || 'Failed to update setting', 'error');
    }
  }

  return (
    <div>
      <ToastContainer toasts={toasts} />
      <h3>Organization</h3>
      <Card>
        <form onSubmit={submit} noValidate>
          <FormGroup label="Organization Name" error={fieldError.name}>
            <input type="text" value={form.name} onInput={e => setForm({ ...form, name: e.target.value })} />
          </FormGroup>
          <FormGroup label="Slug" error={fieldError.slug}
                     hint={`${SLUG_CHANGE_WARNING} Status pages live at ${origin}/${previewSlug}/status/...`}>
            <input type="text" value={form.slug} autoComplete="off" spellcheck={false}
                   onInput={e => setForm({ ...form, slug: e.target.value })} />
          </FormGroup>
          <div class="form-group">
            <label>On-Call Display</label>
            <select value={form.oncall_display} onChange={e => setForm({ ...form, oncall_display: e.target.value })}>
              <option value="email">Email</option>
              <option value="name">Name</option>
            </select>
          </div>
          <button type="submit" class="btn btn-primary" disabled={saving}>
            {saving ? 'Saving...' : 'Save'}
          </button>
        </form>
      </Card>

      <Modal open={confirmSlug} onClose={() => { if (!saving) setConfirmSlug(false); }} title="Change the slug?">
        <p>{SLUG_CHANGE_WARNING} Links people already have to your status pages will stop working.</p>
        <p>
          <span class="text-muted">Old:</span> <code>{origin}/{currentOrg.value?.slug}/status/...</code><br />
          <span class="text-muted">New:</span> <code>{origin}/{normalizeSlug(form.slug)}/status/...</code>
        </p>
        <div class="btn-group modal-actions">
          <button type="button" class="btn btn-primary" onClick={save} disabled={saving}>
            {saving ? 'Saving...' : 'Change slug'}
          </button>
          <button type="button" class="btn" data-autofocus onClick={() => setConfirmSlug(false)} disabled={saving}>Cancel</button>
        </div>
      </Modal>

      <h3 style="margin-top: 24px">Alert & Monitor Settings</h3>
      <Card>
        {settingsLoading ? (
          <LoadingPage />
        ) : (
          <>
            <div class="form-group" style="display: flex; align-items: center; gap: 12px; margin-bottom: 16px">
              <label style="display: flex; align-items: center; gap: 8px; cursor: pointer; margin: 0">
                <input type="checkbox" checked={orgSettings.mute_new_monitors === 'true'} onChange={() => toggleSetting('mute_new_monitors')} />
                Auto-mute new monitors
              </label>
              <span class="text-muted" style="font-size: 0.8rem">New monitors start muted. Unmute them when ready to receive alerts.</span>
            </div>
            <div class="form-group" style="display: flex; align-items: center; gap: 12px">
              <label style="display: flex; align-items: center; gap: 8px; cursor: pointer; margin: 0">
                <input type="checkbox" checked={orgSettings.incident_auto_resolve === 'true'} onChange={() => toggleSetting('incident_auto_resolve')} />
                Auto-resolve incidents when all linked alerts recover
              </label>
            </div>
            <AllowResolveWhileDownSetting
              checked={orgSettings.allow_resolve_while_down === 'true'}
              onToggle={() => toggleSetting('allow_resolve_while_down')} />
          </>
        )}
      </Card>

      <h3 style="margin-top: 24px">Notification Channel Permissions</h3>
      <ChannelPermissionsCard toast={toast} />

      <h3 style="margin-top: 24px">Channel Failure Emails</h3>
      <ChannelFailureAlertsCard toast={toast} />
    </div>
  );
}
