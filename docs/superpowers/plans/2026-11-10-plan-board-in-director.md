# Board in the director (cloud Part 1) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The director serves the board UI itself, the CLI only proxies, and the in-cluster pieces the cloud will need exist: frame setting from `bp-project`, an agent key screen, an actor header with an audit log, and an importable install package.

**Architecture:** The UI files stay in `internal/board/ui` (embedded) and are served by the director's admin listener behind the existing admin guard; `/api/*` keeps going to the API mux. `boardingparty board` keeps its launch link, cookie and checks but forwards every path. Settings the cloud owns live in an optional ConfigMap `bp-project` that `setup` never writes.

**Tech Stack:** Go (net/http, client-go, controller-runtime client), SQLite, Preact/htm UI, Kubernetes manifests in `deploy/director.yaml`.

**Spec:** docs/superpowers/specs/2026-11-10-cloud-design.md (Part 1, items 1-7, and "Security notes" for `X-BP-Actor`).

## Global Constraints

- Commits: `git -c user.name="Tiny Systems" -c user.email="dev@tinysystems.io" commit`, no Co-Authored-By or Generated trailers; never commit `.idea/`.
- No `replace` directives in go.mod.
- Push to boardingparty main is approved.
- User-facing copy in GB English, plain words, sentence case.
- Never print secrets or tokens (admin token, agent key) in logs, errors, responses or test output.
- Known names (from the spec, exact): Deployment `bp-director`, Secret `bp-director-admin` key `token`, ConfigMap `bp-project` key `frameAncestors`, tiny Secret `tiny-agent-env` key `ANTHROPIC_API_KEY`, annotations `boardingparty.io/manifest-hash` and `boardingparty.io/cli-built`.
- `frameAncestors` default `'none'`.
- tiny's public wrapper for `internal/runtimeinstall` (spec Part 1 item 7, second half) is in the tiny repository and is left to the cloud plan; this plan does only boardingparty's `pkg/install`.

## Review Focus

1. `frameAncestors` holding something other than origins (`'none'; script-src *`, newlines, `http://` origins): the director must fall back to `'none'` and never splice it into the CSP. Test in Task 2.
2. A browser sending its own `X-BP-Actor` through the CLI board: stripped, so it can't forge who did what. Test in Task 3.
3. A new CLI against a director that predates this plan (404 on `/`): the board shows "This director is too old to serve the board: run boardingparty setup" instead of a bare 404. Test in Task 3.
4. `tiny-agent-env` that already holds `CLAUDE_CODE_OAUTH_TOKEN`: saving an API key keeps the other keys; GET reports the key as set without ever returning it. Tests in Task 4.
5. An actor header longer than 254 bytes or with control characters: refused with 400, never stored. Test in Task 5.

---

### Task 1: Relative URLs in the UI

**Files:**
- Modify: `internal/board/ui/index.html`, `internal/board/ui/lib.js`, `internal/board/ui/app.js`, `internal/board/ui/*.js` (every `/api/...` and `/styles.css`, `/app.js` reference)
- Test: `internal/board/server_test.go`

**Interfaces:**
- Produces: UI files that use `api/...`, `styles.css`, `app.js` with no leading slash; `api(path)` in lib.js accepts `api/...`.

