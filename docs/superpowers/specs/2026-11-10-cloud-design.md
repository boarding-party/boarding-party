# boardingparty cloud: design

Status: draft for review, 10 November 2026.

## Intent

The open-source product works: one command (`boardingparty board -n <ns>`)
installs a director into a namespace and serves a local board. The cloud
makes the same product usable without a terminal: sign up, connect a
cluster (or rent one from us), and open projects in a browser with a team.

The cloud is a thin layer. Everything a project knows (runs, bugs, mail,
personas, videos) stays in the customer's namespace. The cloud knows who
the people are, which projects exist and where each one lives.

## Goals

- Accounts with teams: invites, two roles, owner and member.
- Projects: each project points at one cluster and one namespace.
- Three ways to get a cluster: the customer's public cluster, our shared
  cluster (paid), or no cloud at all for private clusters (open source).
- Opening a project shows the project's own board, served from its
  namespace, inside the cloud's page.
- The cloud can install and upgrade the in-cluster parts when connected.
- First-run help: from the staging URL, suggest the first personas.

## Non-goals (for now)

- Reselling model usage. Customers bring their own Anthropic key (BYOK).
- Reaching private clusters (no agent, no tunnel, no VPN). Those
  customers use the open-source CLI.
- Cloud-side copies of run data, cross-project dashboards or search.
- Single sign-on, audit export, fine-grained permissions.

## Anti-goals

- The cloud becoming a second source of truth. If the cloud database is
  lost, every project and all its history must still be intact in its
  namespace; only the account and project list is lost.
- A board whose screens don't match the director behind it (this morning's
  downgrade bug, where an old CLI's board UI dropped fields the newer
  director kept).
- A permanent connection from the cloud to customer clusters.
- The cloud ever holding a customer's Anthropic key or site passwords.

## Decided by the user

- The cloud lives separately from the clusters: accounts, their projects,
  and each project's cluster and namespace.
- Private clusters: we don't connect; we give instructions for the
  open-source route.
- Public clusters: the customer chooses their cloud provider and we
  connect to the cluster, then create the project and namespace, install
  everything, and link it together.
- No cluster: we sell one. It is a shared cluster on cheap servers
  (Hetzner or similar), one namespace per customer, network policies in
  place.
- BYOK for model use; reselling maybe later.
- Teams with invites, owner and member roles. Owners connect and
  disconnect clusters.
- The cloud is not connected to customer clusters all the time. The board
  web app is proxied from the customer's cluster and embedded in the cloud,
  so the board version is the customer's own.
- When connected, the cloud can help upgrade the in-cluster components.
- Database and assets stay in the cluster, found by known names.

## Architecture

### Part 1: in-cluster redesign (open source, done first)

Today the board UI is embedded in the CLI binary and the CLI proxies its
API calls to the director over a port-forward. That puts the UI version in
the hands of whoever runs the CLI. The redesign moves the UI into the
director:

1. **The director serves the board.** The `internal/board/ui` files are
   embedded in the director image and served on the admin listener
   (`127.0.0.1:8081`) at `/`, next to `/api/...`. The admin listener stays
   on loopback; it is only reachable through a Kubernetes port-forward.
2. **The CLI becomes a thin, version-agnostic proxy.** `boardingparty board`
   keeps its local server, launch link, cookie, Host and Origin checks, and
   forwards every path (UI and API) to the director, adding the admin
   token. It no longer ships UI files, so an old CLI shows the new UI. The
   downgrade guard stays for `setup`, which still installs a version.
3. **Known names.** Everything is found by fixed names in the namespace,
   which the cloud relies on:
   - Deployment `bp-director`, label `app=bp-director`, container `director`;
   - Secret `bp-director-admin`, key `token` (admin API bearer);
   - PersistentVolumeClaim for `/data` (database `boardingparty.db`,
     `artifacts/`, `files/`);
   - Deployment annotations `boardingparty.io/manifest-hash` and
     `boardingparty.io/cli-built`, plus the image tag, give the installed
     version.
   - A ConfigMap `bp-project` (new) holds the cloud link: cloud project id
     and account id, so a namespace can say which cloud project it belongs
     to. Absent for open-source installs.
4. **Base path.** The UI uses relative URLs (`api/runs`, not `/api/runs`)
   so it works at any path. My call: the cloud serves each project on its own
   subdomain anyway (see Part 2), but relative URLs cost little and keep
   both doors open.
