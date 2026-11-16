// Narration: what the agent says and does, read from Claude Code's transcript
// lines, for the board's captions. Text blocks are thoughts; tool calls are
// actions. Run URLs carry the run token, so an entry holding a secret is
// dropped, and Bash calls show their name only.

const MAX_TEXT = 500;
const MAX_ACTION = 200;

// narrationOf returns the entries in one transcript line.
export function narrationOf(line, secrets) {
  let m;
  try {
    m = JSON.parse(line);
  } catch {
    return [];
  }
  const content = m && m.type === 'assistant' && m.message && m.message.content;
  if (!Array.isArray(content) || typeof m.timestamp !== 'string' || !m.timestamp) return [];
  const out = [];
  for (const b of content) {
    let e = null;
    if (b && b.type === 'text' && typeof b.text === 'string' && b.text.trim()) {
      e = { at: m.timestamp, kind: 'thought', text: b.text.trim().slice(0, MAX_TEXT) };
    } else if (b && b.type === 'tool_use' && typeof b.name === 'string') {
      e = { at: m.timestamp, kind: 'action', text: actionText(b.name, b.input).slice(0, MAX_ACTION) };
    }
    if (e && !secrets.some((s) => s && e.text.includes(s))) out.push(e);
  }
  return out;
}

function actionText(name, input) {
  const tool = name.replace(/^mcp__[^_]+(?:_[^_]+)*?__/, '');
  if (tool === 'Bash') return tool;
  const i = input && typeof input === 'object' ? input : {};
  const what = [i.element, i.url, i.text, i.key].find((v) => typeof v === 'string' && v.trim());
  return what ? `${tool}: ${what.trim()}` : tool;
}

// capEntries keeps the newest max entries.
export function capEntries(list, max = 2000) {
  return list.length > max ? list.slice(list.length - max) : list;
}
