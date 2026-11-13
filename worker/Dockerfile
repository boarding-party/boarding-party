# Worker image for boardingparty runs: a tiny Session pod runs this image.
# tiny injects its own payload (Claude Code, tmux, entrypoint) via an init
# container and overrides the command, so no ENTRYPOINT/CMD is set here.
# Requirements from tiny: glibc, git, non-root (uid 1000) friendly.
FROM mcr.microsoft.com/playwright:v1.64.1-noble

ARG PLAYWRIGHT_MCP_VERSION=0.0.84

# The base image points apt at Ubuntu's mirrors on ubuntu.com, which CI
# runners at times can't resolve (and the arm64 Azure host is gone). If
# the first update errors, retry from a university mirror over HTTPS.
RUN { apt-get update --error-on=any \
      || { sed -i -e 's#http://[a-z.]*ports\.ubuntu\.com/ubuntu-ports#https://mirrors.ocf.berkeley.edu/ubuntu-ports#g' \
                  -e 's#http://[a-z.]*\(archive\|security\)\.ubuntu\.com/ubuntu#https://mirrors.ocf.berkeley.edu/ubuntu#g' \
                  /etc/apt/sources.list.d/*.sources \
           && apt-get update --error-on=any; }; } \
    && apt-get install -y --no-install-recommends git curl ca-certificates zip \
    && rm -rf /var/lib/apt/lists/*

# Global install; browsers come from the base image (PLAYWRIGHT_BROWSERS_PATH=/ms-playwright).
RUN npm i -g @playwright/mcp@${PLAYWRIGHT_MCP_VERSION} && npm cache clean --force

# Personas type like people: patch Playwright MCP so text goes in key by key
# at a human pace (see humanise.mjs). Fails the build if the patch misses.
COPY humanise.mjs /tmp/humanise.mjs
RUN node /tmp/humanise.mjs "$(find "$(npm root -g)/@playwright/mcp" -path '*/playwright-core/lib/coreBundle.js' | head -1)" \
    && rm /tmp/humanise.mjs

# Claude Code's telemetry, error reports and update checks are not part of the
# test: without this they show up as blocked hosts on every run.
ENV CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1

# @playwright/mcp always tracks a Playwright alpha (1.65.0-alpha), which expects a
# newer Chromium revision (1252) than the base image ships (1248, Playwright 1.64.1).
# Fetch the revision it wants into the same browsers dir; --only-shell style trimming
# is not offered by this command. The base browsers stay as a fallback.
ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright
RUN playwright-mcp install-browser chrome-for-testing \
    && chmod -R a+rX /ms-playwright

# Live-screen streaming: bp-browser launches bp-screen and then Playwright MCP.
# /opt/bp is also mounted over by the smoke test (docker run -v worker:/opt/bp).
COPY bp-screen.mjs bp-console.mjs bp-usage.mjs bp-null-mcp.mjs bp-replay.mjs bp-journey-lib.mjs bp-data.mjs pw-config.json /opt/bp/
COPY smoke/ /opt/bp/smoke/
COPY bp-browser /opt/bp/bp-browser
COPY bp-browser /usr/local/bin/bp-browser
# bp-replay runs a recorded journey with no model (self-replays in AI runs,
# and replay runs as tiny command sessions).
RUN printf '#!/bin/sh\nexec node /opt/bp/bp-replay.mjs "$@"\n' > /usr/local/bin/bp-replay
RUN chmod 0755 /opt/bp/bp-browser /usr/local/bin/bp-browser /usr/local/bin/bp-replay && chmod -R a+rX /opt/bp

WORKDIR /workspace
