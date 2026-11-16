// Checks the narration extractor: what the agent says and does, read from
// Claude Code's transcript lines, never carrying the run token.
import assert from 'node:assert/strict';
import { narrationOf, capEntries } from '../bp-narration.mjs';

const at = '2026-11-16T10:00:01.000Z';
const line = (content, extra = {}) =>
  JSON.stringify({ type: 'assistant', uuid: 'u1', timestamp: at, message: { id: 'm1', content }, ...extra });

// A text block is a thought, at the line's time.
assert.deepEqual(narrationOf(line([{ type: 'text', text: "I'll try the postcode field" }]), []),
  [{ at, kind: 'thought', text: "I'll try the postcode field" }]);

// A browser tool call is an action: the tool without its server prefix, and what it acts on.
assert.deepEqual(narrationOf(line([{ type: 'tool_use', name: 'mcp__pw__browser_click', input: { element: 'Pay button', ref: 'e12' } }]), []),
  [{ at, kind: 'action', text: 'browser_click: Pay button' }]);

// Bash shows its name only: its commands carry the upload URL.
assert.deepEqual(narrationOf(line([{ type: 'tool_use', name: 'Bash', input: { command: 'curl -T a http://bp-director.x.svc:8080/artifacts/TOKEN123/a' } }]), ['TOKEN123']),
  [{ at, kind: 'action', text: 'Bash' }]);

// token never in narration: a thought quoting a run URL is dropped.
const quoted = narrationOf(line([{ type: 'text', text: 'Fetching http://bp-director.x.svc:8080/files/TOKEN123/ now' },
  { type: 'tool_use', name: 'mcp__pw__browser_navigate', input: { url: 'http://bp-director.x.svc:8080/files/TOKEN123/x' } }]), ['TOKEN123']);
assert.ok(quoted.every((e) => !e.text.includes('TOKEN123')), 'token leaked: ' + JSON.stringify(quoted));

// Long text is cut to 500 characters, an action summary to 200.
assert.equal(narrationOf(line([{ type: 'text', text: 'x'.repeat(900) }]), [])[0].text.length, 500);
assert.equal(narrationOf(line([{ type: 'tool_use', name: 'mcp__pw__browser_type', input: { text: 'y'.repeat(900) } }]), [])[0].text.length, 200);

// Nothing from user lines, tool results, thinking, blank text or broken JSON.
assert.deepEqual(narrationOf(JSON.stringify({ type: 'user', timestamp: at, message: { content: [{ type: 'text', text: 'hi' }] } }), []), []);
assert.deepEqual(narrationOf(line([{ type: 'tool_result', content: 'ok' }]), []), []);
assert.deepEqual(narrationOf(line([{ type: 'thinking', thinking: 'secret plan' }]), []), []);
assert.deepEqual(narrationOf(line([{ type: 'text', text: '   ' }]), []), []);
assert.deepEqual(narrationOf('{not json', []), []);
assert.deepEqual(narrationOf(line([{ type: 'text', text: 'no time' }], { timestamp: undefined }), []), []);

// keeps newest 2000
const many = Array.from({ length: 2500 }, (_, i) => ({ at, kind: 'thought', text: String(i) }));
const kept = capEntries(many);
assert.equal(kept.length, 2000);
assert.equal(kept[0].text, '500');
assert.equal(kept[1999].text, '2499');

console.log('narration smoke ok');
