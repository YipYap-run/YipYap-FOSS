import { useState, useEffect, useRef } from 'preact/hooks';
import { get, post, patch, del } from '../../api/client';
import { Card, LoadingPage, ErrorMessage, Spinner, formatTime, useToast, ToastContainer } from '../../components/ui';
import { currentOrg, currentUser, appMeta, billingStatus, loadMeta } from '../../state/auth';
import { formatPromoDate, promoCardCopy, promoUpgradeCopy } from '../../components/promo-copy.mjs';
import { isOrgAdmin } from '../../state/permissions.mjs';
import { SMSVoiceUsage } from './usage';
import { pollDelay, isStripePlanActive, safeRedirectURL, stripCheckoutMarker, checkAgainMessage } from './checkout-activation.mjs';
import {
  hasSubscription, isStaffManagedPlan, validDate, planName, trialNote, periodLabel, SEATS_HINT,
  ENTERPRISE_ADD_ON_NOTE, enterpriseConfirm, enterpriseSettled, enterprisePendingText, enterpriseStalledText,
  checkoutConflict, activationDoneText, ACTIVATION_PENDING_TEXT, billingEmailRequest,
  formatCents, enterpriseMonthlyLabel, billingEmailError, billingEmailServerError, memberPlanNote,
  enterpriseConflictText, portalErrorText,
} from './billing-view.mjs';

// Delays between GET /billing checks after an Enterprise add-on change,
// about 50 seconds in total before the page says to reload later.
const ENTERPRISE_POLL_MS = [2000, 3000, 5000, 5000, 10000, 10000, 15000];

// Reads and strips the checkout return marker so a reload or bookmark does not
// restart the activation flow.
function takeCheckoutReturn() {
  try {
    const { returned, search } = stripCheckoutMarker(window.location.search);
    if (!returned) return false;
    history.replaceState(null, '', window.location.pathname + search + window.location.hash);
    return true;
  } catch (_) {
    return false;
  }
}

async function refreshPlanState() {
  try { currentOrg.value = await get('/org'); } catch (_) {}
  await loadMeta();
}

// Activation states after returning from checkout:
//   null      normal page
//   'pending' polling for the webhook to land
//   'slow'    still polling in the background, the user can check again
//   'stalled' background polling stopped, the user can still check again
//   'done'    plan is active
function CheckoutStatus({ state, onCheckAgain, checking, checkNote, doneText }) {
  if (state === 'pending') {
    return (
      <div class="checkout-status checkout-status-pending">
        <Spinner />
        <div>
          <strong>{ACTIVATION_PENDING_TEXT}</strong>
          <p>This usually takes a few seconds. You can leave this page; the upgrade finishes either way.</p>
        </div>
      </div>
    );
  }
  if (state === 'slow' || state === 'stalled') {
    return (
      <div class="checkout-status checkout-status-slow">
        <div>
          <strong>Your plan is still activating.</strong>
          <p>
            Stripe has not confirmed the subscription with us yet. It retries on its own, so this
            normally clears within a few minutes. You will not be charged twice, and you do not need
            to check out again. If the plan has not changed after 15 minutes, <a href="/support">contact support</a>.
          </p>
          {checkNote && <p class="checkout-status-note">{checkNote}</p>}
          <button type="button" class="btn btn-sm" onClick={onCheckAgain} disabled={checking} aria-disabled={checking ? 'true' : undefined}>
            {checking ? 'Checking...' : 'Check again'}
          </button>
        </div>
      </div>
    );
  }
  if (state === 'done') {
    return (
      <div class="checkout-status checkout-status-done">
        <div>
          <strong>Your plan is active.</strong>
          <p>{doneText}</p>
        </div>
      </div>
    );
  }
  return null;
}

