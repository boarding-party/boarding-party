# boardingparty worker image

Image used by every boardingparty run. A run is one tiny Session (a Kubernetes pod);
tiny injects Claude Code, tmux and its entrypoint into this image through an init
container and overrides the command, so the Dockerfile sets no ENTRYPOINT or CMD.

Target name: `ghcr.io/boarding-party/worker:<tag>`. Local dev tag: `boardingparty-worker:dev`.

## Contents

- Base: `mcr.microsoft.com/playwright:v1.64.1-noble` (latest stable Playwright image; Ubuntu 24.04, glibc, node 24, Chromium/Firefox/WebKit).
- `git` (tiny's entrypoint refuses images without it), `curl`, `ca-certificates`.
- `@playwright/mcp@0.0.84` installed globally (`playwright-mcp` on PATH).
- Chromium revision 1252 (see "Version choice").
- `WORKDIR /workspace`. In a tiny session `/workspace` is a PVC and the agent runs as uid 1000.

Build: `docker build -t boardingparty-worker:dev worker/`

## Version choice

`@playwright/mcp` has no stable line: every release (0.0.73 to 0.0.84) depends on a
Playwright alpha of the next minor (0.0.84 needs `playwright 1.65.0-alpha-2026-10-30`).
The newest stable base image is `v1.64.1-noble`, whose Chromium is revision 1248, while
the alpha wants revision 1252. Without a fix every tool call fails with
`Browser "chrome-for-testing" is not installed; expected executable at /ms-playwright/chromium-1252/...`.
Passing `--executable-path` to the base's 1248 build also works but the path differs per
architecture (`chrome-linux` vs `chrome-linux-arm64`), so the Dockerfile instead runs
`playwright-mcp install-browser chrome-for-testing` once at build time into
`/ms-playwright` (the base's `PLAYWRIGHT_BROWSERS_PATH`). Cost: about 250 MB extra
(Chromium 1252 plus headless shell). The 1248 browsers remain as unused fallback.
When bumping `@playwright/mcp`, rebuild; the install step follows whatever revision it expects.

## Why `--no-sandbox`

tiny runs the agent as non-root uid 1000 with all capabilities dropped and seccomp
`RuntimeDefault`. Chromium's sandbox needs user namespaces or setuid helpers, which
that profile does not allow, so it is disabled. The pod itself is the isolation boundary.

## MCP server definition

Flag names confirmed with `playwright-mcp --help` (0.0.84):

| Purpose | Flag |
| --- | --- |
| headless | `--headless` |
| in-memory profile | `--isolated` |
| no Chromium sandbox | `--no-sandbox` |
| browser | `--browser chromium` |
| output dir | `--output-dir <path>` |
| viewport | `--viewport-size 1280x800` (also `--device "<name>"`, `--mobile`) |
| storage state | `--storage-state <path>` (isolated sessions) |
| enable video, tracing and storage-state tools | `--caps=devtools,storage` |
| config file | `--config <path>` |

`--save-video` and `--save-trace` DO NOT EXIST in 0.0.84 (the brief's guess was wrong).
Video and tracing are tools, available only with `--caps=devtools`:
`browser_start_video` (optional `filename`, `size`, `fps`, `cursor`), `browser_stop_video`,
`browser_start_tracing`, `browser_stop_tracing`. Files land in `--output-dir`
(`video-<timestamp>.webm`, `traces/trace-<ts>.trace` plus `screencast/` JPEGs). The agent
(or director prompt) must call start before and stop after the scenario, or the files are
not finalised.

```json
{
  "name": "pw",
  "command": "playwright-mcp",
  "args": ["--headless", "--isolated", "--no-sandbox", "--browser=chromium",
           "--output-dir=/workspace/out", "--viewport-size=1280x800", "--caps=devtools"]
}
```

## Verified (local docker, arm64, 2026-10-31)

All as `--user 1000 --cap-drop ALL --security-opt no-new-privileges` (Docker's default seccomp applies):

1. `playwright-mcp --help` lists the flags in the table above.
2. Real MCP stdio session with the args above (output dir `/tmp/out`): `browser_start_video`
   (1280x800), `browser_start_tracing`, `browser_navigate` to a `data:` URL, stop both.
   Chromium launched without sandbox or seccomp errors; produced a `.webm` (7.4 KB),
   a `.trace` with screencast frames, and a page snapshot `.yml`.
3. `git 2.43.0`, `curl 8.5.0`, `node v24.22.1`.

Not yet verified: running under tiny itself (needs push to ghcr and a cluster), and
linux/amd64 (local build was arm64; build the pushed image for the cluster's architecture).

## Live screen (`bp-browser`)

The director launches Playwright MCP as `bp-browser --live-url=<url> [--files-url=<url>] [SETTINGS] <playwright-mcp args>`.
It starts `bp-screen` once per pod (pidfile `/tmp/bp-screen.pid`, log `/tmp/bp-screen.log`), writes the run's
config with `bp-pw-setup`, then `exec`s `bp-gate -- playwright-mcp --config /tmp/bp-pw/pw-config.json ...`.
The base config (`/opt/bp/pw-config.json`) makes Chromium listen for CDP on
`127.0.0.1:9222` (pod-local only). `bp-screen` attaches to the newest page, runs a CDP screencast
(JPEG, quality 60, max 1280) and PUTs frames to the live URL: every 250 ms while the director reports
watchers (`X-Bp-Watchers` > 0), otherwise every 5 s, one request in flight. A 404 or 410 means the run is over
and it stops. The agent never sees frames. Nothing is written to stdout (MCP stdio owns it).

`--files-url` is the director's `/files/<run token>/` list. Before the browser starts, `bp-browser`
downloads every file it lists into `/workspace/files/` (30 s in total, best effort: failures go to
`/tmp/bp-files.log` and never stop the browser from starting).

SETTINGS are the persona's device and accessibility settings, as stable keys from the director's profile
catalogue. `bp-a11y.mjs` turns them into browser settings, for AI runs and replays alike:

| Flag | Keys | Browser |
|---|---|---|
| `--bp-device` | `desktop`, `desktop-large`, `laptop-small`; `iphone-15`, `iphone-15-pro-max`, `pixel-7`, `galaxy-s24`, `ipad`, `ipad-mini` | viewport, or the Playwright device; unset or unknown is a 1280×800 desktop |
| `--bp-assistive` | `zoom` | half the viewport at twice the pixel density: the page lays out as at 200% zoom |
| | `keyboard-only` | `bp-gate` removes click, hover, drag, drop, select, fill, element typing and JS evaluation, and adds `browser_type_focused` (types into the focused element) |
| | `screen-reader` | `--image-responses=omit`: the agent works from the accessibility snapshot. Screenshots are still saved |
| `--bp-vision` | `blurred`, `low-contrast`, `deuteranopia`, `protanopia`, `tritanopia`, `achromatopsia` | Chrome's vision deficiency emulation on every tab (an init page) |
| `--bp-display` | `reduced-motion`, `high-contrast`, `dark` | `reducedMotion`, `forcedColors` with `contrast: more`, `colorScheme: dark` |

`bp-gate` refuses `browser_resize` and `browser_emulate_media` to every persona: the browser is set to the
persona, and the agent may not undo it. A refused call returns an error result that says what to use instead.
`bp-pw-setup` records the settings in `/tmp/bp-pw/settings.json` for `bp-replay --self`. If it fails, `bp-browser`
exits instead of starting a browser that is not the persona's.

Smoke tests: `docker run --rm bp-worker:dev node /opt/bp/smoke/screen-smoke.mjs` (expects `ok N frames`),
`a11y-smoke.mjs` (settings, gate, config) and `browser-smoke.mjs` (bp-browser end to end, needs a writable
`/workspace`).

## Journeys (`bp-replay`)

`bp-replay` runs a recorded journey (an ES module exporting `async function journey({ page, data, bp, expect })`)
with Playwright and no model. Two modes:

- `bp-replay --journey <file> --self [--seed N] [--expect-fail-step N] [SETTINGS]`: an AI run proves the
  journey it wrote. With no SETTINGS (`--device`, `--assistive`, `--vision`, `--display`) it uses the persona's,
  from `/tmp/bp-pw/settings.json`. A fresh browser (no CDP port, so it never clashes with the run's own browser), one JSON line on
  stdout (`{"status":"passed"|"failed","step","stepName","error","seed","inputs","durationMs"}`), exit 0 only when
  the result proves the revision: passed, or failed exactly at `--expect-fail-step`.
- `bp-replay --claim-url URL [--seed N] [SETTINGS]`: a replay run (a tiny `command` session). POSTs the
  one-time claim URL for the run's URLs, fetches the code, restores the persona's browser profile, starts
  `bp-screen` and `bp-console` (Chromium gets the `pw-config.json` flags, CDP on 9222), runs, uploads `trace.zip`,
  `replay-N.webm` and `failed-step.png`, saves the profile back and PUTs the result to the report URL.

The run's URLs hold its token, and anything in the pod can read every process's command line and environment.
So the pod's command carries only the claim, which works once and is spent before the journey starts. The URLs
stay in bp-replay's memory and reach `bp-screen` and `bp-console` on stdin (`--url-stdin`). The journey runs in a
child process given only paths.
Playwright comes from the Playwright MCP install, so `locator.fill` types at the same human pace as `browser_type`.
`expect` is Playwright Test's. Helpers: `bp.step(name, fn)` (numbered from 1; a failure records the step, its name
and a screenshot), `bp.money(n)` (`£1,234.56`), `bp.totalWithVat(lines, rate = 0.2)`, `bp.ukDate(iso)`
(`09/02/2026`), `data.int(min, max)`, `data.pick(list)`, `data.plusEmail(address)` (`info+megan@x` becomes
`info+megan-<seed>@x`). UK test data comes from `bp-data.mjs`: `data.personName()`, `data.businessName({ size })`
(`sole`, `small` or `firm`), `data.address()`, `data.postcode()`, `data.phone()` (Ofcom's drama range only),
`data.vatNumber()`, `data.companyNumber()`, `data.price()`, `data.quantity()`, `data.date()`, `data.dateRange()` and
`data.lineItems()`. Every drawn value is recorded in `inputs`; the same seed draws the same values.

About 30% of seeds carry one or two edge cases: awkward but valid input such as a zero quantity, a leading space or
a 120-character business name. They are listed in `inputs.edges` as `quantity2:zero`. A call takes `exclude: [ids]`
or `edges: false`. Edge decisions come from their own stream, so excluding one never moves any other value. Self
mode with no `--seed` picks a seed without edge cases. A journey has 10 minutes (`BP_REPLAY_TIMEOUT_MS`), each action 15 s.

Smoke tests: `node /opt/bp/smoke/journey-lib-smoke.mjs` and `node /opt/bp/smoke/replay-smoke.mjs` (expect `ok …`).
