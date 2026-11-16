// Tests for the run studio's resizable split: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { clampPanel, PANEL_MIN, STAGE_MIN } from '../ui/studio-split.js';

test('clampPanel keeps both sides usable', () => {
  assert.equal(clampPanel(500, 1440), 500);
  assert.equal(clampPanel(100, 1440), PANEL_MIN); // too narrow for the tabs
  assert.equal(clampPanel(1400, 1440), 1440 - STAGE_MIN); // the player keeps its room
  assert.equal(clampPanel(NaN, 1440), null); // nothing remembered: the default width
  assert.equal(clampPanel(600, PANEL_MIN + STAGE_MIN - 50), PANEL_MIN); // a small window: the panel's floor wins
});
