// Write-only channel secrets (notif-7). The API replaces every secret
// config value with CONFIG_SENTINEL on read, for every role: tokens,
// passwords, webhook header values, and endpoint URLs that carry a
// credential (Slack, Discord, Teams and similar incoming-webhook URLs, or
// any URL with a token in it). For a hidden URL the channel also carries
// url_field, url_host (scheme and host) and url_set=true.
//
// Sending the sentinel back keeps the stored value, a new value replaces
// it, and a header left out of the object is deleted. A sentinel with no
// stored value behind it is a 400 naming the field.

export const CONFIG_SENTINEL = '•'.repeat(8);

export function isSavedSecret(v) {
  return v === CONFIG_SENTINEL;
}

// hostLabel turns "https://hooks.slack.com" into "hooks.slack.com".
export function hostLabel(urlHost) {
  if (typeof urlHost !== 'string' || !urlHost) return '';
  return urlHost.replace(/^[a-z][a-z0-9+.-]*:\/\//i, '');
}

// savedURLText is what the form shows in place of a hidden URL.
export function savedURLText(urlHost) {
  const host = hostLabel(urlHost);
  return host ? 'Saved URL on ' + host : 'Saved URL';
}

// writeOnlyURLSummary is the channel list summary for a hidden URL, or
// null when the URL is not hidden.
export function writeOnlyURLSummary(ch) {
  if (!ch || !ch.url_set) return null;
  return hostLabel(ch.url_host) || 'URL saved';
}
