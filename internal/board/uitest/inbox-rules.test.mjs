// Tests for the inbox's dates, avatars and message stepping: `node --test 'internal/board/uitest/*.test.mjs'`.
process.env.TZ = 'Europe/London';
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mailDate, mailFull, initial, avatarTone, AVATAR_TONES, position } from '../ui/inbox-rules.js';

const now = new Date('2026-11-18T12:00:00Z');

test('mailDate: time today, day and month this year, with the year before', () => {
  assert.equal(mailDate('2026-11-18T09:10:00Z', now), '09:10');
  assert.equal(mailDate('2026-11-17T23:59:00Z', now), '17 Nov');
  assert.equal(mailDate('2026-01-02T10:00:00Z', now), '2 Jan');
  assert.equal(mailDate('2025-11-06T18:23:00Z', now), '6 Nov 2025');
});

test('mailDate: London summer time decides the day', () => {
  const june = new Date('2026-06-10T12:00:00Z');
  assert.equal(mailDate('2026-06-09T23:30:00Z', june), '00:30');
});

test('mailDate and mailFull: unset or broken times show nothing', () => {
  assert.equal(mailDate('', now), '');
  assert.equal(mailDate('0001-01-01T00:00:00Z', now), '');
  assert.equal(mailFull('nonsense', now), '');
});

test('mailFull: weekday, date and time; year only when not this year', () => {
  assert.equal(mailFull('2026-11-06T18:23:00Z', now), 'Fri 6 Nov, 18:23');
  assert.equal(mailFull('2025-11-06T18:23:00Z', now), 'Thu 6 Nov 2025, 18:23');
});

test('initial: first letter of the name, upper case; ? without one', () => {
  assert.equal(initial('megan Clarke'), 'M');
  assert.equal(initial('  Élodie'), 'É');
  assert.equal(initial(''), '?');
  assert.equal(initial(undefined), '?');
});

test('avatarTone: the same name always gets the same tone', () => {
  assert.equal(avatarTone('Megan Clarke'), avatarTone('Megan Clarke'));
  assert.ok(AVATAR_TONES.includes(avatarTone('Megan Clarke')));
  assert.ok(AVATAR_TONES.includes(avatarTone('')));
  const tones = new Set(['Ann', 'Bob', 'Priya Shah', 'John Smith', 'Megan Clarke', 'Sam'].map(avatarTone));
  assert.ok(tones.size > 1, 'different names spread over the tones');
});

test('position: place in a newest-first list with newer and older ids', () => {
  const list = [{ id: 'a' }, { id: 'b' }, { id: 'c' }];
  assert.deepEqual(position(list, 'b'), { n: 2, of: 3, newer: 'a', older: 'c' });
  assert.deepEqual(position(list, 'a'), { n: 1, of: 3, newer: '', older: 'b' });
  assert.deepEqual(position(list, 'c'), { n: 3, of: 3, newer: 'b', older: '' });
});

test('position: an id not in the list (older than the page) has no place', () => {
  assert.equal(position([{ id: 'a' }], 'zz'), null);
  assert.equal(position(null, 'a'), null);
});
