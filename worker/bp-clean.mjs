// URL cleaning shared by the console recorder and the narration: page URLs
// and messages can carry tokens from emails, so queries, fragments and
// credentials never leave the pod.

// cleanUrl drops the query and fragment: they can carry tokens from emails.
export function cleanUrl(u) {
  if (!u) return '';
  try {
    const p = new URL(u);
    if (p.protocol !== 'http:' && p.protocol !== 'https:') return p.protocol;
    return p.origin + p.pathname;
  } catch {
    return '';
  }
}

// cleanText drops the query and fragment of every URL in a message: stack
// traces and messages quote page URLs, which can carry tokens from emails.
export function cleanText(s) {
  // Match to the next space, quote or angle bracket (never stop at characters a
  // URL may hold), then cut at the first ? or #. No URL parsing: what is cut
  // must not depend on how a parser reads a malformed URL.
  return String(s || '').replace(/(?:https?|wss?):\/\/[^\s"<>]+/gi, cleanToken);
}

// cleanToken cuts one URL-like token at its first ? or #, then strips
// credentials from it and from any URL nested in what is left. When the first
// ? or # falls in the part between a scheme's :// and the next / and that part
// also has an @, credentials and query can't be told apart: everything after
// that :// goes.
const stripCreds = (s) => s.replace(/((?:https?|wss?):\/\/)[^/]*@/gi, '$1');

function cleanToken(u) {
  const q = u.search(/[?#]/);
  if (q >= 0) {
    for (const m of u.matchAll(/(?:https?|wss?):\/\//gi)) {
      const start = m.index + m[0].length;
      if (start > q) break;
      const slash = u.indexOf('/', start);
      const end = slash < 0 ? u.length : slash;
      if (q < end && u.slice(start, end).includes('@')) return stripCreds(u.slice(0, start)) + '[redacted]';
    }
  }
  return stripCreds(q < 0 ? u : u.slice(0, q));
}
