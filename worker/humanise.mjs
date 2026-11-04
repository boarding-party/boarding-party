// Build-time patch for Playwright MCP: text goes into fields one key at a
// time at a human pace instead of appearing all at once. Playwright MCP has
// no setting for this (browser_type's "slowly" types with no delay), so the
// three places that put text into a field are rewritten to call
// __bpHumanType, defined at the end of the bundle. The patch is tied to the
// pinned @playwright/mcp version: if any target is missing the build fails
// rather than shipping a browser that types at machine speed.
import { readFileSync, writeFileSync } from "node:fs";

const file = process.argv[2];
let src = readFileSync(file, "utf8");

const targets = [
  // browser_type (default path) and browser_fill_form textboxes
  ["await locator3.fill(secret.value, tab2.actionTimeoutOptions);", 2],
  // browser_type with slowly: true
  ["await locator3.pressSequentially(secret.value, tab2.actionTimeoutOptions);", 1],
];
for (const [from, want] of targets) {
  const got = src.split(from).length - 1;
  if (got !== want) {
    console.error(`humanise: expected ${want} of ${JSON.stringify(from)}, found ${got}`);
    process.exit(1);
  }
  src = src.replaceAll(from, "await globalThis.__bpHumanType(locator3, secret.value, tab2.actionTimeoutOptions);");
}

src += `
globalThis.__bpHumanType = async (locator, value, opts) => {
  const typeable = ["text", "email", "password", "search", "tel", "url", "number"];
  const kind = await locator
    .evaluate((e) => (e.isContentEditable || e.tagName === "TEXTAREA" ? "text" : e.type || "text"), undefined, opts)
    .catch(() => "other");
  if (!typeable.includes(kind)) return locator.fill(value, opts); // dates, ranges, colours: no keys to press
  await locator.fill("", opts);
  for (const ch of value) {
    await locator.pressSequentially(ch, opts);
    let ms = 60 + Math.random() * 120;
    if (/[\\s.,;:!?@]/.test(ch)) ms += Math.random() * 250;
    await new Promise((r) => setTimeout(r, ms));
  }
};
`;
writeFileSync(file, src);
console.log("humanise: patched " + file);
