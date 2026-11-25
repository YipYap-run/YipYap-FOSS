// Helpers for the Sessions settings page. Pure functions, no DOM access.

// describeDevice turns a User-Agent header into "Firefox on macOS". It
// returns null when neither the browser nor the OS is recognized, so the
// caller can fall back to the raw string.
export function describeDevice(ua) {
  if (!ua || typeof ua !== 'string') return null;
  const browser = detectBrowser(ua);
  const os = detectOS(ua);
  if (browser && os) return `${browser} on ${os}`;
  return browser || os || null;
}

function detectBrowser(ua) {
  if (/\bEdg(e|A|iOS)?\//.test(ua)) return 'Edge';
  if (/\b(OPR|Opera)\//.test(ua)) return 'Opera';
  if (/\bSamsungBrowser\//.test(ua)) return 'Samsung Internet';
  if (/\b(Firefox|FxiOS)\//.test(ua)) return 'Firefox';
  if (/\bVivaldi\//.test(ua)) return 'Vivaldi';
  if (/\b(Chrome|CriOS)\//.test(ua) && !/\bChromium\//.test(ua)) return 'Chrome';
  if (/\bChromium\//.test(ua)) return 'Chromium';
  if (/\bVersion\/[\d.]+.*\bSafari\//.test(ua)) return 'Safari';
  if (/^curl\//i.test(ua)) return 'curl';
  if (/^Go-http-client\//.test(ua)) return 'Go HTTP client';
  return null;
}

function detectOS(ua) {
  if (/\biPad\b/.test(ua)) return 'iPadOS';
  if (/\b(iPhone|iPod)\b/.test(ua)) return 'iOS';
  if (/\bAndroid\b/.test(ua)) return 'Android';
  if (/\bCrOS\b/.test(ua)) return 'ChromeOS';
  if (/\bWindows\b/.test(ua)) return 'Windows';
  if (/\bMac OS X\b|\bMacintosh\b/.test(ua)) return 'macOS';
  if (/\bLinux\b/.test(ua)) return 'Linux';
  return null;
}

const METHOD_LABELS = {
  password: 'Password',
  mfa: 'Password and two-factor code',
  passkey: 'Passkey',
  sso: 'SSO',
  impersonation: 'Staff access',
};

// signInMethodLabel names how the session signed in.
export function signInMethodLabel(method) {
  return METHOD_LABELS[method] || 'Unknown method';
}

// lastActiveLabel says when a session was last used. The server updates
// last_seen_at at most once a minute, so anything within two minutes counts
// as active now.
export function lastActiveLabel(ts, now = Date.now()) {
  const t = new Date(ts).getTime();
  if (!ts || Number.isNaN(t)) return 'Last active time unknown';
  const diff = now - t;
  if (diff < 2 * 60 * 1000) return 'Active now';
  return `Last active ${relative(diff)}`;
}

function relative(diffMs) {
  const minutes = Math.round(diffMs / 60000);
  if (minutes < 60) return `${minutes} minutes ago`;
  const hours = Math.round(minutes / 60);
  if (hours < 24) return hours === 1 ? '1 hour ago' : `${hours} hours ago`;
  const days = Math.round(hours / 24);
  return days === 1 ? '1 day ago' : `${days} days ago`;
}
