// Build-time patch for Playwright MCP: browser_start_video with no size
// records at the size the screen draws (bp-a11y's videoScale), not shrunk to
// fit 800 px, which the board then stretches into a blur. The agent starts
// recording before it knows the persona's device, so the size can't come
// from the tool call. The patch is tied to the pinned @playwright/mcp
// version: if the target is missing the build fails.
import { readFileSync, writeFileSync } from "node:fs";
import { videoScale } from "./bp-a11y.mjs";

const file = process.argv[2];
let src = readFileSync(file, "utf8");

const from = "const scale = Math.min(1, 800 / Math.max(viewport.width, viewport.height));";
const got = src.split(from).length - 1;
if (got !== 1) {
  console.error(`video-size: expected 1 of ${JSON.stringify(from)}, found ${got}`);
  process.exit(1);
}
src = src.replace(from, "const scale = globalThis.__bpVideoScale(viewport, this.page.browserContext._options.deviceScaleFactor);");
src += `\nglobalThis.__bpVideoScale = ${videoScale.toString()};\n`;
writeFileSync(file, src);
console.log("video-size: patched", file);
