# Cloud skeleton Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A team can sign in to the cloud, link a namespace that already runs a director, and open that project's own board in the browser through the cloud.

**Architecture:** One Go service (`boarding-party/cloud`, private) with Postgres. It serves the cloud pages on the cloud origin and every project's board on `<slug>.p.<domain>`, proxied over an on-demand port-forward to `bp-director:8081` in the project's namespace. The cloud reaches clusters with its own ServiceAccount token, created once from a pasted kubeconfig; it never stores the kubeconfig.

**Tech Stack:** Go 1.26, `net/http`, `html/template`, `jackc/pgx/v5`, `k8s.io/client-go`, `github.com/boarding-party/boardingparty/pkg/install` (pinned version, no `replace`).

**Spec:** `docs/superpowers/specs/2026-11-10-cloud-design.md` (Part 2, Order of work step 2).

## Global Constraints

- Commits as `Tiny Systems <dev@tinysystems.io>` only; no trailers.
- No `replace` directives. The cloud pins a tagged open-source version.
- The cloud never stores a customer's kubeconfig, Anthropic key or site passwords. It stores one sealed ServiceAccount token per cluster.
- The admin token from `bp-director-admin` lives in memory only, for one forward's life.
- Anything about runs, bugs, personas or mail stays in the namespace; no cloud copies.
- Roles: `owner`, `member`. Owners connect and disconnect clusters, link and unlink projects, invite and remove people. Members open projects.
- Port-forward closes after 10 minutes without requests.
- Enter codes are single-use and expire in 60 seconds; invites expire in 7 days; magic links in 15 minutes.
- GB English in all copy. Compact UI in the board's visual system (Instrument Sans, `.card`, `.btn`, `.tag`).
- Never print tokens, sealed values or the seal key in logs.

## Review Focus

1. A user removed from a team keeps an open board tab: the next proxied request must fail within 30 s (membership cache TTL), not at cookie expiry.
2. A board response that tries to set a cookie, or a request that carries `X-BP-Actor`, `Authorization` or the cloud cookie: none of these cross the proxy.
3. A cluster that is unreachable: the project page names the cluster and the last time it was reached; other projects keep working.
4. Two tabs opening the same project at once: one port-forward, not two.
5. A slug that collides with a reserved name (`www`, `api`, `cloud`) or another project.

---

### Task 0: Tag the open-source module

**Files:** none (boardingparty repo).

- [ ] **Step 1:** After the director at the tagged commit is deployed and verified, tag `v0.1.0` on boardingparty `main` and push the tag.
- [ ] **Step 2:** `GOPROXY=https://proxy.golang.org go list -m github.com/boarding-party/boardingparty@v0.1.0`
  Expected: `github.com/boarding-party/boardingparty v0.1.0`.

### Task 1: Repository, config and schema

**Files:**
- Create: `go.mod` (`module github.com/boarding-party/cloud`), `cmd/cloud/main.go`, `internal/config/config.go`, `internal/db/db.go`, `internal/db/schema.sql`, `internal/db/db_test.go`, `.github/workflows/ci.yml`, `Dockerfile`.

**Interfaces:**
- Produces: `config.Load() (Config, error)` with fields `Listen`, `DatabaseURL`, `Origin` (e.g. `https://cloud.boardingparty.io`), `ProjectDomain` (e.g. `p.boardingparty.io`), `SealKey []byte` (32 bytes, base64 env `CLOUD_SEAL_KEY`), `SMTP` settings. `db.Open(ctx, url) (*db.DB, error)` applies `schema.sql` idempotently.

- [ ] **Step 1: Failing test** `TestOpenAppliesSchemaTwice`: open the test database twice; both succeed; table `accounts` exists. Tests use `CLOUD_TEST_DATABASE_URL` and skip when unset; CI runs a `postgres:17` service.
- [ ] **Step 2:** Run `go test ./internal/db/`. Expected: FAIL (`db.Open` undefined).
- [ ] **Step 3:** Schema, exactly these tables (all ids `text` primary keys, times `timestamptz`):
  `accounts(id, name, created)`, `users(id, email unique, name, created)`, `memberships(account, user_id, role check in ('owner','member'), primary key(account,user_id))`, `invites(id, account, email, role, token_hash unique, expires, created_by)`, `login_links(token_hash primary key, email, expires)`, `sessions(id_hash primary key, user_id, expires)`, `clusters(id, account, name, kind check in ('customer','shared'), api_server, ca_pem, token_sealed bytea, seal_key_id, connected_by, last_reached, created)`, `projects(id, account, cluster, namespace, slug unique, name, staging_url, version_seen, created, unique(cluster, namespace))`, `enter_codes(code_hash primary key, project, user_id, expires)`, `access_log(at, user_id, project, cluster, action)`.
