# Cloud step 3: GKE connect, install, upgrade — plan

**Spec:** docs/superpowers/specs/2026-11-17-cloud-gke-install-design.md
**Repo:** /Users/mtrofimenko/Work/boardingparty-cloud (module github.com/boarding-party/cloud)
**Execution:** inline on main (user's standing preference). Tests:
`CLOUD_TEST_DATABASE_URL=postgres://postgres@127.0.0.1:55432/postgres?sslmode=disable go test ./...`
(throwaway pg14 in the scratchpad).

## Global constraints
- Commits as Tiny Systems <dev@tinysystems.io>, no trailers. No `replace`.
- The cloud's ServiceAccount rules (internal/cluster/rbac.go) do not change.
- Owner access (Google token, kubeconfig) is never stored unsealed and never past 10 min.
- GB English in UI copy. Existing UI system (templates + static CSS), no JS needed.

## Task 1: config + internal/google client
- config: `Google{ClientID, ClientSecret}` from GOOGLE_CLIENT_ID / GOOGLE_CLIENT_SECRET; both or neither (error if only one). `Config.Google.Enabled()`.
- `internal/google`: `Client{ID, Secret, Redirect string; AuthBase, TokenURL, CRMBase, ContainerBase string; HTTP *http.Client}` (bases default to Google's).
  - `AuthURL(state, challenge string) string` — code flow, scope cloud-platform, access_type=online, prompt=select_account, code_challenge_method=S256.
  - `NewVerifier() (verifier, challenge string)`.
  - `Exchange(ctx, code, verifier) (Token{Access string; Expiry time.Time}, error)`.
  - `Projects(ctx, tok) ([]Project{ID, Name}, error)` — v1 projects, active only, all pages.
  - `Clusters(ctx, tok, project) ([]GKE{Name, Location, Resource, Endpoint string; CA []byte; PrivateOnly bool; Status string}, error)`.
  - `Cluster(ctx, tok, resource) (GKE, error)`.
- Tests: httptest server standing in for all four bases.

## Task 2: grants (internal/google/grants.go)
- schema: `google_grants (id text pk, session_hash bytea, token_sealed bytea, seal_key_id text, next text, expires timestamptz)`.
- `Grants{DB, Seal, Now}`: `Put(ctx, session string, tok Token, next string) (id, error)` (expires = min(now+10m, tok.Expiry)); `Get(ctx, session, id) (Token, next string, error)` (ErrExpired when gone/expired); `Drop(ctx, id)`; `DropSession(ctx, session)`.
- Sealed with `Seal.SealFor("grant:"+id, …)`.

## Task 3: clusters connect as GKE; owner access
- schema: clusters add `provider text not null default 'kubeconfig'`, `gke_resource text not null default ''`.
- `Cluster` gains `Provider, GKEResource`.
- Refactor Connect around `connectCfg(ctx, account, by, name, cfg, server, ca, provider, resource, namespaces, allowNone)`.
- `ConnectGKE(ctx, account, by, name, tok, g google.GKE, namespaces)`; `NamespacesGKE(ctx, account, by, tok, g)`.
- `OwnerAccess{Kubeconfig []byte; Google string /*access token*/}`; `(s) OwnerConfig(ctx, c Cluster, by, a OwnerAccess) (*rest.Config, error)` — kubeconfig must point at c.APIServer; Google uses c.APIServer + c.CA + bearer; both pinned with `public`. `BindProject(ctx, cfg, ns)` binds the cloud's project role in ns (as the owner).
- Tests: fake kube; GKE connect stores provider/resource; OwnerConfig refuses a kubeconfig for another server.

## Task 4: installs (internal/deploy)
- schema: `installs (id pk, account, cluster, project text default '', namespace, name, staging_url, kind 'install'|'upgrade', state 'running'|'done'|'failed', log text, by_user, started, finished)`.
- `Version() string` — boardingparty module version from build info, "v" stripped; overridable var for tests.
- `Service{DB, Projects *project.Service, Clusters *cluster.Service, Steps Steps, Now}`; `Steps{Tiny func(ctx, cfg, ns) error; Director func(ctx, cfg, ns, tag string, out io.Writer) error}` defaulting to tinyruntime.Init + install.EnsureDirector(NoDowngrade).
- `StartNew(ctx, account, by, clusterID, ns, name, stagingURL, cfg) (id, error)`, `StartUpgrade(ctx, account, by, p project.Project, cfg) (id, error)` — validate (owner, ns name DNS label, name 1-80, URL http(s), upgrade only when installed < Version and installed is a release), insert row, run steps in goroutine with ctx detached (15 min timeout), append log lines, then for new: `Clusters.BindProject`, `Projects.Register(...)`; for upgrade: update version_seen. Log access_log action install/upgrade.
- `Get(ctx, account, id) (Install, error)`; `FailStale(ctx)` on start-up.
- `UpgradeOffer(installed string) (newer string, ok bool, custom bool)`.
- project: split `Link` into checks + `Register(ctx, k, account, clusterID, ns, name, stagingURL, version)`.
- Tests: fake steps; new install ends with project row + done; failing step -> failed with error in log; stale rows failed; upgrade refused for sha builds and for same/newer.

## Task 5: web
- Routes (owner): GET /google/start?next=…, GET /google/callback, GET /clusters/gke, POST /clusters/gke/connect, GET /projects/new?cluster=, POST /projects/new, POST /p/{slug}/upgrade, GET /installs/{id}.
- `next` allow-list: /clusters/gke, /projects/new?cluster=<id>, /p/<slug>?upgrade=1.
- State + PKCE verifier in `__Host-bp_gstate` cookie (HttpOnly, Lax, 10 min), cleared on callback.
- Install page refreshes itself every 2 s (meta refresh) while running.
- Clusters page: "Connect with Google" button when enabled. Projects page: "New project" per cluster. Project page: version + Upgrade/custom build.
- Sign-out drops the session's grants.
- Tests: callback with bad state refused; next outside allow-list refused; new-project form posts start an install.

## Task 6: ship
- flux: GOOGLE_CLIENT_ID env, GOOGLE_CLIENT_SECRET from ESO key `bpcloud-google-client-secret`.
- GCP: OAuth consent screen + web client in tinyplatform (redirect https://boardingparty.io/google/callback); enable cloudresourcemanager + container APIs.
- Live: connect cluster-1 with Google, New project in `bp-cloudtest`, open its board, upgrade check shows state.
