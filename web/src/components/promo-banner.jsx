import { useEffect } from 'preact/hooks';
import { appMeta, currentUser, refreshMetaIfStale } from '../state/auth';
import { isOrgAdmin } from '../state/permissions.mjs';
import { promoBanner } from './promo-copy.mjs';

// Reload /meta at most once an hour when the tab comes back into view, so a
// tab left open moves from the warning to the grace banner and the day count
// stays current.
const META_MAX_AGE_MS = 60 * 60 * 1000;

// PromoBanner shows the promo end date to every member from 14 days before
// expiry, and the switch-to-Free countdown during grace. Owners and admins
// get a link to Billing.
export function PromoBanner() {
  const isPromo = appMeta.value?.plan === 'promo';
  useEffect(() => {
    if (!isPromo) return undefined;
    const onVisible = () => {
      if (document.visibilityState === 'visible') refreshMetaIfStale(META_MAX_AGE_MS);
    };
    document.addEventListener('visibilitychange', onVisible);
    return () => document.removeEventListener('visibilitychange', onVisible);
  }, [isPromo]);

  const banner = promoBanner(appMeta.value, isOrgAdmin(currentUser.value?.role));
  if (!banner) return null;
  return (
    <div class="promo-banner" role="status">
      {banner.lead}
      {banner.action && <>{' '}<a href={banner.actionHref}>{banner.action}</a></>}
      {banner.tail}
    </div>
  );
}
