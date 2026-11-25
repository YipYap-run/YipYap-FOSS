import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import {
  FILTER_DEBOUNCE_MS,
  buildReplyQuery,
  createLatestRequest,
  debounce,
  isAbortError,
  outcomeClass,
  statusText,
} from '../src/pages/admin/reply-activity-fetch.mjs';

function fakeTimers() {
  let now = 0;
  let seq = 0;
  const pending = new Map();
  return {
    setTimeout(fn, ms) { const id = ++seq; pending.set(id, { fn, at: now + ms }); return id; },
    clearTimeout(id) { pending.delete(id); },
    advance(ms) {
      now += ms;
      for (const [id, t] of [...pending].sort((a, b) => a[1].at - b[1].at)) {
        if (t.at <= now) { pending.delete(id); t.fn(); }
      }
    },
  };
}

test('query trims text filters and drops empty ones', () => {
  const qs = new URLSearchParams(buildReplyQuery({
    since: '2026-11-24T00:00:00.000Z', channel: '  ch-1 ', alert: '   ', replyType: '', limit: 100,
  }));
  assert.equal(qs.get('channel'), 'ch-1');
  assert.equal(qs.has('alert'), false);
  assert.equal(qs.has('type'), false);
  assert.equal(qs.get('limit'), '100');
});

test('typing quickly sends one request after the pause, for the final value', () => {
  const timers = fakeTimers();
  const sent = [];
  const apply = debounce((c) => sent.push(c), FILTER_DEBOUNCE_MS, timers);
  for (const v of ['a', 'ab', 'abc', 'abcd']) {
    apply(v);
    timers.advance(100);
  }
  assert.deepEqual(sent, []);
  timers.advance(FILTER_DEBOUNCE_MS);
  assert.deepEqual(sent, ['abcd']);
});

test('debounce cancel drops the pending call', () => {
  const timers = fakeTimers();
  const sent = [];
  const apply = debounce((c) => sent.push(c), FILTER_DEBOUNCE_MS, timers);
  apply('x');
  apply.cancel();
  timers.advance(1000);
  assert.deepEqual(sent, []);
});

test('a new request aborts the old one and the old response is ignored', async () => {
  const latest = createLatestRequest();
  const shown = [];
  const resolvers = {};
  // Fake fetch that ignores the abort signal, like a response already in flight.
  const fetchFor = (filter) => new Promise((resolve) => { resolvers[filter] = resolve; });

  async function load(filter) {
    const req = latest.start();
    const rows = await fetchFor(filter);
    if (req.isCurrent()) shown.push(rows);
    return req;
  }

  const first = load('old');
  const second = load('new');
  resolvers.new(['new rows']);
  const reqNew = await second;
  resolvers.old(['old rows']);
  const reqOld = await first;

  assert.deepEqual(shown, [['new rows']]);
  assert.equal(reqOld.signal.aborted, true);
  assert.equal(reqNew.signal.aborted, false);
});

test('cancel aborts the in-flight request', () => {
  const latest = createLatestRequest();
  const req = latest.start();
  latest.cancel();
  assert.equal(req.signal.aborted, true);
  assert.equal(req.isCurrent(), false);
});

test('abort errors are recognised', () => {
  assert.equal(isAbortError(new DOMException('x', 'AbortError')), true);
  assert.equal(isAbortError(new Error('HTTP 500')), false);
  assert.equal(isAbortError(null), false);
});

