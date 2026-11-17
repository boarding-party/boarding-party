# boardingparty cloud, step 3: connect GKE, install and upgrade

Approved by the user 2026-11-17 ("go"). Extends
`2026-11-10-cloud-design.md` (Part 2, order of work step 3). EKS and AKS
come later; the shared cluster (step 4) and first-run personas (step 5)
are separate.

## Intent

Today an owner can only link a namespace that already runs a director, and
only by pasting a kubeconfig with an inline token (GKE users must swap the
gcloud plugin for a short-lived token by hand). Step 3 lets an owner:

1. connect a GKE cluster by signing in with Google and picking it from a list;
2. create a project from the cloud: tiny's namespace setup plus the
   director, in a new or existing namespace;
3. upgrade a project's director from the cloud.

## Decided

- **The cloud's own account stays narrow.** Its ServiceAccount keeps
  today's rules (read the admin Secret, port-forward, read the director,
  keep `bp-project`). It is never given rights to create namespaces,
  apply manifests or touch CRDs. (Changed from the Part 2 text, which had
  installs run as the cloud's account; my call, told to the user: a
  wider standing token is a bigger prize, and every connected cluster
  would need reconnecting to get it.)
- **Installs and upgrades act as the owner**, with access the owner hands
  over for that one action:
  - on a cluster connected through Google: a Google access token from a
    fresh sign-in (a click if the owner has consented before);
  - on any other cluster: a pasted kubeconfig, as for connect today.
  The owner's access is used for the install and the binding, then
  dropped. It is never written to the database unsealed and never kept
  past ten minutes.
- **Google sign-in** (OAuth 2.0 web client, authorisation code + PKCE,
  `access_type=online`, so no refresh token exists; scope
  `https://www.googleapis.com/auth/cloud-platform`, which GKE needs to
  accept the token as a Kubernetes bearer). `state` is bound to the
  cloud session. Settings `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`;
  when unset the Google button is hidden and everything else works.
  Until Google verifies the app, owners see the "unverified app" screen
  and sign-ins are capped at 100 users. Accepted for now.
- **The grant** (the access token between the callback and the owner's
  next click) is sealed with the existing sealer and stored in a
  `google_grants` row tied to the session, expiring after ten minutes
  (or the token's own expiry, if sooner), deleted when used or on sign-out.
- **Picking a cluster:** projects from Resource Manager (active only),
  then clusters from the Container API across all locations. The cloud
  connects to the cluster's public endpoint with its CA from
  `masterAuth`. A cluster with only a private endpoint is refused with a
  plain message (the cloud can't reach it).
- **Connect with Google** reuses today's connect (create the cloud's
  account, wait for its token, seal and store it). The cluster row
  records `provider = 'gke'` and the GKE resource name
  (`projects/P/locations/L/clusters/C`) so later actions know to ask
  Google and can check the token is for the same cluster. Namespaces
  that already run a director can be picked at connect time, as today;
  picking none is allowed for a Google connect (projects are made later).
- **New project** (owners): name, namespace (new or existing), staging
  URL. It runs, as the owner:
  1. `tinyruntime.Init(ns, EgressProxy: true, FenceWait on k3s)`, the
     same as `boardingparty up`;
  2. the director install from the pinned `pkg/install`
     (`EnsureDirector` with server-side apply, verify), tag = the
     pinned module's release version;
  3. the cloud's project binding in the namespace;
  4. `bp-project` (frame ancestors, cloud ids) and the project row,
     as `Link` does today, plus the staging URL.
  tiny's step also applies its two CRDs, which are cluster-wide. The
  confirm says so in one line.
- **Version.** The director the cloud installs is the boardingparty
  module version the cloud is built with (read from build info), so
  manifest and image always match. Bumping that pin is how a new release
  reaches the cloud.
- **Upgrade** (owners) is offered when the installed tag is a release
  older than that version. It runs the same steps 1-2 with the owner's
  access and `NoDowngrade`. It never downgrades. A director on a dev build
  (`sha-…`, `main`) shows "custom build" and no button.
- **Long work runs in the background.** Install and upgrade take minutes
  (rollout, image pull) and the ingress cuts requests after 60 s. Each is
  an `installs` row (project, kind install/upgrade, state
  running/done/failed, log text, by, started, finished). A goroutine
  writes the log lines; the page polls the row. On start-up, rows left
  `running` by a previous process are marked failed ("the cloud restarted
  during the install; run it again"). Runs on one replica today; the row
  is the source of truth so a second replica only loses live log lines.
- **Audit.** Every install and upgrade is logged with user, project and
  cluster, like other uses of a cluster.

## Not doing (now)

- EKS, AKS sign-in.
- Upgrading tiny's CRDs on their own from the cluster page (they upgrade
  with each install or upgrade).
- Picking the director version; downgrades.
- Removing a director or namespace from the cloud (unlink stays as it is).

## Left to the builder

- Page layout and words, within the existing cloud UI.
- Polling interval and how the log is shown.
- Exact table columns beyond what is named here.