- [ ] **Step 4:** Run tests. Expected: PASS.
- [ ] **Step 5: Commit** `feat: service skeleton, config and schema`.

### Task 2: Sign-in by email link

**Files:** Create `internal/auth/{auth.go,mail.go,auth_test.go}`.

**Interfaces:**
- Produces: `auth.Service{DB, Mail Sender, Now}`; `Sender interface{ Send(ctx, to, subject, body string) error }`; `(*Service).SendLink(ctx, email) error`; `(*Service).Redeem(ctx, token) (sessionID string, err error)`; `(*Service).User(r *http.Request) (User, bool)` reading cookie `bp_cloud` (host-only, Secure, HttpOnly, SameSite=Lax, 30 days).
- Tokens: 32 random bytes, hex; only the SHA-256 is stored.

- [ ] **Step 1: Failing tests:** `TestLinkSignsInOnce` (redeem twice: second fails), `TestLinkExpires` (16 minutes later fails), `TestFirstSignInCreatesAccount` (new email gets an account named after the email's domain with them as owner), `TestUnknownTokenFails`.
- [ ] **Step 2:** Run; expect FAIL.
- [ ] **Step 3:** Implement. The SMTP sender reuses the smesolutions settings shape (host, port, user, password from a Secret). A user invited before first sign-in joins the inviting account instead of getting a new one (Task 3 owns the invite lookup; here call `invites.AcceptPending(ctx, email)` stub returning false until Task 3).
- [ ] **Step 4:** Run; expect PASS. **Step 5: Commit** `feat: sign in by email link`.

### Task 3: Teams and invites

**Files:** Create `internal/team/{team.go,team_test.go}`.

**Interfaces:**
- Produces: `team.Role(ctx, account, user) (string, error)`; `Invite(ctx, account, by User, email, role) (link string, err error)` (owner only); `Accept(ctx, token, user) error`; `Remove(ctx, account, by, user) error` (owner only; refuses removing the last owner); `AcceptPending(ctx, email) (bool, error)`.

- [ ] **Step 1: Failing tests:** `TestMemberCannotInvite`, `TestInviteExpiresAfterSevenDays`, `TestLastOwnerStays`, `TestAcceptJoinsWithRole`.
- [ ] **Step 2–4:** fail, implement, pass. Invite mail says who invited and to which account.
- [ ] **Step 5: Commit** `feat: teams with invites and owner/member roles`.

### Task 4: Sealing cluster tokens

**Files:** Create `internal/seal/{seal.go,seal_test.go}`.

**Interfaces:**
- Produces: `seal.New(keys map[string][]byte, current string) (*Sealer, error)`; `Seal(plain []byte) (ct []byte, keyID string, err error)`; `Open(ct []byte, keyID string) ([]byte, error)`. AES-256-GCM, random 12-byte nonce prefixed; additional data = cluster id, passed by the caller (`SealFor(clusterID, plain)`, `OpenFor(clusterID, ct, keyID)`).

- [ ] **Step 1: Failing tests:** `TestRoundTrip`, `TestWrongClusterFails` (ciphertext moved to another row does not open), `TestOldKeyStillOpens` (rotation: new current key, old ciphertext opens with its key id).
- [ ] **Step 2–4.** **Step 5: Commit** `feat: seal cluster tokens with AES-GCM and key ids`.

### Task 5: Connecting a cluster

**Files:** Create `internal/cluster/{connect.go,rbac.go,client.go,connect_test.go}`.

**Interfaces:**
- Consumes: `seal.Sealer`, `install.VerifyDirector`.
- Produces: `cluster.Connect(ctx, account, by User, name string, kubeconfig []byte, namespaces []string) (Cluster, error)`; `cluster.Namespaces(ctx, kubeconfig) ([]string, error)` (namespaces with a `bp-director` Deployment, for the picker); `cluster.Config(ctx, c Cluster) (*rest.Config, error)` (opens the sealed token); `cluster.Disconnect(ctx, c, by) error`.

Ruling (spec leaves RBAC to the builder): in the skeleton the cloud gets rights only in the namespaces the owner picks while the pasted kubeconfig is in hand. Linking another namespace later means pasting a kubeconfig again ("Add namespaces"). The cloud gets no cluster-wide rights until provider connect (step 3 of the order of work) creates namespaces itself. Cost if wrong: one extra paste per new namespace.

Objects created with the pasted kubeconfig, then the kubeconfig is dropped:
- Namespace `boardingparty-system`; ServiceAccount `boardingparty-cloud`; Secret `boardingparty-cloud-token` of type `kubernetes.io/service-account-token`.
- ClusterRole `boardingparty-cloud-project`: `secrets` get with `resourceNames: [bp-director-admin]`; `pods` list, get; `pods/portforward` create; `deployments` get (apps); `configmaps` get, create, update with `resourceNames: [bp-project]` (create cannot take resourceNames: grant `create` on configmaps separately and document it).
- One RoleBinding `boardingparty-cloud` per picked namespace, binding that ClusterRole to the ServiceAccount.

- [ ] **Step 1: Failing tests** (client-go fake clientset): `TestConnectCreatesOnlyNamespaceBindings` (no ClusterRoleBinding exists afterwards), `TestConnectStoresSealedTokenNotKubeconfig` (the row's bytes do not contain the kubeconfig's token), `TestDisconnectDeletesIdentity`, `TestDisconnectUnreachableStillForgets` (row and token gone, error reported as a warning).
- [ ] **Step 2–4.** Wait for the token Secret's `token` key up to 30 s.
- [ ] **Step 5: Commit** `feat: connect a cluster from a pasted kubeconfig`.

### Task 6: Linking a project

**Files:** Create `internal/project/{project.go,slug.go,project_test.go}`.

**Interfaces:**
- Produces: `project.Link(ctx, account, by, clusterID, namespace, name string) (Project, error)`; `project.Unlink(ctx, p, by) error` (removes `bp-project` and the row; leaves the namespace); `slug.Make(name string, taken func(string) bool) string`.
- On link: `install.VerifyDirector(cfg, ns)` must pass; write ConfigMap `bp-project` with keys `frameAncestors` = `Config.Origin`, `cloudProject`, `cloudAccount`.

- [ ] **Step 1: Failing tests:** `TestSlugAvoidsReserved` (`www`, `api`, `cloud`, `app`, `mail` never returned), `TestSlugDedupes` (`shop`, `shop-2`), `TestLinkWritesProjectConfigMap`, `TestLinkRefusesNamespaceWithoutDirector`, `TestUnlinkLeavesNamespace`.
- [ ] **Step 2–4.** **Step 5: Commit** `feat: link a namespace as a project`.

### Task 7: The project proxy

**Files:** Create `internal/proxy/{proxy.go,forwards.go,enter.go,proxy_test.go}`.

**Interfaces:**
- Consumes: `cluster.Config`, `install.Forward(ctx, cfg, ns, "app=bp-director", 8081)`, `install.ReadAdminToken`.
- Produces: `proxy.Handler{...}` mounted for hosts `*.` + `ProjectDomain`; `proxy.EnterURL(p Project, code string) string`.

Behaviour:
- Cloud origin `GET /p/{slug}/open` (signed in, member) stores an enter code and redirects to `https://<slug>.<domain>/_bp/enter?code=…`. The proxy redeems it once and sets host-only cookie `bp_proj` (Secure, HttpOnly, SameSite=Lax, 12 h) bound to user and project, then redirects to `/`.
- Every request: valid `bp_proj`, membership re-checked with a 30 s cache, else 302 to the cloud's open URL.
- Upstream request: drop `Cookie`, `Authorization`, `X-BP-Actor`; set `Authorization: Bearer <admin token>` and `X-BP-Actor: <email>`. Upstream response: drop `Set-Cookie`. `FlushInterval: -1` so live views stream.
- Forwards: one per project, created under a per-project lock, closed after 10 minutes without requests or on error. Admin token read when the forward opens.
- Unreachable cluster: 502 page naming the cluster and `last_reached`; `last_reached` updates on each successful forward.
- Every forward open writes `access_log(user, project, cluster, "forward.open")`.
- Ruling (spec leaves multi-replica to the builder): run one replica in the skeleton. Cost if wrong: a later move to sticky routing per project.

- [ ] **Step 1: Failing tests** (fake director via `httptest`, forwarder behind an interface): `TestRemovedMemberLosesAccessWithin30s`, `TestHeadersDoNotCross` (incoming `X-BP-Actor: evil` and cookie never reach upstream; upstream `Set-Cookie` never reaches the browser), `TestEnterCodeSingleUse`, `TestOneForwardForParallelRequests`, `TestIdleForwardCloses` (fake clock), `TestUnreachableNamesCluster`, `TestLiveStreamFlushes`.
- [ ] **Step 2–4.** **Step 5: Commit** `feat: proxy each project's board on its own subdomain`.

### Task 8: Cloud pages

**Files:** Create `internal/web/{web.go,templates/*.html,static/styles.css,web_test.go}`.

Pages: sign in; check your email; projects (list, "Open", owners also "Link a project"); project (header with name, cluster, version seen, and the board in an iframe filling the rest); team (members, roles, invite, remove); clusters (owners: connect by pasting a kubeconfig, pick namespaces, disconnect, add namespaces). Private cluster help: a short page with the open-source command `boardingparty board -n <namespace>`.

- CSP on cloud pages: `default-src 'self'; frame-src https://*.<ProjectDomain>; frame-ancestors 'none'`. Forms carry a CSRF token tied to the session.
- [ ] **Step 1: Failing tests:** `TestMemberSeesNoClusterPage` (403), `TestFormsNeedCSRF`, `TestProjectPageFramesSubdomain`.
- [ ] **Step 2–4.** Use the frontend-design skill; keep the board's look so the frame and its host read as one product.
- [ ] **Step 5: Commit** `feat: cloud pages for projects, team and clusters`.

### Task 9: Deploy

**Files:** Dockerfile and CI already exist from Task 1; add Helm chart or plain manifests in the flux-config repository (`tiny-systems/flux-config`, pushed to main).

- [ ] **Step 1:** Domain bought; DNS: apex/`cloud.` and wildcard `*.p.` to the ingress; cert-manager wildcard certificate (DNS-01 on Route 53).
- [ ] **Step 2:** Secrets via External Secrets: database URL, `CLOUD_SEAL_KEY`, SMTP password.
- [ ] **Step 3:** Postgres: the existing in-cluster Postgres (decided by the user). Create a database `cloud` and a role `cloud` that owns only it; the URL goes into the External Secret.
- [ ] **Step 4:** Verify: sign in, link `boardingparty-test`, open the board, start nothing, check `GET /api/audit` shows your email for a project save.
- [ ] **Step 5: Commit** in flux-config `cloud: deploy`.

## Rulings made during the build

- Boards live on a different registrable domain from the cloud (config refuses the same one). A board is same-site with anything on the cloud's domain and could set cookies for it otherwise. Two domains to buy, such as `boardingparty.io` for the cloud and `bpboards.io` for `*.p.` project hosts. Cost if wrong: one extra domain.
- The project-host cookie is `__Host-bp_proj; SameSite=None; Partitioned` (CHIPS): the board runs in a cross-site iframe, where Safari and Firefox block ordinary third-party cookies. The cloud puts the enter link straight into the iframe, because Safari drops partitioned cookies after cross-site redirect chains. Safari supports CHIPS from 26.2. Cost if wrong: boards ask people to reload in browsers without CHIPS.
- Sign-in links open a "Sign in as …?" page and sign in only on its button, which stops a link sent by someone else from signing a browser in, and stops mail scanners spending links.
- Nobody joins an account without following its invite link; an invite works only while its inviter is still an owner.
- API servers must be public: internal names and addresses are refused, and every config is pinned to the checked IP with TLS on the original name, so port-forwards are pinned too.
- The proxy adds its own CSP next to the director's (frame-ancestors = cloud; sandbox on artifacts and worker files) and refuses unclean paths.
