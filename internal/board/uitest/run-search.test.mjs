// Tests for the runs search: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { runMatches } from '../ui/run-search.js';

const run = { id: '9ae4ab8936ad0f5468aa04810c4e1127', persona: 'Priya Shah', role: 'Bookkeeper', story: 'check-invoice', outcome: 'Totals match.' };

test('a run ID prefix finds the run', () => {
  assert.equal(runMatches(run, '9ae4ab89'), true);
});

test('the whole run ID finds the run', () => {
  assert.equal(runMatches(run, '9AE4AB8936AD0F5468AA04810C4E1127'), true);
});

test('persona, story and outcome still match', () => {
  assert.equal(runMatches(run, 'priya'), true);
  assert.equal(runMatches(run, 'invoice'), true);
  assert.equal(runMatches(run, 'totals'), true);
});

test('an empty search matches everything', () => {
  assert.equal(runMatches(run, '  '), true);
});

test('other text does not match', () => {
  assert.equal(runMatches(run, 'zzz'), false);
});
