// Narration: what the agent says and does, read from Claude Code's transcript
// lines, for the board's captions. Text blocks are thoughts; tool calls are
// actions. Run URLs carry the run token, so an entry holding a secret is
// dropped (checked on the whole text, before it is cut), and Bash calls show
// their name only. Every URL loses its query, fragment and credentials, as
// in the console, and typed values never appear: an action names its target.
import { cleanText } from './bp-clean.mjs';

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
    let kind = '', text = '', max = 0;
    if (b && b.type === 'text' && typeof b.text === 'string' && b.text.trim()) {
      [kind, text, max] = ['thought', b.text.trim(), MAX_TEXT];
    } else if (b && b.type === 'tool_use' && typeof b.name === 'string') {
      [kind, text, max] = ['action', actionText(b.name, b.input), MAX_ACTION];
    }
    if (!kind || secrets.some((s) => s && text.includes(s))) continue;
    out.push({ at: m.timestamp, kind, text: cleanText(text).slice(0, max) });
  }
  return out;
}

function actionText(name, input) {
  const tool = name.replace(/^mcp__[^_]+(?:_[^_]+)*?__/, '');
  if (tool === 'Bash') return tool;
  const i = input && typeof input === 'object' ? input : {};
  const what = [i.element, i.url, i.key].find((v) => typeof v === 'string' && v.trim());
  return what ? `${tool}: ${what.trim()}` : tool;
}

// capEntries keeps the newest max entries.
export function capEntries(list, max = 2000) {
  return list.length > max ? list.slice(list.length - max) : list;
}
