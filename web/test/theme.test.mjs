// Run with: npm test (node --test). No browser or extra packages needed.
// Helpers under test use .mjs because this package is CommonJS for Node.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import vm from 'node:vm';
import { initialTheme, readStoredTheme, writeStoredTheme, prefersDark } from '../src/state/theme-store.mjs';

function blockedStorage() {
  // Chrome with site data blocked throws SecurityError on access.
  const boom = () => { throw new Error('SecurityError: access denied'); };
  return { getItem: boom, setItem: boom, removeItem: boom };
}

function fakeWindow({ stored, dark = false, storage, noMatchMedia = false } = {}) {
  const data = new Map(stored === undefined ? [] : [['yipyap-theme', stored]]);
  const win = {
    matchMedia: noMatchMedia ? undefined : () => ({ matches: dark }),
  };
  if (storage === 'blocked') {
    Object.defineProperty(win, 'localStorage', { get() { throw new Error('SecurityError'); } });
  } else if (storage === 'throws') {
    win.localStorage = blockedStorage();
  } else {
    win.localStorage = {
      getItem: (k) => (data.has(k) ? data.get(k) : null),
      setItem: (k, v) => data.set(k, String(v)),
    };
  }
  return { win, data };
}

test('stored theme wins when valid', () => {
  for (const t of ['light', 'dark', 'oled']) {
    assert.equal(initialTheme(fakeWindow({ stored: t, dark: t === 'light' }).win), t);
  }
});

test('invalid stored value falls back to the system scheme', () => {
  assert.equal(initialTheme(fakeWindow({ stored: '"><script>', dark: true }).win), 'dark');
  assert.equal(initialTheme(fakeWindow({ stored: 'sepia' }).win), 'light');
  assert.equal(readStoredTheme(fakeWindow({ stored: 'sepia' }).win), null);
});

test('blocked storage does not throw and uses prefers-color-scheme', () => {
  for (const storage of ['blocked', 'throws']) {
    assert.equal(initialTheme(fakeWindow({ storage, dark: true }).win), 'dark');
    assert.equal(initialTheme(fakeWindow({ storage, dark: false }).win), 'light');
    assert.equal(writeStoredTheme(fakeWindow({ storage }).win, 'dark'), false);
  }
});

test('write saves only known themes', () => {
  const { win, data } = fakeWindow();
  assert.equal(writeStoredTheme(win, 'oled'), true);
  assert.equal(data.get('yipyap-theme'), 'oled');
  assert.equal(writeStoredTheme(win, 'neon'), false);
  assert.equal(data.get('yipyap-theme'), 'oled');
});

test('missing matchMedia means light', () => {
  assert.equal(prefersDark(fakeWindow({ noMatchMedia: true }).win), false);
});

// public/theme-init.js is a plain script that runs before the bundle. Run it
// in a sandbox with a fake document.
const initSrc = readFileSync(fileURLToPath(new URL('../public/theme-init.js', import.meta.url)), 'utf8');

function runInit(opts) {
  const { win } = fakeWindow(opts);
  const attrs = {};
  win.document = { documentElement: { setAttribute: (k, v) => { attrs[k] = v; } } };
  win.window = win;
  vm.runInNewContext(initSrc, win);
  return attrs['data-theme'];
}

test('theme-init applies a valid stored theme', () => {
  assert.equal(runInit({ stored: 'oled' }), 'oled');
});

test('theme-init ignores unknown values', () => {
  assert.equal(runInit({ stored: 'bogus', dark: true }), 'dark');
  assert.equal(runInit({ stored: 'bogus', dark: false }), 'light');
});

test('theme-init survives blocked storage', () => {
  assert.equal(runInit({ storage: 'blocked', dark: true }), 'dark');
  assert.equal(runInit({ storage: 'throws', dark: false }), 'light');
  assert.equal(runInit({ storage: 'blocked', noMatchMedia: true }), 'light');
});
