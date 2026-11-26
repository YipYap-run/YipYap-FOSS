// Status colors as text must reach WCAG AA (4.5:1) in every theme.
// --color-up, --color-warning and --color-down are fill colors (dots, bars,
// borders); text uses the matching --color-*-text token.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';

const srcDir = fileURLToPath(new URL('../src/', import.meta.url));
const css = readFileSync(join(srcDir, 'styles/main.css'), 'utf8');

function block(selector) {
  const start = css.indexOf(selector + ' {');
  assert.ok(start >= 0, `missing ${selector} block`);
  return css.slice(start, css.indexOf('}', start));
}

function tokens(selector) {
  const out = {};
  for (const m of block(selector).matchAll(/--([\w-]+):\s*(#[0-9a-fA-F]{6})/g)) out[m[1]] = m[2];
  return out;
}

function luminance(hex) {
  const c = [1, 3, 5].map((i) => parseInt(hex.slice(i, i + 2), 16) / 255)
    .map((v) => (v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4));
  return 0.2126 * c[0] + 0.7152 * c[1] + 0.0722 * c[2];
}

function contrast(a, b) {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x);
  return (hi + 0.05) / (lo + 0.05);
}

test('status text tokens reach 4.5:1 on page, surface and tinted backgrounds', () => {
  const light = tokens(':root');
  for (const [name, sel] of [['light', ':root'], ['dark', '[data-theme="dark"]'], ['oled', '[data-theme="oled"]']]) {
    const t = { ...light, ...tokens(sel) };
    for (const kind of ['up', 'warning', 'down']) {
      const fg = t[`color-${kind}-text`];
      for (const bg of ['color-bg', 'color-surface', `color-${kind}-bg`]) {
        const ratio = contrast(fg, t[bg]);
        assert.ok(ratio >= 4.5, `${name}: --color-${kind}-text on --${bg} is ${ratio.toFixed(2)}:1`);
      }
    }
  }
});

function walk(dir) {
  return readdirSync(dir).flatMap((f) => {
    const p = join(dir, f);
    return statSync(p).isDirectory() ? walk(p) : [p];
  });
}

test('no text is colored with a status fill color', () => {
  const bad = /(?<![-\w])color:\s*var\(--color-(up|warning|down)\)/;
  const hits = walk(srcDir)
    .filter((p) => /\.(css|jsx|js|mjs)$/.test(p))
    .flatMap((p) => readFileSync(p, 'utf8').split('\n')
      .map((line, i) => (bad.test(line) ? `${p.slice(srcDir.length)}:${i + 1}: ${line.trim()}` : null))
      .filter(Boolean));
  assert.deepEqual(hits, [], 'use the --color-*-text token for text');
});
