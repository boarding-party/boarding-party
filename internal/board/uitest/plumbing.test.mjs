// Tests for hiding the agent's housekeeping steps: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { withoutPlumbing } from '../ui/plumbing.js';

const a = (text) => ({ at: '2026-11-16T15:25:00Z', kind: 'action', text });

test('housekeeping steps are hidden', () => {
  const names = ['ToolSearch', 'set_title', 'next_assignment', 'browser_start_video', 'browser_stop_video',
    'browser_start_tracing', 'browser_stop_tracing', 'browser_snapshot', 'TodoWrite', 'Read'];
  assert.deepEqual(withoutPlumbing(names.map(a)), []);
});

test('what the persona does on the site stays', () => {
  const keep = [a('browser_navigate: https://smesolutions.uk/'), a('browser_click: Open Invoice Generator link'),
    a('browser_find'), a('browser_take_screenshot'), a('browser_press_key: PageDown'), a('Bash')];
  assert.deepEqual(withoutPlumbing(keep), keep);
});

test('thoughts stay, even when they name a tool', () => {
  const t = { at: '2026-11-16T15:25:00Z', kind: 'thought', text: 'set_title' };
  assert.deepEqual(withoutPlumbing([t]), [t]);
});

test('a housekeeping tool with a target is still hidden', () => {
  assert.deepEqual(withoutPlumbing([a('set_title: Checking the invoice')]), []);
});