// The page wires the helpers and exposes rows as real buttons. There is no DOM
// test runner in this repo, so check the source contract.
test('reply activity page rows are buttons with aria-expanded and aria-controls', () => {
  const src = readFileSync(new URL('../src/pages/admin/reply-activity.jsx', import.meta.url), 'utf8');
  assert.match(src, /<button\s+type="button"[\s\S]*?aria-expanded=\{active \? 'true' : 'false'\}[\s\S]*?aria-controls=\{detailId\}/);
  assert.match(src, /id=\{detailId\}/);
  assert.doesNotMatch(src, /<div\s+key=\{e\.id\}\s+onClick=/, 'rows must not be clickable divs');
  assert.match(src, /get\('\/admin\/cloudevents\/replies\?' \+ qs, \{ signal: req\.signal \}\)/);
  assert.match(src, /debounce\(/);
  assert.match(src, /req\.isCurrent\(\)/);
});

test('status text summarises the list for the live region', () => {
  assert.equal(statusText({ loading: true, error: null, count: 3, hasMore: false }), 'Loading replies…');
  assert.equal(statusText({ loading: false, error: new Error('x'), count: 0, hasMore: false }), '');
  assert.equal(statusText({ loading: false, error: null, count: 0, hasMore: false }), 'No replies found.');
  assert.equal(statusText({ loading: false, error: null, count: 1, hasMore: false }), 'Showing 1 reply.');
  assert.equal(statusText({ loading: false, error: null, count: 100, hasMore: true }), 'Showing the latest 100 replies. More are available.');
});

test('outcome classes cover accepted, rejected, handler errors and unknown values', () => {
  assert.equal(outcomeClass('accepted'), 'ra-accepted');
  assert.equal(outcomeClass('rejected:rate_limited'), 'ra-rejected');
  assert.equal(outcomeClass('handler_error:boom'), 'ra-failed');
  assert.equal(outcomeClass(''), 'ra-other');
  assert.equal(outcomeClass(undefined), 'ra-other');
});

// Contrast: hex pairs from reply-activity.css and main.css. WCAG AA for small
// text is 4.5:1.
function luminance(hex) {
  const n = parseInt(hex.slice(1), 16);
  const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  });
  return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2];
}
function contrast(a, b) {
  const [x, y] = [luminance(a), luminance(b)].sort((p, q) => q - p);
  return (x + 0.05) / (y + 0.05);
}

test('reply activity text colors meet 4.5:1 on their backgrounds', () => {
  const css = readFileSync(new URL('../src/styles/reply-activity.css', import.meta.url), 'utf8');
  const light = css.slice(css.indexOf('.reply-activity {'), css.indexOf('[data-theme="dark"] .reply-activity'));
  const dark = css.slice(css.indexOf('[data-theme="dark"] .reply-activity'), css.indexOf('.reply-activity .ra-intro'));
  const tok = (block, name) => block.match(new RegExp(`--${name}:\\s*(#[0-9a-fA-F]{6})`))[1];
  const pairs = [
    // [token, light background, dark background, oled background]
    ['ra-muted', ['#ffffff', '#f1f5f9'], ['#1e293b', '#334155'], ['#0a0a0a', '#1a1a1a']],
    ['ra-accepted', ['#ecfdf5'], ['#064e3b'], ['#021a10']],
    ['ra-rejected', ['#fffbeb'], ['#451a03'], ['#1a0a00']],
    ['ra-error', ['#fef2f2'], ['#450a0a'], ['#1a0505']],
    ['ra-other', ['#f1f5f9'], ['#1e293b'], ['#0a0a0a']],
  ];
  for (const [name, lightBgs, darkBgs, oledBgs] of pairs) {
    for (const bg of lightBgs) {
      const r = contrast(tok(light, name), bg);
      assert.ok(r >= 4.5, `${name} on ${bg} is ${r.toFixed(2)}:1`);
    }
    for (const bg of [...darkBgs, ...oledBgs]) {
      const r = contrast(tok(dark, name), bg);
      assert.ok(r >= 4.5, `${name} (dark) on ${bg} is ${r.toFixed(2)}:1`);
    }
  }
});

test('reply rows have a visible focus style and close on Escape', () => {
  const src = readFileSync(new URL('../src/pages/admin/reply-activity.jsx', import.meta.url), 'utf8');
  const css = readFileSync(new URL('../src/styles/reply-activity.css', import.meta.url), 'utf8');
  assert.match(css, /\.reply-row:focus-visible/);
  assert.match(src, /import '\.\.\/\.\.\/styles\/reply-activity\.css'/);
  // Escape on the expanded row button, not only inside the panel.
  assert.match(src, /class="reply-row"/);
  assert.match(src, /ev\.key === 'Escape' && active/);
  // Results are announced through a polite live region.
  assert.match(src, /role="status"\s+aria-live="polite"/);
  // No hard-coded low-contrast muted fallback left in the page.
  assert.doesNotMatch(src, /#94a3b8/);
});
