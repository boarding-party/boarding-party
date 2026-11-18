// Tests for the bugs list's attention tag and the issue page's report marks: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { attention, marking, earliestOf, severityOf, DAY_MS } from '../ui/issue-rules.js';

const now = Date.parse('2026-11-18T12:00:00Z');
const ago = (ms) => new Date(now - ms).toISOString();
const H = 3600000;

test('attention: reopened beats new beats different', () => {
  const i = { status: 'open', reopened: ago(H), firstSeen: ago(H), differentAt: ago(H) };
  assert.deepEqual(attention(i, now), { label: 'Seen again after fix', tag: 'bad' });
  assert.deepEqual(attention({ ...i, reopened: undefined }, now), { label: 'New today', tag: 'run' });
});

test('attention: reopened but closed again is not seen again', () => {
  assert.equal(attention({ status: 'closed', reopened: ago(2 * DAY_MS), firstSeen: ago(9 * DAY_MS) }, now), null);
  assert.equal(attention({ status: 'not_a_bug', reopened: ago(H), firstSeen: ago(9 * DAY_MS) }, now), null);
});

test('attention: new today is a rolling 24 hours', () => {
  assert.equal(attention({ status: 'open', firstSeen: ago(23 * H) }, now).label, 'New today');
  assert.equal(attention({ status: 'open', firstSeen: ago(25 * H) }, now), null);
});

test('attention: something different within a day', () => {
  const i = { status: 'confirmed', firstSeen: ago(9 * DAY_MS), differentAt: ago(2 * H) };
  assert.deepEqual(attention(i, now), { label: 'Something different', tag: 'sup' });
  assert.equal(attention({ ...i, differentAt: ago(30 * H) }, now), null);
});

test('marking: different stays visible even as the earliest', () => {
  assert.deepEqual(marking({ id: 'a', match: 'different', difference: 'on mobile' }, 'a'), { kind: 'different', text: 'Different: on mobile' });
});

test('marking: first, joined, same, none', () => {
  assert.deepEqual(marking({ id: 'a', match: 'same' }, 'a'), { kind: 'first', text: 'First report' });
  assert.deepEqual(marking({ id: 'b', match: 'joined' }, 'a'), { kind: 'joined', text: 'Same as before (joined by the lead)' });
  assert.deepEqual(marking({ id: 'b', match: 'same' }, 'a'), { kind: 'same', text: 'Same as before' });
  assert.deepEqual(marking({ id: 'b' }, 'a'), { kind: '', text: '' });
});

test('earliestOf picks the oldest created', () => {
  const rs = [{ id: 'c', created: ago(H) }, { id: 'b', created: ago(3 * H) }, { id: 'a', created: ago(3 * H) }];
  assert.equal(earliestOf(rs), 'a'); // newest-first list: on a tie the later entry is older
  assert.equal(earliestOf([]), '');
});

test('severityOf normalises', () => {
  assert.equal(severityOf('High'), 'high');
  assert.equal(severityOf('critical'), 'high');
  assert.equal(severityOf('low'), 'low');
  assert.equal(severityOf(''), '');
  assert.equal(severityOf('weird'), '');
});