5. **Framing.** The director's CSP gains a configurable `frame-ancestors`
   list, read from the `bp-project` ConfigMap (key `frameAncestors`,
   default `'none'`). Because it lives in the ConfigMap and not in the
   director manifest, a later CLI `setup` on the same namespace re-applies
   the manifest without breaking the cloud's frame. `setup` never writes
   or deletes `bp-project` (my call).
6. **Agent key screen.** The board gets a setup screen for the Anthropic
   key (BYOK). The director writes it into tiny's Secret `tiny-agent-env`
   (`ANTHROPIC_API_KEY`); its Role gains get and update on that one Secret
   by name. `setup`'s preflight turns a missing key from a failure into a
   warning, and runs refuse to start until the key is set, with the board
   pointing to the screen. This keeps the key out of the cloud and works
   the same for open-source installs (my call).
7. **Public packages.** The install, port-forward and known-names code
   moves from `internal/cli` to an importable package (`pkg/install`), and
   tiny's namespace and CRD install (`internal/runtimeinstall` in the tiny
   repo) gets a public wrapper, so the cloud can import both. Go can't
   import another module's `internal` packages (my call).

### Part 2: the cloud service

**Stack (my call):** Go, Postgres, one service, its own private repository
(`boarding-party/cloud`), deployed on our GKE with Flux like our other
services. It imports the open-source module for the install and connect
code (`setup`, port-forward, known names) rather than copying it. No
`replace` directives: the cloud pins a released version of the
open-source module.

**Data (Postgres):**
- `accounts` (id, name, created)
- `users` (id, email, name), `memberships` (account, user, role owner or
  member), `invites` (account, email, role, token hash, expires)
- `clusters` (id, account, kind `customer` or `shared`, provider, API
  server URL, CA, credential reference, connected by, created)
- `projects` (id, account, cluster, namespace, name, staging URL,
  installed version seen last, created)
- `cluster_credentials` stored encrypted (envelope encryption with a key
  in GCP Secret Manager or KMS); the row holds ciphertext only.

Nothing about runs, bugs, personas or mail is stored.

**Cluster credentials.** On connect, the cloud gets temporary access
through the provider (GKE, EKS, AKS sign-in; pasting a kubeconfig is the
fallback) and uses it once to create its own identity in the cluster: a
ServiceAccount `boardingparty-cloud` in a namespace `boardingparty-system`,
with a ClusterRole that can create namespaces and a Role per project
namespace for installing and port-forwarding. It stores only a token for
that ServiceAccount. Disconnecting deletes the cloud's identity from the
cluster (when reachable) and the stored token.

The token is long-lived, because members must be able to open a board
without an owner signing in to the cloud provider each time. "Not
connected all the time" means no open connection, not no stored access.
The token is scoped (namespaces it created, plus port-forward and
install rights there), rotatable from the cluster page, and every use
is logged with the user and project (my call). Same model as the earlier
Tiny Systems platform: the cloud has its own scoped credentials, never the
user's.

**Opening a project (the request path):**

```
browser ── cloud.boardingparty.io  (cloud UI: account, projects, team)
   │         └─ iframe src= https://<project>.p.boardingparty.io/
   └──── <project>.p.boardingparty.io  (project proxy, same cloud service)
            │  checks the cloud session and the user's membership
            │  opens (or reuses) a port-forward to bp-director:8081
            │  adds Authorization: Bearer <token from bp-director-admin>
            ▼
        director in the customer's namespace serves UI + API
```

