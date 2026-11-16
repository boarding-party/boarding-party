// Tests for the run studio's time helpers: `node --test internal/board/uitest/`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { placeOnVideo, captionAt, nearest } from '../ui/studio-time.js';

const T = Date.UTC(2026, 10, 16, 10, 0, 0);
const iso = (ms) => new Date(T + ms).toISOString();

test('placeOnVideo clamps', () => {
  assert.equal(placeOnVideo(T + 5000, T, 60), 5);
  assert.equal(placeOnVideo(T - 5000, T, 60), 0); // said before the first frame
  assert.equal(placeOnVideo(T + 90000, T, 60), 60); // after the video ends
  assert.equal(placeOnVideo(NaN, T, 60), 0);
  assert.equal(placeOnVideo(T + 1000, NaN, 60), 0);
  assert.equal(placeOnVideo(T + 1000, T, NaN), 0);
});

test('captionAt shows the latest thought for 6 seconds', () => {
  const n = [
    { at: iso(0), kind: 'thought', text: 'first' },
    { at: iso(1000), kind: 'action', text: 'browser_click: Pay' },
    { at: iso(2000), kind: 'thought', text: 'second' },
    { at: iso(2500), kind: 'action', text: 'browser_type: Name' },
  ];
  assert.equal(captionAt(n, T + 1500).text, 'first');
  assert.equal(captionAt(n, T + 4000).text, 'second'); // actions are not captions
  assert.equal(captionAt(n, T + 7999).text, 'second');
  assert.equal(captionAt(n, T + 8000), null); // 6 s after it was said
  assert.equal(captionAt(n, T - 1), null);
  assert.equal(captionAt([], T), null);
  assert.equal(captionAt(undefined, T), null);
});

test('nearest finds the last item at or before a time', () => {
  const xs = [{ t: 10 }, { t: 20 }, { t: 30 }];
  const key = (x) => x.t;
  assert.equal(nearest(xs, 25, key), 1);
  assert.equal(nearest(xs, 30, key), 2);
  assert.equal(nearest(xs, 5, key), 0);
  assert.equal(nearest(xs, 99, key), 2);
  assert.equal(nearest([], 5, key), 0);
});
