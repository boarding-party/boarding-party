// Tests for the persona names in the Start run list: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { personaLabel } from '../ui/persona-label.js';

const fields = [{ key: 'device', options: [{ value: 'iphone-15', label: 'iPhone 15' }, { value: 'desktop', label: 'Desktop 1280×800' }] }];

test('personaLabel names the device', () => {
  assert.equal(personaLabel({ name: 'John Smith', profile: { device: 'iphone-15' } }, fields), 'John Smith (iPhone 15)');
  assert.equal(personaLabel({ name: 'Priya Shah', profile: { device: 'desktop' } }, fields), 'Priya Shah (Desktop 1280×800)');
});

test('personaLabel leaves the name alone without a known device', () => {
  assert.equal(personaLabel({ name: 'Sam Taylor', profile: {} }, fields), 'Sam Taylor');
  assert.equal(personaLabel({ name: 'Sam Taylor' }, fields), 'Sam Taylor');
  assert.equal(personaLabel({ name: 'Dev Rahman', profile: { device: 'iphone-15' } }, []), 'Dev Rahman'); // fields not loaded yet
  assert.equal(personaLabel({ name: 'Dev Rahman', profile: { device: 'fridge' } }, fields), 'Dev Rahman');
});