- One subdomain per project: each board is its own browser origin, so a
  bug in one project's board can't reach another project's data. The cloud
  sets a project-scoped cookie on that subdomain after a redirect hop from
  the cloud origin (single-use code, like the CLI's launch link).
- The port-forward opens on the first request and closes after 10 minutes
  without requests (my call). Live views (`/api/runs/{id}/live`) stream
  through it.
- The admin token is read from the Secret when the forward opens and kept
  in memory only for the forward's life.
- If the cluster can't be reached, the project page says so with the
  cluster's name and the last time it was reached. Other projects keep working.

**Install and upgrade.** The cloud runs the same install as `setup`
(server-side apply of the director manifest, admin Secret, network fence)
with its ServiceAccount, and tiny's namespace setup. It shows the
installed version (from the known names) next to the newest release and
offers "Upgrade" to owners. It never downgrades. Upgrades only happen when
an owner presses the button.

The owner's Upgrade button covers only what lives in the project's
namespace (director, worker image, fence settings). tiny's CRDs are
cluster-wide: on a customer cluster they are upgraded by a separate
owner action on the cluster page, which warns that it affects every tiny
user on that cluster; on our shared cluster we upgrade them ourselves as
one rollout, never from a customer's button (my call).

**Shared cluster (ours).** k3s on rented servers (Hetzner or similar),
started with the k3s installer from the ideas document. Each customer
project gets a namespace with:
- default-deny network policies: no traffic between customer namespaces,
  egress only through tiny's fence;
- a ResourceQuota and LimitRange (CPU, memory, storage, one active run);
- the customer's Anthropic key as a Secret in their namespace, entered in
  the board, never through the cloud;
- the cloud's ServiceAccount, which customers never get. They have no
  cluster access, only the board;
- run pods under gVisor (a RuntimeClass `gvisor`): each run is an agent
  acting on a customer's instructions, and namespaces alone don't stop a
  container escape onto a node other customers share. Decided by the user.
  This limits server types to ones where gVisor runs, and the director
  gains a setting for the run pods' RuntimeClass (default none, so customer
  clusters are unchanged).

**Data on the shared cluster.** Servers use local disks, so a lost node
loses a project's database and videos. Backups are a paid add-on, next to
the cluster itself (decided by the user): nightly snapshots of the
project's volume to object storage, kept 7 days (my call on the numbers).
Without the add-on the project page says plainly that its data is not
backed up. When a subscription ends, the project turns read-only for 14
days, then its namespace and backups are deleted (my call).

**Billing.** Customer clusters: free to connect (the team layer may be
paid later). Shared cluster: priced per project per month, by size, with
backups as a paid add-on. Stripe; details are not needed for the
architecture.

**First-run personas.** After install, the cloud asks for the staging URL
and, if the site needs a login, test credentials (stored by the director in
the namespace, as today). It then starts a lead run in the project
("first wake") with a brief built from the URL. The lead reads the site
and creates the first personas, as Megan did. This needs nothing new: the cloud
only writes the lead's brief and presses Run now.

## Flows

1. Sign up, create account (you are owner).
2. Connect a cluster: private, public, or ours.
   - Private: show the open-source instructions and stop.
   - Public: provider sign-in, create the cloud's identity, check tiny's
     requirements (the setup preflight), record the cluster.
   - Ours: choose size, pay, the cloud picks a shared cluster.
3. Create project: name, staging URL. The cloud creates the namespace,
   installs tiny's parts and the director with the cloud's
   `frame-ancestors`, writes the `bp-project` ConfigMap.
4. Open project: the board in the iframe asks for the Anthropic key and
   mailbox in its own setup screens, then the cloud suggests the first lead
   run.
5. Invite teammates. Members open projects; owners also connect clusters,
   create and delete projects, and upgrade.

## Security notes

- The cloud holds, per cluster, one encrypted ServiceAccount token, and
  per open project, an admin token in memory. Both are the most sensitive
  things it has; access is logged per user and project.
- Members and owners alike get the full board of a project they can open.
  The board itself has no users yet; the cloud passes the user's email in
  a header (`X-BP-Actor`) so the director can record who did what (lead
  decisions, project edits). The director trusts it only from the admin
  listener.
- Deleting a project in the cloud asks whether to delete the namespace
  too. By default it unlinks and leaves the data in the cluster.

## Order of work

1. Part 1 (open source): director serves the UI, CLI proxies all paths,
   relative URLs, `--frame-ancestors`, `bp-project` ConfigMap, `X-BP-Actor`.
   This is useful on its own: it removes the UI version skew for every CLI
   user.
2. Cloud skeleton: accounts, teams, invites, projects pointing at an
   existing namespace via a pasted kubeconfig; project proxy on
   subdomains; open and use a board.
3. Provider connect (GKE first, then EKS and AKS), install and upgrade from
   the cloud.
4. Shared cluster: k3s servers with gVisor, namespace setup, quotas,
   billing, backups add-on.
5. First-run personas from the staging URL.

## Left to the builder

- Session and login method for the cloud (email magic link is enough to
  start).
- Exact RBAC rules for the cloud's ServiceAccount, derived from what
  `setup` and tiny's namespace setup apply.
- How the project proxy keeps port-forwards across several cloud replicas
  (sticky routing per project, or one forward per replica).
- Domain names.
