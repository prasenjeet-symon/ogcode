import test from 'node:test';
import assert from 'node:assert/strict';

import { slugify } from './slugify.js';

test('basic slug', () => {
  assert.equal(slugify('Hello, World!'), 'hello-world');
});

test('trims separators at both ends', () => {
  assert.equal(slugify('  spaced  out  '), 'spaced-out');
});

test('symbols collapse to one dash', () => {
  assert.equal(slugify('C++ & Rust'), 'c-rust');
});

test('an existing slug is unchanged', () => {
  assert.equal(slugify('already-a-slug'), 'already-a-slug');
});

test('empty input', () => {
  assert.equal(slugify(''), '');
});

test('long input is capped without a trailing dash', () => {
  const slug = slugify('word '.repeat(40));
  assert.ok(slug.length <= 60, `slug too long: ${slug.length}`);
  assert.ok(!slug.endsWith('-'), `slug ends with a dash: ${slug}`);
});
