// Tests for the lead ask rules: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { askLine, raiseLabel, keepLabel, logLine } from '../ui/ask-rules.js';

test('askLine says what the lead wants and has now', () => {
  assert.equal(askLine({ limit: 'maxChildren', now: 2, to: 3 }), 'Asks for 3 persona slots (now 2).');
  assert.equal(askLine({ limit: 'maxChildren', now: 1, to: 2 }), 'Asks for 2 persona slots (now 1).');
  assert.equal(askLine({ limit: 'dailyRuns', now: 4, to: 6 }), 'Asks for 6 child runs a day (now 4).');
  assert.equal(askLine({ limit: 'dailyBudget', now: 2, to: 3.5 }), 'Asks for $3.50 a day (now $2.00).');
  assert.equal(askLine({ limit: 'wakeAt', now: 1, to: 2 }), '');
});

test('raiseLabel names the new value', () => {
  assert.equal(raiseLabel({ limit: 'maxChildren', to: 3 }), 'Raise to 3');
  assert.equal(raiseLabel({ limit: 'dailyBudget', to: 3.5 }), 'Raise to $3.50');
});

test('keepLabel names the value now', () => {
  assert.equal(keepLabel({ limit: 'maxChildren', now: 2 }), 'Keep at 2');
  assert.equal(keepLabel({ limit: 'dailyBudget', now: 2 }), 'Keep at $2.00');
});

test('logLine reads asks and the owner\'s no in the lead log', () => {
  assert.deepEqual(logLine({ action: 'ask', subject: 'maxChildren 3' }), ['asked', '3 persona slots']);
  assert.deepEqual(logLine({ action: 'ask', subject: 'dailyBudget 2.5' }), ['asked', '$2.50 a day']);
  assert.deepEqual(logLine({ action: 'decline', subject: 'maxChildren 2' }), ['you kept', '2 persona slots']);
  assert.deepEqual(logLine({ action: 'ask', subject: 'odd' }), ['asked', 'odd']);
  assert.deepEqual(logLine({ action: 'create', subject: 'Ana' }), ['create', 'Ana']);
});
