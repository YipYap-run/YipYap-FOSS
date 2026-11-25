import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readLinkParams, runWhenVisible, savedRatingNotice } from '../src/pages/support/survey-link.mjs';

test('token and rating come from the fragment', () => {
  assert.deepEqual(readLinkParams({ hash: '#token=abc.def&rating=4', search: '' }), { token: 'abc.def', rating: 4 });
});

test('legacy query links still work', () => {
  assert.deepEqual(readLinkParams({ hash: '', search: '?token=old&rating=2' }), { token: 'old', rating: 2 });
});

test('fragment wins over query', () => {
  assert.equal(readLinkParams({ hash: '#token=new', search: '?token=old' }).token, 'new');
});

test('rating outside 1 to 5 is ignored', () => {
  for (const r of ['0', '6', '3.5', '-1', 'x', '']) {
    assert.equal(readLinkParams({ hash: `#token=t&rating=${r}`, search: '' }).rating, 0, r);
  }
});

function fakeTimers() {
  let now = 0;
  let seq = 0;
  const pending = new Map();
  return {
    setTimeout(fn, ms) { const id = ++seq; pending.set(id, { fn, at: now + ms }); return id; },
    clearTimeout(id) { pending.delete(id); },
    advance(ms) {
      now += ms;
      for (const [id, t] of [...pending]) {
        if (t.at <= now) { pending.delete(id); t.fn(); }
      }
    },
    get size() { return pending.size; },
  };
}

function fakeDoc(state) {
  const listeners = new Set();
  return {
    visibilityState: state,
    addEventListener: (_, fn) => listeners.add(fn),
    removeEventListener: (_, fn) => listeners.delete(fn),
    set(next) { this.visibilityState = next; for (const fn of [...listeners]) fn(); },
    get listeners() { return listeners.size; },
  };
}

test('runs after the delay when visible', () => {
  const timers = fakeTimers();
  const doc = fakeDoc('visible');
  let ran = 0;
  runWhenVisible(doc, 1500, () => ran++, timers);
  timers.advance(1499);
  assert.equal(ran, 0);
  timers.advance(1);
  assert.equal(ran, 1);
  assert.equal(doc.listeners, 0);
});

test('a hidden page does not vote until shown, then waits the full delay', () => {
  const timers = fakeTimers();
  const doc = fakeDoc('hidden');
  let ran = 0;
  runWhenVisible(doc, 1500, () => ran++, timers);
  timers.advance(10000);
  assert.equal(ran, 0);
  doc.set('visible');
  timers.advance(1000);
  assert.equal(ran, 0);
  doc.set('hidden');
  timers.advance(5000);
  assert.equal(ran, 0);
  doc.set('visible');
  timers.advance(1500);
  assert.equal(ran, 1);
});

test('cancel stops the vote', () => {
  const timers = fakeTimers();
  const doc = fakeDoc('visible');
  let ran = 0;
  const cancel = runWhenVisible(doc, 1500, () => ran++, timers);
  cancel();
  timers.advance(5000);
  doc.set('visible');
  timers.advance(5000);
  assert.equal(ran, 0);
  assert.equal(timers.size, 0);
  assert.equal(doc.listeners, 0);
});

test('saved notice copy', () => {
  assert.equal(savedRatingNotice(4), 'Thanks, you rated 4/5. Change it or add a comment below.');
});

test('survey errors map to page states', async () => {
  const { describeSurveyError, endsSurvey } = await import('../src/pages/support/survey-link.mjs');
  const withStatus = (status) => Object.assign(new Error('x'), { status });
  assert.equal(describeSurveyError(new Error('Unauthorized'), 'F').kind, 'expired');
  assert.equal(describeSurveyError(withStatus(401), 'F').kind, 'expired');
  assert.equal(describeSurveyError(withStatus(409), 'F').kind, 'reopened');
  const busy = describeSurveyError(withStatus(429), 'Not saved.');
  assert.equal(busy.kind, 'busy');
  assert.match(busy.text, /^Not saved\. .*Wait a minute/);
  assert.equal(describeSurveyError(new TypeError('Failed to fetch'), 'F').kind, 'network');
  assert.equal(describeSurveyError(withStatus(503), 'F').text, 'F Try again in a minute.');
  assert.equal(describeSurveyError(withStatus(400), 'F').text, 'F');
  assert.ok(endsSurvey(describeSurveyError(withStatus(409), 'F')));
  assert.ok(endsSurvey(describeSurveyError(withStatus(401), 'F')));
  assert.ok(!endsSurvey(describeSurveyError(withStatus(429), 'F')));
  assert.ok(!endsSurvey(describeSurveyError(new TypeError('x'), 'F')));
});

test('the survey page never puts the token in a request URL', async () => {
  const { readFile } = await import('node:fs/promises');
  const src = await readFile(new URL('../src/pages/support/feedback.jsx', import.meta.url), 'utf8');
  const calls = [...src.matchAll(/\b(?:post|get|api|fetch)\(\s*([`'"])([^`'"]*)\1/g)].map((m) => m[2]);
  assert.ok(calls.length >= 3, 'expected the survey API calls to be found');
  for (const url of calls) {
    assert.ok(!/token/i.test(url), `token in request URL: ${url}`);
    assert.ok(url === '/support/survey' || url === '/support/survey/view', `unexpected survey URL: ${url}`);
  }
});
