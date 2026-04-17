export function safeHref(url) {
  if (!url) return null;
  try {
    const u = new URL(url, window.location.origin);
    return (u.protocol === 'https:' || u.protocol === 'http:') ? url : null;
  } catch {
    return null;
  }
}
