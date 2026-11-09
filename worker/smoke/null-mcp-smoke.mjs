// Smoke test: bp-null-mcp answers initialize and lists no tools.
import { spawn } from "node:child_process";

const p = spawn("node", ["/opt/bp/bp-null-mcp.mjs"]);
let out = "";
p.stdout.on("data", (d) => (out += d));
p.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 1, method: "initialize", params: { protocolVersion: "2025-06-18" } }) + "\n");
p.stdin.write(JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }) + "\n");
p.stdin.write(JSON.stringify({ jsonrpc: "2.0", id: 2, method: "tools/list" }) + "\n");
setTimeout(() => {
  p.kill();
  const lines = out.trim().split("\n").map((l) => JSON.parse(l));
  const ok = lines.length === 2 && lines[0].result?.serverInfo?.name === "bp-usage" && lines[1].result?.tools?.length === 0;
  console.log(ok ? "null mcp ok" : "null mcp bad: " + out);
  process.exit(ok ? 0 : 1);
}, 1000);
