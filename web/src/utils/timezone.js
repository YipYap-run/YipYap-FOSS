// Time zone helpers for forms that take wall-clock times in an IANA zone.
// Everything here goes through Intl, so no zone data ships with the SPA.

export function browserTimeZone() {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC';
  } catch {
    return 'UTC';
  }
}

const partsFormatters = new Map();

function partsFormatter(tz) {
  let f = partsFormatters.get(tz);
  if (!f) {
    f = new Intl.DateTimeFormat('en-US', {
      timeZone: tz, hourCycle: 'h23',
      year: 'numeric', month: '2-digit', day: '2-digit',
      hour: '2-digit', minute: '2-digit', second: '2-digit',
    });
    partsFormatters.set(tz, f);
  }
  return f;
}

// zoneOffsetMinutes returns the UTC offset of tz at instant ms, in minutes
// (positive east of UTC). Returns null for a zone the browser does not know.
export function zoneOffsetMinutes(ms, tz) {
  try {
    const p = {};
    for (const part of partsFormatter(tz).formatToParts(new Date(ms))) p[part.type] = part.value;
    const asUTC = Date.UTC(+p.year, +p.month - 1, +p.day, +p.hour % 24, +p.minute, +p.second);
    return Math.round((asUTC - Math.floor(ms / 1000) * 1000) / 60000);
  } catch {
    return null;
  }
}

export function formatOffset(minutes) {
  if (minutes == null) return '';
  const sign = minutes < 0 ? '-' : '+';
  const abs = Math.abs(minutes);
  const pad = n => String(n).padStart(2, '0');
  return `UTC${sign}${pad(Math.floor(abs / 60))}:${pad(abs % 60)}`;
}

const LOCAL_RE = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2}))?$/;

// parseLocal splits a "YYYY-MM-DDTHH:mm" wall-clock value into numbers.
export function parseLocal(v) {
  const m = LOCAL_RE.exec(v || '');
  if (!m) return null;
  return { y: +m[1], mo: +m[2], d: +m[3], h: +m[4], mi: +m[5], s: +(m[6] || 0) };
}

// zonedLocalToDate resolves a wall-clock value in tz to an instant. A wall
// time skipped by a DST change moves forward by the gap, the same way the
// server resolves it. Returns null for bad input.
export function zonedLocalToDate(v, tz) {
  const p = parseLocal(v);
  if (!p) return null;
  const guess = Date.UTC(p.y, p.mo - 1, p.d, p.h, p.mi, p.s);
  const off1 = zoneOffsetMinutes(guess, tz);
  if (off1 == null) return null;
  let t = guess - off1 * 60000;
  const off2 = zoneOffsetMinutes(t, tz);
  if (off2 != null && off2 !== off1) {
    // Near a transition. Pick the offset that maps back to the wall time,
    // else (a skipped wall time) the earlier offset, which moves it forward.
    const t2 = guess - off2 * 60000;
    t = zoneOffsetMinutes(t2, tz) === off2 ? t2 : guess - Math.min(off1, off2) * 60000;
  }
  return new Date(t);
}

// formatLocalWall renders a wall-clock value as written, with no zone
// conversion ("24 Nov 2026, 23:00"). Pass {timeOnly: true} for "23:00".
export function formatLocalWall(v, { timeOnly = false, dateOnly = false } = {}) {
  const p = parseLocal(v);
  if (!p) return v || '';
  const d = new Date(Date.UTC(p.y, p.mo - 1, p.d, p.h, p.mi));
  const opts = { timeZone: 'UTC' };
  if (!dateOnly) Object.assign(opts, { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
  if (!timeOnly) Object.assign(opts, { day: 'numeric', month: 'short', year: 'numeric' });
  try {
    return new Intl.DateTimeFormat(undefined, opts).format(d);
  } catch {
    return v.replace('T', ' ');
  }
}

// formatInZone renders an instant in tz (date and time, short form).
export function formatInZone(date, tz) {
  try {
    return new Intl.DateTimeFormat(undefined, {
      timeZone: tz, day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', hourCycle: 'h23',
    }).format(date);
  } catch {
    return date.toISOString();
  }
}

let zoneCache = null;

function allZones() {
  if (zoneCache) return zoneCache;
  let zones = [];
  try {
    if (typeof Intl.supportedValuesOf === 'function') zones = Intl.supportedValuesOf('timeZone');
  } catch {
    zones = [];
  }
  zoneCache = zones.filter(z => z !== 'UTC');
  return zoneCache;
}

function zoneLabel(zone, now) {
  if (zone === 'UTC') return 'UTC';
  const slash = zone.indexOf('/');
  const place = (slash === -1 ? zone : zone.slice(slash + 1)).replace(/_/g, ' ').replace(/\//g, ', ');
  const off = formatOffset(zoneOffsetMinutes(now, zone));
  return off ? `${place} (${off})` : place;
}

// timeZoneGroups returns options for a time zone <select>: a "Suggested"
// group with the given zones first, then every zone the browser knows,
// grouped by region and labelled by city with its current UTC offset. The
// city comes first so typing a city name jumps to it in the native select.
export function timeZoneGroups(suggested = []) {
  const now = Date.now();
  const pinned = [...new Set(['UTC', ...suggested.filter(Boolean)])];
  const groups = new Map();
  for (const z of new Set([...allZones(), ...pinned.filter(z => z !== 'UTC')])) {
    const slash = z.indexOf('/');
    const region = slash === -1 ? 'Other' : z.slice(0, slash);
    if (!groups.has(region)) groups.set(region, []);
    groups.get(region).push({ value: z, label: zoneLabel(z, now) });
  }
  const regions = [...groups.keys()].sort((a, b) => (a === 'Other') - (b === 'Other') || a.localeCompare(b));
  return {
    suggested: pinned.map(z => ({ value: z, label: z === 'UTC' ? 'UTC' : `${z.replace(/_/g, ' ')} (${formatOffset(zoneOffsetMinutes(now, z))})` })),
    groups: regions.map(r => ({ region: r, zones: groups.get(r).sort((a, b) => a.label.localeCompare(b.label)) })),
  };
}
