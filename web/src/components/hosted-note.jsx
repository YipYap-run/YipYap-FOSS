import { appMeta } from '../state/auth';

// Product site for the hosted service. Self-hosted installs link here from
// settings so admins know the managed option exists.
export const HOSTED_URL = 'https://yipyap.run';

// HostedNote is a short, static note shown only on the self-hosted edition.
// It replaces plan upgrade prompts, which do not apply to self-hosted installs.
export function HostedNote() {
  if (appMeta.value?.edition !== 'foss') return null;
  return (
    <aside class="hosted-note" aria-labelledby="hosted-note-title">
      <p>
        <strong id="hosted-note-title">Self-hosted edition.</strong>{' '}
        Hosted YipYap adds managed checks, SMS and voice alerts, SSO, and more,
        with no server to run.{' '}
        <a href={HOSTED_URL}>Learn about hosted YipYap</a>
      </p>
    </aside>
  );
}

// operatorLimits returns the operator-set caps from /api/v1/meta on the
// self-hosted edition, or null on SaaS. A max_* value of 0 means unlimited.
export function operatorLimits() {
  if (appMeta.value?.edition !== 'foss') return null;
  return appMeta.value?.operator_limits || { min_check_interval_seconds: 10 };
}
