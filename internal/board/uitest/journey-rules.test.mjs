// Tests for the journeys list's titles, steps and replay summaries: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { storyOf, stepsShown, summaryLine, recordLine } from '../ui/journey-rules.js';

const project = { roles: [
  { name: 'Customer', personas: [{ name: 'John Smith' }], stories: [{ name: 'first-invoice', goal: 'Make a first invoice and download the PDF.' }] },
  { name: 'Reviewer', personas: [{ name: 'Priya Shah' }], stories: [{ name: 'check-invoice', goal: '' }] },
] };

test('storyOf: goal and role of the persona\'s story', () => {
  assert.deepEqual(storyOf(project, 'John Smith', 'first-invoice'), { goal: 'Make a first invoice and download the PDF.', role: 'Customer' });
});

test('storyOf: a story without a goal, or gone from the project, has no goal', () => {
  assert.deepEqual(storyOf(project, 'Priya Shah', 'check-invoice'), { goal: '', role: 'Reviewer' });
  assert.deepEqual(storyOf(project, 'Ann', 'gone'), { goal: '', role: '' });
  assert.deepEqual(storyOf(null, 'Ann', 'gone'), { goal: '', role: '' });
});

test('stepsShown: the first few steps and how many more', () => {
  const s = ['a', 'b', 'c', 'd', 'e', 'f'];
  assert.deepEqual(stepsShown(s, 4), { shown: ['a', 'b', 'c', 'd'], more: 2 });
  assert.deepEqual(stepsShown(s.slice(0, 5), 4), { shown: ['a', 'b', 'c', 'd', 'e'], more: 0 }, 'one hidden step is shown instead of "+1 more"');
  assert.deepEqual(stepsShown([], 4), { shown: [], more: 0 });
  assert.deepEqual(stepsShown(undefined, 4), { shown: [], more: 0 });
});

test('summaryLine: journeys and this week\'s replays', () => {
  const j = (p, f) => ({ record: { weekPassed: p, weekFailed: f } });
  assert.equal(summaryLine([j(30, 1), j(9, 1), j(0, 0)]), '3 journeys · 41 replays this week, 39 passed');
  assert.equal(summaryLine([j(1, 0)]), '1 journey · 1 replay this week, passed');
  assert.equal(summaryLine([j(0, 1)]), '1 journey · 1 replay this week, failed');
  assert.equal(summaryLine([j(0, 0), {}]), '2 journeys · no replays this week');
});

test('recordLine: passes of all replays', () => {
  assert.equal(recordLine({ passed: 12, failed: 2 }), '12 of 14 passed');
  assert.equal(recordLine({ passed: 0, failed: 0 }), 'Not replayed yet');
});
