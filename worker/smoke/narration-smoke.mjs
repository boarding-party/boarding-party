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
assert.equal(narrationOf(line([{ type: 'tool_use', name: 'mcp__pw__browser_click', input: { element: 'y'.repeat(900) } }]), [])[0].text.length, 200);

// A token cut in half by the length limit still drops the entry.
assert.deepEqual(narrationOf(line([{ type: 'text', text: 'x'.repeat(495) + 'TOKEN123' }]), ['TOKEN123']), []);

// Typed values never appear: the action names its target only.
assert.deepEqual(narrationOf(line([{ type: 'tool_use', name: 'mcp__pw__browser_type', input: { element: 'Password', ref: 'e3', text: 'hunter2' } }]), []),
  [{ at, kind: 'action', text: 'browser_type: Password' }]);

// URLs lose their query, fragment and credentials, in thoughts and actions.
const urls = narrationOf(line([{ type: 'text', text: 'Opening https://bob:pw@site.test/reset?code=123#x now' },
  { type: 'tool_use', name: 'mcp__pw__browser_navigate', input: { url: 'https://site.test/verify?token=abc' } }]), []);
assert.deepEqual(urls.map((e) => e.text), ['Opening https://site.test/reset now', 'browser_navigate: https://site.test/verify']);

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

// Entries from several transcripts (subagents) come in file order: kept by time.
const mixed = capEntries([{ at: '2026-11-16T10:00:05Z', kind: 'thought', text: 'b' }, { at: '2026-11-16T10:00:01Z', kind: 'thought', text: 'a' }]);
assert.deepEqual(mixed.map((e) => e.text), ['a', 'b']);

// The director takes at most 2 MiB: long non-Latin text is capped by bytes, oldest first.
const wide = Array.from({ length: 2000 }, (_, i) => ({ at: new Date(Date.UTC(2026, 10, 16) + i * 1000).toISOString(), kind: 'thought', text: String(i).padEnd(500, '漢') }));
const fit = capEntries(wide);
assert.ok(Buffer.byteLength(JSON.stringify(fit)) < 2 << 20, 'over the director body limit: ' + Buffer.byteLength(JSON.stringify(fit)));
assert.ok(fit.length > 1000 && fit[fit.length - 1].text.startsWith('1999'), 'kept the wrong end');

console.log('narration smoke ok');
