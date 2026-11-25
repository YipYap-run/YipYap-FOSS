import { useState, useEffect } from 'preact/hooks';
import { post } from '../../api/client';
import { Card, useToast, ToastContainer } from '../../components/ui';
import { appMeta, currentUser, loadMeta } from '../../state/auth';
import { promoState, planName, formatPromoDate, daysLeftText } from '../../state/promo';
import '../../styles/promo.css';

// How often and how long to re-read /meta after returning from Stripe
// Checkout, while the webhook records the new subscription.
const CONFIRM_POLL_MS = 3000;
const CONFIRM_POLL_TRIES = 10;

const SUB_STATUS = {
  active: 'active',
  trialing: 'first charge when the promo ends',
  past_due: 'payment failed',
};

function isSafeRedirect(url) {
  return typeof url === 'string' && url.startsWith('https://');
}

// PromoBilling is the billing page for an org on a promo. It says when the
// promo ends and what the org moves to, and lets owners and admins subscribe
// ahead of time (the first charge waits for the promo to end) or manage a
// subscription that is already billing.
export function PromoBilling({ billing }) {
  const { toasts, toast } = useToast();
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const p = promoState(appMeta.value);
  const role = currentUser.value?.role;
  const canManage = role === 'owner' || role === 'admin';

  // Back from Stripe Checkout: the webhook may lag, so poll until /meta
  // reports the subscription, then drop the session id from the URL.
  useEffect(() => {
    let params;
    try { params = new URLSearchParams(window.location.search); } catch (_) { return undefined; }
    if (!params.has('session_id')) return undefined;
    window.history.replaceState(null, '', window.location.pathname);
    if (appMeta.value?.promo_subscribed) return undefined;
    setConfirming(true);
    let tries = 0;
    let timer;
    let cancelled = false;
    async function poll() {
      tries++;
      await loadMeta();
      if (cancelled) return;
      if (appMeta.value?.promo_subscribed || tries >= CONFIRM_POLL_TRIES) {
        setConfirming(false);
        return;
      }
      timer = setTimeout(poll, CONFIRM_POLL_MS);
    }
    timer = setTimeout(poll, CONFIRM_POLL_MS);
    return () => { cancelled = true; clearTimeout(timer); };
  }, []);

  if (!p) return null;

  async function subscribe() {
    setBusy(true);
    try {
      const res = await post('/billing/checkout', {});
      if (isSafeRedirect(res?.url)) {
        window.location.href = res.url;
        return;
      }
      toast('Checkout did not return a link. Try again.', 'error');
    } catch (err) {
      toast(err.message || 'Could not start checkout', 'error');
    }
    setBusy(false);
  }

  async function openPortal() {
    setBusy(true);
    try {
      const res = await post('/billing/portal', {});
      if (isSafeRedirect(res?.url)) {
        window.location.href = res.url;
        return;
      }
      toast('The billing portal did not return a link. Try again.', 'error');
    } catch (err) {
      toast(err.message || 'Could not open the billing portal', 'error');
    }
    setBusy(false);
  }

  const endDate = formatPromoDate(p.endsAt);
  const expiryDate = formatPromoDate(p.expiresAt);
  const after = planName(p.planAfter);
  const pastDue = billing?.status === 'past_due';

  let afterText;
  if (!p.endsAt) {
    afterText = 'Your promotional plan has no end date.';
  } else if (p.subscribed) {
    afterText = `When it ends, your organization moves to your ${after} subscription.`;
  } else {
    afterText = 'When it ends, your organization moves to the Free plan unless you subscribe.';
  }

  return (
    <div>
      <ToastContainer toasts={toasts} />
      {pastDue && (
        <div class="payment-banner promo-billing-alert" role="alert">
          Payment failed. Update your payment method so your {after} subscription stays active after the promo.
        </div>
      )}
      <h3>Billing</h3>
      <Card>
        <div class="section-header">
          <h4>Promotional plan</h4>
          <span class="badge promo-badge">Promo</span>
        </div>
        <p class="text-muted promo-billing-intro">
          Your organization has Enterprise usage limits through a promotional plan. {afterText}
        </p>

        <dl class="promo-billing-facts">
          {p.endsAt && p.inGrace && expiryDate && (
            <div><dt>Expired on</dt><dd>{expiryDate}</dd></div>
          )}
          {p.endsAt && (
            <div>
              <dt>{p.inGrace ? 'Limits end on' : 'Ends on'}</dt>
              <dd>
                {endDate}
                {p.daysLeft != null && <span class={p.inGrace ? 'promo-billing-urgent' : 'text-muted'}> ({daysLeftText(p.daysLeft)})</span>}
              </dd>
            </div>
          )}
          <div><dt>Then moves to</dt><dd>{p.endsAt ? after : 'Not scheduled'}</dd></div>
          <div>
            <dt>Subscription</dt>
            <dd>{p.subscribed ? `${after}, ${SUB_STATUS[billing?.status] || 'active'}` : 'None'}</dd>
          </div>
        </dl>

        <p class="promo-billing-note" role="status" aria-live="polite">
          {confirming ? 'Confirming your subscription with Stripe. This page updates when it is recorded.' : ''}
        </p>

        {!canManage ? (
          <p class="text-muted promo-billing-note">Only owners and admins can change billing.</p>
        ) : p.subscribed ? (
          <div class="btn-group">
            <button type="button" class="btn btn-primary" onClick={openPortal} disabled={busy}>
              {busy ? 'Opening...' : 'Manage subscription'}
            </button>
          </div>
        ) : p.endsAt && !confirming ? (
          <div class="promo-billing-subscribe">
            <p class="promo-billing-note">
              {p.billingStartsAt
                ? `Subscribe to Pro now to keep paid features after the promo. Your card is not charged until ${formatPromoDate(p.billingStartsAt)}.`
                : 'Subscribe to Pro now to keep paid features after the promo. Billing starts today because the promo ends within two days.'}
            </p>
            <div class="btn-group">
              <button type="button" class="btn btn-primary" onClick={subscribe} disabled={busy}>
                {busy ? 'Redirecting...' : 'Subscribe to Pro'}
              </button>
            </div>
          </div>
        ) : null}
      </Card>
    </div>
  );
}