// EnterpriseConfirmDialog asks before adding or removing the Enterprise
// add-on (bill-7). A native <dialog> opened with showModal() keeps focus
// inside, closes on Escape and makes the page behind it inert. Focus goes
// back to the button that opened it.
function EnterpriseConfirmDialog({ text, busy, onConfirm, onCancel }) {
  const ref = useRef(null);
  const opener = useRef(null);

  useEffect(() => {
    const dlg = ref.current;
    if (!text || !dlg) return undefined;
    opener.current = document.activeElement;
    if (!dlg.open) {
      try { dlg.showModal(); } catch (_) { dlg.setAttribute('open', ''); }
    }
    return () => {
      if (dlg.open) dlg.close();
      // The opener may be disabled while the change is pending; fall back to
      // the status line that reports it so focus does not drop to <body>.
      const el = opener.current;
      if (el && typeof el.focus === 'function' && document.contains(el) && !el.disabled) {
        el.focus();
      } else {
        document.getElementById('billing-ent-status')?.focus();
      }
    };
  }, [text]);

  if (!text) return null;
  return (
    <dialog
      ref={ref}
      class="confirm-dialog"
      aria-labelledby="ent-confirm-title"
      aria-describedby="ent-confirm-desc"
      onCancel={(e) => { e.preventDefault(); if (!busy) onCancel(); }}
    >
      <div class="modal-header">
        <h3 id="ent-confirm-title">{text.title}</h3>
        <button type="button" class="modal-close" aria-label="Close" onClick={onCancel} disabled={busy}>&times;</button>
      </div>
      <div class="modal-body" id="ent-confirm-desc">
        {text.price && <p><strong>{text.price}</strong></p>}
        <p class="promo-note">{text.note}</p>
        <div class="btn-group confirm-dialog-actions">
          <button type="button" class="btn" onClick={onCancel} disabled={busy} autofocus>Cancel</button>
          <button type="button" class="btn btn-primary" onClick={onConfirm} disabled={busy} aria-disabled={busy ? 'true' : undefined}>
            {busy ? 'Sending...' : text.action}
          </button>
        </div>
      </div>
    </dialog>
  );
}

