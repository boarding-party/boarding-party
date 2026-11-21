// Tests for the project editor's summaries: `node --test 'internal/board/uitest/*.test.mjs'`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { scheduleSummary, profileSummary, storyOptionsSummary } from '../ui/form-rules.js';

test('scheduleSummary says when the role runs and how often it gets a fresh AI run', () => {
  assert.equal(scheduleSummary({ activity: '', explore: '' }), 'Runs by hand or hand-off. Fresh AI run weekly.');
  assert.equal(scheduleSummary({ activity: 'every 30 min, 09:00–17:30', explore: 'daily' }), 'Runs every 30 min, 09:00–17:30. Fresh AI run daily.');
  assert.equal(scheduleSummary({ activity: ' daily at 09:00 ', explore: ' ' }), 'Runs daily at 09:00. Fresh AI run weekly.');
});

const fields = [
  { key: 'device', label: 'Device', options: [{ value: 'phone', label: 'Phone' }, { value: 'laptop-small', label: 'Small laptop 1366×768' }] },
  { key: 'age', label: 'Age', options: [{ value: '65+', label: '65+' }] },
  { key: 'techLevel', label: 'Tech confidence', options: [{ value: 'low', label: 'Low' }] },
];

test('profileSummary lists the set values by their labels, in field order', () => {
  assert.equal(profileSummary({ techLevel: 'low', device: 'phone' }, [], fields), 'Phone · Low');
  assert.equal(profileSummary({ age: '65+' }, ['distracted'], fields), '65+ · distracted');
  assert.equal(profileSummary({ device: 'tablet-odd' }, [], fields), 'tablet-odd');
  assert.equal(profileSummary({}, [], fields), '');
});

const base = { model: '', handoffs: [], trigger: { kind: '' }, freshBrowser: false, replay: true };

test('storyOptionsSummary is empty for defaults and names what differs', () => {
  assert.equal(storyOptionsSummary(base), '');
  assert.equal(storyOptionsSummary({ ...base, model: 'claude-sonnet-5-5' }), 'Sonnet 5.5');
  assert.equal(storyOptionsSummary({ ...base, handoffs: ['a', 'b'], trigger: { kind: 'message' } }), 'hands off to 2 · starts on an email');
  assert.equal(storyOptionsSummary({ ...base, trigger: { kind: 'calendar' }, freshBrowser: true, replay: false }),
    'starts on a calendar event · empty browser · no replays');
  assert.equal(storyOptionsSummary({ ...base, model: 'custom-model' }), 'custom-model');
});