- [ ] **Step 1: Write the failing test** `TestUIUsesRelativeURLs` in `internal/board/server_test.go`: walk `UI()`, for every `.js` and `.html` file fail when the content matches the regexp `` ['"`(]/(api|styles\.css|app\.js|[a-z]+\.js)\b `` and report file and match.
- [ ] **Step 2: Run** `go test ./internal/board/ -run TestUIUsesRelativeURLs`. Expected: FAIL listing about 43 matches.
- [ ] **Step 3: Replace** each leading `/` (`'/api/...'` → `'api/...'`, `` `/api/...` `` → `` `api/...` ``, `href="/styles.css"` → `href="styles.css"`, `src="/app.js"` → `src="app.js"`). Hash routes (`#/runs`) stay as they are.
- [ ] **Step 4: Run** `go test ./internal/board/` and `node --check` on each changed JS file. Expected: PASS, no syntax errors.
- [ ] **Step 5: Commit** `refactor(board): relative URLs so the UI works at any path`.

### Task 2: The director serves the board, with the frame setting

**Files:**
- Modify: `internal/board/server.go` (export the static serving), `cmd/director/main.go` (`newServers`, config), `deploy/director.yaml` (env from `bp-project`)
- Create: `internal/board/frame.go`
- Test: `internal/board/frame_test.go`, `cmd/director/main_test.go`, `internal/cli/setup_test.go`

**Interfaces:**
- Produces:
  - `func board.Static(ui fs.FS) http.Handler`: GET/HEAD only, files or `index.html` fallback, `Cache-Control: no-cache` (the logic now in `(*Handler).static`/`serveFile`, which the CLI handler stops using in Task 3).
  - `func board.FrameAncestors(raw string) string`: returns a CSP source list; `'none'` unless `raw` is one or more space-separated `https://host[:port]` origins (host letters, digits, dots, hyphens), max 8.
  - `func board.UIHeaders(h http.Header, frameAncestors string)`: like `SecurityHeaders` but with that `frame-ancestors`, and `X-Frame-Options: DENY` only when it is `'none'`.
  - director config field `FrameAncestors string` from env `BP_FRAME_ANCESTORS`.

- [ ] **Step 1: Write failing tests.**
  - `TestFrameAncestors` table: `""`→`'none'`; `"'none'"`→`'none'`; `"https://cloud.boardingparty.io"`→same; `"https://a.io https://b.io:8443"`→same; `"'none'; script-src *"`→`'none'`; `"http://a.io"`→`'none'`; `"https://a.io\nX: y"`→`'none'`; nine origins→`'none'`.
  - `TestUIHeadersFrame`: with `'none'`, CSP contains `frame-ancestors 'none'` and `X-Frame-Options` is `DENY`; with `https://c.io`, CSP contains `frame-ancestors https://c.io` and there is no `X-Frame-Options`.
  - `TestAdminServesBoard` in `cmd/director/main_test.go`: build `newServers` with a token; `GET /` with the bearer and Host `127.0.0.1:8081` returns 200 with `<script type="module" src="app.js">` and the CSP from `UIHeaders`; `GET /` without the bearer returns 401; `GET /api/runs` with the bearer still returns JSON.
  - `TestManifestReadsFrameFromProject` in `internal/cli/setup_test.go`: the rendered director Deployment has env `BP_FRAME_ANCESTORS` from `configMapKeyRef{name: bp-project, key: frameAncestors, optional: true}`, and no rendered object is a ConfigMap named `bp-project`.
- [ ] **Step 2: Run** `go test ./internal/board/ ./cmd/director/ ./internal/cli/ -run 'Frame|AdminServesBoard|ReadsFrame'`. Expected: FAIL (undefined functions, missing env).
- [ ] **Step 3: Implement.** In `newServers`, the admin handler becomes a mux: `/api` and `/api/` to the API handler as today; everything else to `board.Static(ui)` wrapped so `UIHeaders(w.Header(), board.FrameAncestors(cfg.FrameAncestors))` is set first, and the whole mux sits behind the same admin guard (export a guard constructor from `internal/api` if needed: `api.Guard(token string, next http.Handler) http.Handler`). Add the env entry to `deploy/director.yaml`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(director): serve the board on the admin listener; frame-ancestors from bp-project`.

### Task 3: The CLI board proxies every path

**Files:**
- Modify: `internal/board/server.go` (`Config` loses `UI`; `ServeHTTP` proxies all paths), `internal/board/proxy.go` (strip `X-BP-Actor`; old-director message), `internal/cli/board.go` (stop passing `UI`)
- Test: `internal/board/server_test.go`

**Interfaces:**
- Consumes: Task 2's director, which answers `/` with the UI.
- Produces: `board.Config{Port, Proxy, LinkTTL, Now}` (no `UI`). The CLI's own responses (401, 403, login redirect) keep `SecurityHeaders`; proxied responses keep the CLI's `SecurityHeaders` too (local board: `frame-ancestors 'none'`).

- [ ] **Step 1: Write failing tests.**
  - Replace `TestStaticFilesAndSPAFallback` with `TestEveryPathIsProxied`: with a session, `GET /`, `GET /app.js`, `GET /api/runs` all reach the fake director (record paths: `/`, `/app.js`, `/api/runs`).
  - `TestProxyDropsActorHeader`: a browser request with `X-BP-Actor: mallory@x.io` reaches the director without that header.
  - `TestOldDirectorMessage`: fake director answers 404 for `GET /`; the board answers 502 with body containing `This director is too old to serve the board: run boardingparty setup`. A 404 on `/api/...` passes through unchanged.
  - Keep `TestEmbeddedUIHasIndex` (the director uses the embed).
- [ ] **Step 2: Run** `go test ./internal/board/`. Expected: FAIL.
- [ ] **Step 3: Implement.** Remove the `/api` prefix check and `h.static` from `ServeHTTP`; always clear headers and call `cfg.Proxy`. Add `X-BP-Actor` to the proxy's stripped-header list. In `ModifyResponse`, when the request path is `/` and status is 404, replace the response with the 502 message above. Update `internal/cli/board.go`.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat(board): the CLI proxies every path; the UI comes from the director`.

### Task 4: Agent key screen

**Files:**
- Create: `internal/agentkey/agentkey.go` (Secret access), `internal/api/agentkey.go` (routes)
- Modify: `internal/api/api.go` (routes, server field), `cmd/director/main.go` (wire), `deploy/director.yaml` (Role), `internal/cli/setup.go` (preflight), `internal/runs/runs.go` (start check), `internal/board/ui/project.js` (screen)
- Test: `internal/agentkey/agentkey_test.go`, `internal/api/agentkey_test.go`, `internal/cli/setup_test.go`, `internal/runs/runs_test.go`

**Interfaces:**
- Produces:
  - `type agentkey.Status struct { Set bool; Kind string }` with JSON `{"set":bool,"kind":"api-key"|"subscription"|""}`; `api-key` when `ANTHROPIC_API_KEY` is present and non-empty, else `subscription` when `CLAUDE_CODE_OAUTH_TOKEN` is, else unset.
  - `type agentkey.Store interface { Status(ctx) (Status, error); SetAPIKey(ctx, key string) error }`; `agentkey.NewSecretStore(c client.Client, namespace string) Store` on Secret `tiny-agent-env`; `SetAPIKey` creates the Secret if missing and otherwise updates only `ANTHROPIC_API_KEY`, keeping other keys.
  - Routes: `GET /api/agent-key` → Status; `PUT /api/agent-key` body `{"key":"..."}` → 204. Refuses (400) a key that is empty, longer than 300 bytes, has whitespace or doesn't start with `sk-ant-`.
  - `runs.Service.AgentKey agentkey.Store` (nil: no check). `Start` returns `ErrInvalid` with `set the Anthropic key in Project → Agent key` when `Status().Set` is false.

- [ ] **Step 1: Write failing tests.**
  - `TestSetAPIKeyKeepsOtherKeys` (fake client with `tiny-agent-env` holding `CLAUDE_CODE_OAUTH_TOKEN`): after `SetAPIKey("sk-ant-abc")`, both keys present; Status is `{true, "api-key"}`.
  - `TestSetAPIKeyCreatesSecret`: no Secret; after `SetAPIKey`, Secret exists with the key.
  - `TestAgentKeyRoutes`: GET body never contains the key string; PUT with `"bad key"`, `""`, `"sk-ant-"+301 bytes` → 400; valid → 204.
  - `TestPreflightMissingAgentSecretWarns`: missing `tiny-agent-env` produces a `!` warning line, not a `✗` failure, and `ensureDirector` goes on to install.
  - `TestStartNeedsAgentKey`: with an `AgentKey` fake reporting unset, `Start` fails with `ErrInvalid` and the message above; reporting set, it starts.
- [ ] **Step 2: Run** the five tests. Expected: FAIL.
- [ ] **Step 3: Implement**, mirroring `internal/inbox/secret.go`. Role in `deploy/director.yaml`: add `tiny-agent-env` to the `resourceNames` of the `get, update` secrets rule (keep `create` as it is). Board: a "Agent key" card on the project page with the status ("Set: API key", "Set: subscription token", "Not set"), a password input and "Save key"; on save show "Saved." and refresh the status; never fill the input with the stored value.
- [ ] **Step 4: Run** `go test ./...` and `node --check internal/board/ui/project.js`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: agent key screen; the director writes tiny-agent-env; runs wait for a key`.

### Task 5: Actor header and audit log

**Files:**
- Create: `internal/store/audit.go`, `internal/api/actor.go`
- Modify: `internal/store/schema.sql`, `internal/api/api.go` (middleware; audit calls in handlers)
- Test: `internal/store/audit_test.go`, `internal/api/actor_test.go`

**Interfaces:**
- Produces:
  - Table `audit(id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, actor TEXT NOT NULL, action TEXT NOT NULL, subject TEXT NOT NULL DEFAULT '')`.
  - `func (s *Store) Audit(actor, action, subject string, at time.Time) error`; `func (s *Store) AuditLog(limit int) ([]AuditEntry, error)` newest first, `AuditEntry{At time.Time; Actor, Action, Subject string}` with JSON `at, actor, action, subject`.
  - `func actorOf(ctx) string`: the validated `X-BP-Actor` (≤254 bytes, no control characters, else 400 before the handler runs), or `"local"` when absent.
  - `GET /api/audit?limit=N` (default 100, max 500).
  - Actions logged (exact strings): `project.save`, `run.start`, `lead.wake`, `child.adopt`, `child.delete`, `issue.triage`, `host.allow`, `mailbox.save`, `mailbox.delete`, `agentkey.save`; subject is the persona, run, child, issue id or host, never a secret.

- [ ] **Step 1: Write failing tests.**
  - `TestAuditNewestFirst`: three entries come back newest first with the given fields.
  - `TestActorHeader`: `PUT /api/project` with `X-BP-Actor: ann@x.io` writes `{actor: ann@x.io, action: project.save}`; without the header the actor is `local`; a 255-byte header or one with `\n` is 400 and writes nothing.
  - `TestAgentKeySaveAuditedWithoutKey`: after `PUT /api/agent-key`, the audit entry's subject is empty and the log never contains the key.
- [ ] **Step 2: Run** them. Expected: FAIL.
- [ ] **Step 3: Implement** the middleware inside the admin mux (after the guard), store methods, and one `Audit` call after each successful state change listed above.
- [ ] **Step 4: Run** `go test ./...`. Expected: PASS.
- [ ] **Step 5: Commit** `feat: X-BP-Actor and an audit log of board actions`.

### Task 6: Importable install package

**Files:**
- Create: `pkg/install/` (moved from `internal/cli/setup.go`, `internal/cli/forward.go`, `internal/cli/adminsecret.go`; their tests move with them)
- Modify: `internal/cli/*.go` (call `pkg/install`)

**Interfaces:**
- Produces (exported names, behaviour unchanged):
  - constants `install.DirectorDeployment = "bp-director"`, `install.AdminSecret = "bp-director-admin"`, `install.ProjectConfigMap = "bp-project"`, `install.AgentSecret = "tiny-agent-env"`, `install.ManifestHashAnnotation`, `install.CLIBuiltAnnotation`;
  - `type install.Deps`, `type install.Options` (today's `setupDeps`, `setupOpts`, fields exported);
  - `func install.EnsureDirector(ctx, Deps, Options, io.Writer) (adminToken string, err error)`;
  - `func install.Preflight(ctx, kube, ext, ns string, out io.Writer) (failed, notSetUp bool)`;
  - `func install.InstalledVersion(dep *appsv1.Deployment) (tag string, cliBuilt time.Time)`;
  - the port-forward type now in `forward.go`, exported as `install.Forwarder` with its constructor.
- [ ] **Step 1: Move** the files with `git mv`, rename to exported identifiers, fix imports in `internal/cli`. No behaviour change.
- [ ] **Step 2: Run** `go build ./... && go vet ./... && go test ./...`. Expected: PASS, same test count as before the move (record it first with `go test ./... -json | grep -c '"Action":"pass","Test"'`).
- [ ] **Step 3: Add** `TestInstalledVersion` in `pkg/install`: a Deployment with image `...director:sha-abc1234` and annotation `2026-11-10T09:00:00Z` returns `sha-abc1234` and that time; missing annotation returns zero time.
- [ ] **Step 4: Run** `go test ./pkg/install/`. Expected: PASS.
- [ ] **Step 5: Commit** `refactor: move install, preflight and port-forward into pkg/install for the cloud`.

### Finish

- [ ] Push main, wait for CI images, deploy with `setup --image-tag sha-<head>` to `boardingparty-test`, `go install ./cmd/boardingparty`, restart the board and check: the board loads from the director (`GET /` proxied), the Agent key card shows "Set: subscription token" for the current install, and an action appears in `GET /api/audit` with actor `local`.
