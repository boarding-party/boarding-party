#!/bin/sh
# Copies Playwright's trace viewer into the board (internal/board/ui/trace),
# from the playwright-core that the worker's @playwright/mcp bundles: a
# viewer older than the traces it reads cannot open them.
# Usage: scripts/vendor-traceviewer.sh [mcp-version]  (default: worker/Dockerfile's)
set -eu
cd "$(dirname "$0")/.."
mcp="${1:-$(sed -n 's/^ARG PLAYWRIGHT_MCP_VERSION=//p' worker/Dockerfile)}"
core=$(npm view "@playwright/mcp@$mcp" 'dependencies.playwright-core')
[ -n "$core" ] || { echo "no playwright-core dependency for @playwright/mcp@$mcp" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
(cd "$tmp" && npm pack --silent "playwright-core@$core" >/dev/null && tar -xzf playwright-core-*.tgz)
dst=internal/board/ui/trace
rm -rf "$dst"
mkdir -p "$dst"
cp -R "$tmp/package/lib/vite/traceViewer/." "$dst/"
cp "$tmp/package/LICENSE" "$dst/LICENSE"
[ -f "$tmp/package/NOTICE" ] && cp "$tmp/package/NOTICE" "$dst/NOTICE"
printf '@playwright/mcp %s\nplaywright-core %s\n' "$mcp" "$core" > "$dst/VERSION"
echo "trace viewer from playwright-core $core (for @playwright/mcp $mcp)"
