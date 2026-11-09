// A stdio MCP server with no tools. A lead run has no browser, but its pod
// still needs bp-browser to start the usage reporter; bp-browser
// --usage-only execs this in place of Playwright MCP.
import { createInterface } from "node:readline";

const send = (msg) => process.stdout.write(JSON.stringify(msg) + "\n");

createInterface({ input: process.stdin }).on("line", (line) => {
  let req;
  try {
    req = JSON.parse(line);
  } catch {
    return;
  }
  if (req.id === undefined || req.id === null) return; // a notification
  switch (req.method) {
    case "initialize":
      send({ jsonrpc: "2.0", id: req.id, result: {
        protocolVersion: req.params?.protocolVersion ?? "2025-06-18",
        capabilities: { tools: {} },
        serverInfo: { name: "bp-usage", version: "1" },
      } });
      break;
    case "tools/list":
      send({ jsonrpc: "2.0", id: req.id, result: { tools: [] } });
      break;
    case "ping":
      send({ jsonrpc: "2.0", id: req.id, result: {} });
      break;
    default:
      send({ jsonrpc: "2.0", id: req.id, error: { code: -32601, message: "method not found" } });
  }
});
