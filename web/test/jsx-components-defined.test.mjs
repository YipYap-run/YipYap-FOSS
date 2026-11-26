// Every capitalized JSX tag must be imported or defined in its own file.
//
// The bundler does not check this: a component whose import is lost (for
// example in a merge) builds fine and only throws ReferenceError when the
// page renders. The Escalation settings tab shipped broken that way in
// v1.4.0. This test fails the build instead.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync, readdirSync, statSync } from 'node:fs';
import { join, relative } from 'node:path';
import { fileURLToPath } from 'node:url';

const SRC = fileURLToPath(new URL('../src', import.meta.url));

function sourceFiles(dir) {
  const out = [];
  for (const name of readdirSync(dir)) {
    const p = join(dir, name);
    if (statSync(p).isDirectory()) out.push(...sourceFiles(p));
    else if (/\.(jsx|mjs|js)$/.test(name) && !/\.test\./.test(name)) out.push(p);
  }
  return out;
}

// Strip comments and string literals so names inside them are not counted.
function stripNoise(src) {
  return src
    .replace(/\/\*[\s\S]*?\*\//g, ' ')
    .replace(/(^|[^:])\/\/.*$/gm, '$1')
    .replace(/'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*"/g, '""');
}

export function undefinedComponents(raw) {
  const src = stripNoise(raw);
  const used = new Set([...src.matchAll(/<([A-Z][A-Za-z0-9_]*)[\s/>.]/g)].map(m => m[1]));
  const known = new Set(['Fragment']);
  // import Default, { A, B as C } from '...'; import * as NS from '...'
  for (const m of src.matchAll(/import\s+([^;]*?)\s+from\s+["']/g)) {
    for (const part of m[1].replace(/[{}]/g, ',').split(',')) {
      const name = part.trim().split(/\s+as\s+/).pop().replace(/^\*\s*/, '').trim();
      if (name) known.add(name);
    }
  }
  // function X, const X =, class X
  for (const m of src.matchAll(/\b(?:function|const|let|var|class)\s+([A-Z][A-Za-z0-9_]*)/g)) known.add(m[1]);
  // Destructured names, including renames and defaults:
  // ({ component: Component }), ({ HeadingTag = 'h3' }), const { A, b: B } = x
  for (const m of src.matchAll(/\{([^{}]*)\}/g)) {
    for (const part of m[1].split(',')) {
      const name = part.split('=')[0].split(':').pop().trim();
      if (/^[A-Z][A-Za-z0-9_]*$/.test(name)) known.add(name);
    }
  }
  return [...used].filter(u => !known.has(u)).sort();
}

test('every JSX component in web/src is imported or defined', () => {
  const problems = [];
  for (const file of sourceFiles(SRC)) {
    const missing = undefinedComponents(readFileSync(file, 'utf8'));
    if (missing.length) problems.push(`${relative(SRC, file)}: ${missing.join(', ')}`);
  }
  assert.deepEqual(problems, [], `JSX components used without an import or definition:\n${problems.join('\n')}`);
});

test('the check catches a missing import and accepts destructured props', () => {
  assert.deepEqual(undefinedComponents(`export function A() { return <EscalationTab />; }`), ['EscalationTab']);
  assert.deepEqual(undefinedComponents(`import { EscalationTab } from './e.jsx';\nconst x = <EscalationTab />;`), []);
  assert.deepEqual(undefinedComponents(`function R({ component: Component }) { return <Component />; }`), []);
  assert.deepEqual(undefinedComponents(`function S({ HeadingTag = 'h3' }) { return <HeadingTag />; }`), []);
  assert.deepEqual(undefinedComponents(`// <Ghost /> in a comment\nconst s = '<Ghost />';`), []);
});