export function BillingTab() {
  const [billing, setBilling] = useState(null);
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState(null);
  const [billingEmail, setBillingEmail] = useState('');
  const [saving, setSaving] = useState(false);
  const [activation, setActivation] = useState(() => (takeCheckoutReturn() ? 'pending' : null));
  const [checking, setChecking] = useState(false);
  const [checkNote, setCheckNote] = useState('');
  const [entChange, setEntChange] = useState(null); // { want, stalled }
  const [entConfirm, setEntConfirm] = useState(null); // dialog text while asking
  const [entSending, setEntSending] = useState(false);
  const [emailSaving, setEmailSaving] = useState(false);
  const [emailError, setEmailError] = useState('');
  const emailInput = useRef(null);
  const promoCard = useRef(null);
  // Set while the billing email field holds unsaved edits, so the activation
  // and Enterprise polls do not overwrite what the user is typing.
  const emailDirty = useRef(false);
  const timer = useRef(null);
  const entTimer = useRef(null);
  const pollRun = useRef(0);
  const mounted = useRef(true);
  const activated = useRef(false);
  const { toasts, toast } = useToast();
  const role = currentUser.value?.role;
  const isOwnerOrAdmin = isOrgAdmin(role);
  const awaitingActivation = activation === 'pending' || activation === 'slow' || activation === 'stalled';

  async function fetchBilling() {
    const data = await get('/billing');
    setBilling(data);
    if (!emailDirty.current) setBillingEmail(data.billing_email || '');
    billingStatus.value = data;
    return data;
  }

  async function load() {
    setLoading(true);
    setLoadError(null);
    let data = null;
    try {
      data = await fetchBilling();
    } catch (err) {
      setLoadError(err);
    }
    setLoading(false);
    return data;
  }

  async function markActivated() {
    if (activated.current) return;
    activated.current = true;
    clearTimeout(timer.current);
    setActivation('done');
    await refreshPlanState();
  }

  // Polls GET /billing until the webhook moves the org onto a paid plan:
  // after returning from checkout, or when a checkout click finds that an
  // earlier checkout already went through.
  function startActivationPolling() {
    const run = ++pollRun.current;
    let attempt = 0;
    clearTimeout(timer.current);
    setActivation('pending');

    function live() {
      return mounted.current && run === pollRun.current && !activated.current;
    }

    function schedule() {
      if (!live()) return;
      const next = pollDelay(attempt++);
      if (!next) {
        setActivation((s) => (s === 'done' ? s : 'stalled'));
        return;
      }
      if (next.slow) setActivation((s) => (s === 'pending' ? 'slow' : s));
      timer.current = setTimeout(poll, next.ms);
    }

    async function poll() {
      if (!live()) return;
      try {
        const data = await fetchBilling();
        if (!live()) return;
        if (isStripePlanActive(data)) {
          await markActivated();
          return;
        }
      } catch (_) {
        // A transient error while polling is not worth surfacing; keep trying.
      }
      schedule();
    }

    schedule();
  }

  // Initial load, plus the activation poll when returning from checkout.
  useEffect(() => {
    mounted.current = true;
    (async () => {
      const data = await load();
      if (!mounted.current || activation !== 'pending') return;
      if (isStripePlanActive(data)) {
        await markActivated();
        return;
      }
      startActivationPolling();
    })();

    // Coming back from Stripe with the browser Back button can restore this
    // page from the back/forward cache, still showing "Redirecting...".
    function onPageShow(e) {
      if (e.persisted) setSaving(false);
    }
    window.addEventListener('pageshow', onPageShow);

    return () => {
      mounted.current = false;
      clearTimeout(timer.current);
      clearTimeout(entTimer.current);
      window.removeEventListener('pageshow', onPageShow);
    };
  }, []);

  async function checkAgain() {
    setChecking(true);
    setCheckNote('');
    try {
      const data = await fetchBilling();
      if (isStripePlanActive(data)) {
        await markActivated();
      } else {
        setCheckNote(checkAgainMessage(new Date(), activation === 'slow'));
      }
    } catch (err) {
      setCheckNote(`Could not reach the server: ${err.message || 'unknown error'}. Try again in a moment.`);
    }
    setChecking(false);
  }

  async function upgrade(plan) {
    setSaving(true);
    try {
      const res = await post('/billing/checkout', plan ? { plan } : {});
      const url = safeRedirectURL(res?.url);
      if (url) {
        window.location.href = url;
        return;
      }
      toast('Could not start checkout', 'error');
    } catch (err) {
      if (checkoutConflict(err)) {
        // An earlier checkout for this org went through and its webhook is
        // due. Wait for it here rather than offering a second checkout.
        setSaving(false);
        try {
          const data = await fetchBilling();
          if (isStripePlanActive(data)) {
            await markActivated();
            return;
          }
        } catch (_) {
          // Fall through to polling.
        }
        startActivationPolling();
        return;
      }
      toast(err.message || 'Failed to start checkout', 'error');
    }
    setSaving(false);
  }

  async function openPortal() {
    setSaving(true);
    try {
      const res = await post('/billing/portal', {});
      const url = safeRedirectURL(res?.url);
      if (url) {
        window.location.href = url;
        return;
      }
      toast(portalErrorText(null), 'error');
    } catch (err) {
      if (!err.readonly) toast(portalErrorText(err), 'error');
    }
    setSaving(false);
  }

  // Waits for has_enterprise to reflect an add-on change. The add-on
  // endpoints only ask Stripe for it; the subscription webhook applies it.
  function watchEnterprise(want) {
    let attempt = 0;
    clearTimeout(entTimer.current);
    setEntChange({ want, stalled: false });

    async function poll() {
      if (!mounted.current) return;
      try {
        const data = await fetchBilling();
        if (!mounted.current) return;
        if (enterpriseSettled(data, want)) {
          setEntChange(null);
          toast(want ? 'Enterprise add-on is active' : 'Enterprise add-on removed');
          await refreshPlanState();
          return;
        }
      } catch (_) {
        // Keep trying; the stalled notice covers a lasting failure.
      }
      schedule();
    }

    function schedule() {
      if (attempt >= ENTERPRISE_POLL_MS.length) {
        setEntChange({ want, stalled: true });
        return;
      }
      entTimer.current = setTimeout(poll, ENTERPRISE_POLL_MS[attempt++]);
    }

    schedule();
  }

  function askEnterprise() {
    setEntConfirm(enterpriseConfirm(billing));
  }

  async function confirmEnterprise() {
    const want = !billing.has_enterprise;
    setEntSending(true);
    try {
      if (want) {
        await post('/billing/enterprise', {});
      } else {
        await del('/billing/enterprise');
      }
      setEntConfirm(null);
      watchEnterprise(want);
    } catch (err) {
      setEntConfirm(null);
      const conflict = enterpriseConflictText(err, want);
      if (conflict) {
        // The page was stale (another admin, the webhook, or a lapsed
        // subscription). Show the real state.
        try { await fetchBilling(); } catch (_) {}
        await refreshPlanState();
        toast(conflict);
      } else if (!err.readonly) {
        toast('Could not update the Enterprise add-on. Try again in a moment.', 'error');
      }
    }
    setEntSending(false);
  }

  async function saveBillingEmail(e) {
    e.preventDefault();
    const invalid = billingEmailError(billingEmail);
    if (invalid) {
      setEmailError(invalid);
      emailInput.current?.focus();
      return;
    }
    const { path, body } = billingEmailRequest(billingEmail);
    setEmailError('');
    setEmailSaving(true);
    try {
      const res = await patch(path, body);
      emailDirty.current = false;
      setBillingEmail(res?.billing_email || body.email);
      toast('Billing email updated');
    } catch (err) {
      setEmailError(billingEmailServerError(err));
      emailInput.current?.focus();
    }
    setEmailSaving(false);
  }

  // The live region stays mounted across states so screen readers announce
  // each activation change.
  const statusRegion = (
    <div role="status" aria-live="polite" class="checkout-status-region">
      <CheckoutStatus state={activation} onCheckAgain={checkAgain} checking={checking} checkNote={checkNote} doneText={activationDoneText(billing)} />
    </div>
  );

  const seatPrice = formatCents(billing?.seat_price_cents, billing?.seat_currency);
  const seatLabel = seatPrice ? `${seatPrice}/seat/mo` : 'paid';
  const entLabel = enterpriseMonthlyLabel(billing);

  const plan = billing?.plan ?? currentOrg.value?.plan;

  useEffect(() => {
    if (plan !== 'promo' || window.location.hash !== '#promo' || !promoCard.current) return;
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    promoCard.current.scrollIntoView({ behavior: reduce ? 'auto' : 'smooth', block: 'start' });
    promoCard.current.focus({ preventScroll: true });
  }, [plan, loading, billing]);

  if (plan === 'promo') {
    const promoCopy = promoCardCopy(appMeta.value);
    return (
      <div>
        <ToastContainer toasts={toasts} />
        {statusRegion}
        <div class="card" id="promo" ref={promoCard} tabIndex={-1}>
          <div class="section-header">
            <h3>Promo plan</h3>
            <span class="badge badge-info">Promo</span>
          </div>
          <p style="color: var(--color-text-secondary); margin: 8px 0 16px">
            Your organization has a promo with all Pro and Enterprise features.
          </p>
          {appMeta.value?.promo_expires_at && (
            <dl class="promo-dates">
              <dt>{promoCopy.endLabel}</dt>
              <dd>
                {formatPromoDate(appMeta.value.promo_expires_at)}
                {promoCopy.endCountdown && <strong> ({promoCopy.endCountdown})</strong>}
              </dd>
              {appMeta.value.promo_grace_ends_at && (
                <>
                  <dt>Switches to Free</dt>
                  <dd>
                    {formatPromoDate(appMeta.value.promo_grace_ends_at)}
                    {promoCopy.freeCountdown && <strong> ({promoCopy.freeCountdown}, grace period)</strong>}
                  </dd>
                </>
              )}
            </dl>
          )}
          {appMeta.value?.promo_grace_ends_at && appMeta.value?.promo_phase !== 'grace' && (
            <p class="promo-note">
              After the promo ends, features stay on for a grace period, then the organization switches to the Free plan.
            </p>
          )}
          {isOwnerOrAdmin && !awaitingActivation && (
            <div style="margin-top: 16px; border-top: 1px solid var(--color-border); padding-top: 16px">
              <p style="margin-bottom: 8px">{promoCopy.cta}</p>
              {billing && (
                <p id="promo-upgrade-note" class="promo-note" style="margin-bottom: 12px">
                  {promoUpgradeCopy(billing.checkout_trial_end)}
                </p>
              )}
              {!billing && loading && <p class="promo-note" style="margin-bottom: 12px">Loading checkout details...</p>}
              {!billing && !loading && (
                <p class="promo-note" style="margin-bottom: 12px">
                  Could not load checkout details.{' '}
                  <button type="button" class="btn btn-sm" onClick={load}>Try again</button>
                </p>
              )}
              <div class="btn-group">
                <button
                  type="button" class="btn btn-primary" onClick={() => upgrade('pro')}
                  disabled={saving || !billing} aria-disabled={saving || !billing ? 'true' : undefined}
                  aria-describedby={billing ? 'promo-upgrade-note' : undefined}
                >
                  {saving ? 'Redirecting...' : 'Upgrade to Pro'}
                </button>
              </div>
            </div>
          )}
          {!isOwnerOrAdmin && <p class="promo-note" style="margin-top: 16px">{memberPlanNote({ plan: 'promo' })}</p>}
        </div>
      </div>
    );
  }

  if (loading && !billing) return <div>{statusRegion}<LoadingPage /></div>;
  if (!billing) return <div>{statusRegion}<ErrorMessage error={loadError || 'Failed to load billing'} onRetry={load} /></div>;

  const isFree = billing.plan === 'free';
  const staffManaged = isStaffManagedPlan(billing);
  const subscribed = hasSubscription(billing);
  const trial = trialNote(billing);
  const periodEnd = validDate(billing.period_end);
  const entBusy = !!entChange && !entChange.stalled;

  return (
    <div>
      <ToastContainer toasts={toasts} />
      {statusRegion}
      {billing.status === 'past_due' && (
        <div class="payment-banner" role="alert" style="margin-bottom: 1rem; border-radius: var(--radius-sm);">
          Payment failed.{' '}
          {isOwnerOrAdmin
            ? 'Use Manage subscription to update the payment method and avoid service interruption.'
            : 'Ask an owner or admin to update the payment method to avoid service interruption.'}
        </div>
      )}
      <h3>Billing</h3>
      <Card>
        <dl class="billing-facts">
          <dt>Current plan</dt>
          <dd>
            <strong>{isFree ? (awaitingActivation ? 'Free (upgrade in progress)' : 'Free') : planName(billing)}</strong>
            {' '}{!isFree && subscribed && seatPrice && <span class="text-muted">{billing.has_enterprise && entLabel ? `(${seatLabel} + ${entLabel} Enterprise)` : `(${seatLabel})`}</span>}
            {trial && <p class="promo-note">{trial}</p>}
          </dd>
          {!isFree && !staffManaged && (
            <>
              <dt>Seats</dt>
              <dd>
                {billing.seat_count || 0}
                <p class="promo-note">{SEATS_HINT}</p>
              </dd>
              {billing.has_enterprise && (
                <>
                  <dt>Enterprise</dt>
                  <dd>Enabled</dd>
                </>
              )}
              {periodEnd && (
                <>
                  <dt>{periodLabel(billing)}</dt>
                  <dd>{formatTime(periodEnd)}</dd>
                </>
              )}
            </>
          )}
        </dl>
        {isFree ? (
          <>
            <SMSVoiceUsage billing={billing} isOwnerOrAdmin={isOwnerOrAdmin} />
            {isOwnerOrAdmin && !awaitingActivation && (
              <>
                <p id="free-upgrade-note" class="promo-note">{ENTERPRISE_ADD_ON_NOTE}</p>
                <button type="button" class="btn btn-primary" onClick={() => upgrade()} disabled={saving} aria-disabled={saving ? 'true' : undefined} aria-describedby="free-upgrade-note">
                  {saving ? 'Redirecting...' : `Upgrade to Pro${seatPrice ? ` (${seatLabel})` : ''}`}
                </button>
              </>
            )}
            {!isOwnerOrAdmin && <p class="promo-note">{memberPlanNote(billing)}</p>}
          </>
        ) : staffManaged ? (
          <>
            <p class="promo-note">
              Your plan is managed by YipYap. <a href="/support">Contact support</a> for changes.
            </p>
            <SMSVoiceUsage billing={billing} isOwnerOrAdmin={isOwnerOrAdmin} />
          </>
        ) : (
          <>
            <SMSVoiceUsage billing={billing} isOwnerOrAdmin={isOwnerOrAdmin} />
            {!isOwnerOrAdmin && <p class="promo-note">{memberPlanNote(billing)}</p>}
            {isOwnerOrAdmin && (
              <div class="btn-group billing-actions">
                <button type="button" class="btn btn-primary" onClick={openPortal} disabled={saving} aria-disabled={saving ? 'true' : undefined}>
                  {saving ? 'Opening Stripe...' : 'Manage subscription'}
                </button>
                {subscribed && billing.status !== 'past_due' && (
                  <button type="button" class="btn btn-sm" onClick={askEnterprise} disabled={saving || entBusy} aria-describedby="billing-ent-status">
                    {billing.has_enterprise ? 'Remove Enterprise' : `Add Enterprise${entLabel ? ` (${entLabel})` : ''}`}
                  </button>
                )}
              </div>
            )}
            <div id="billing-ent-status" role="status" aria-live="polite" tabIndex={-1}>
              {entChange && (
                <p class="promo-note" style="margin-top: 8px">
                  {entChange.stalled ? enterpriseStalledText(entChange.want) : enterprisePendingText(entChange.want)}
                </p>
              )}
            </div>
          </>
        )}
      </Card>
      {!isFree && !staffManaged && (
        <Card class="mt-1" style="margin-top: 1rem;">
          <h4>Billing email</h4>
          {isOwnerOrAdmin ? (
            <form onSubmit={saveBillingEmail} noValidate>
              <div class="form-group">
                <label for="billing-email">Email</label>
                <input
                  ref={emailInput}
                  id="billing-email" type="email" autocomplete="email" required maxLength={254}
                  aria-invalid={emailError ? 'true' : undefined}
                  aria-describedby={emailError ? 'billing-email-error billing-email-hint' : 'billing-email-hint'}
                  value={billingEmail}
                  onInput={e => { emailDirty.current = true; setEmailError(''); setBillingEmail(e.target.value); }}
                  placeholder="billing@company.com"
                />
                {emailError && <p id="billing-email-error" class="field-error billing-email-error">{emailError}</p>}
                <p id="billing-email-hint" class="promo-note">Receipts and payment problems are sent here.</p>
              </div>
              <button type="submit" class="btn btn-primary" disabled={emailSaving}>{emailSaving ? 'Saving...' : 'Save'}</button>
            </form>
          ) : (
            <dl class="billing-facts">
              <dt>Email</dt>
              <dd>
                {billingEmail || 'Not set'}
                <p class="promo-note">Only owners and admins can change the billing email.</p>
              </dd>
            </dl>
          )}
        </Card>
      )}
      <EnterpriseConfirmDialog
        text={entConfirm}
        busy={entSending}
        onConfirm={confirmEnterprise}
        onCancel={() => { if (!entSending) setEntConfirm(null); }}
      />
    </div>
  );
}
