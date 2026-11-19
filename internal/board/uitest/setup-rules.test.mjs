// Tests for the setup page's rules: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { parseStories, runsPerDay, costLine, loginRowsValid, MAX_STORIES, MAX_STORY } from '../ui/setup-rules.js';

test('parseStories: one per line, trimmed, blanks dropped', () => {
  assert.deepEqual(parseStories('  sign up \n\n make an invoice\n'), { stories: ['sign up', 'make an invoice'], errors: [] });
});

test('parseStories: at least one, at most 5', () => {
  assert.deepEqual(parseStories(' \n ').errors, ['Write at least one story.']);
  assert.equal(parseStories('a\nb\nc\nd\ne').errors.length, 0);
  assert.deepEqual(parseStories('a\nb\nc\nd\ne\nf').errors, [`At most ${MAX_STORIES} stories.`]);
});

test('parseStories: a long line is named by its number', () => {
  const r = parseStories('fine\n' + 'x'.repeat(MAX_STORY + 1));
  assert.deepEqual(r.errors, [`Story 2 is longer than ${MAX_STORY} characters.`]);
  assert.equal(parseStories('x'.repeat(MAX_STORY)).errors.length, 0);
});

test('runsPerDay: personas × stories, at most 30', () => {
  assert.equal(runsPerDay(4, 2), 8);
  assert.equal(runsPerDay(6, 5), 30);
  assert.equal(runsPerDay(4, 0), 0);
});

test('costLine: says what the money allows and that later runs wait', () => {
  const s = costLine(5, 4, 2);
  assert.match(s, /\$5/);
  assert.match(s, /8 runs/);
  assert.match(s, /next day/);
  assert.match(costLine(5, 1, 1), /1 run a day/);
});

test('loginRowsValid: every field, no duplicate kind', () => {
  assert.equal(loginRowsValid([]), '');
  assert.equal(loginRowsValid([{ kind: 'admin', username: 'a@x', password: 'p' }]), '');
  assert.equal(loginRowsValid([{ kind: 'admin', username: '', password: 'p' }]), 'Each login needs a kind of user, a username and a password.');
  assert.equal(loginRowsValid([{ kind: 'Admin', username: 'a', password: 'p' }, { kind: ' admin', username: 'b', password: 'q' }]),
    'Two logins are for "admin". Keep one per kind of user.');
});
