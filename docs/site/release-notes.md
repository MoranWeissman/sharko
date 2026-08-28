# Release Notes

<!--
Format for v2.x release-notes entries:
## vX.Y.Z — <theme>
### Breaking changes
### What's new
### Removed
### Security
### Bug fixes

Each bullet: one-liner summary + (PR #N) link. Detailed body lives in
the PR. Append new releases at the TOP of the v2.x stream so the most
recent release is the first thing readers see.
-->

## v4.0.1 — a chart version pinned without the leading "v" still resolves

**Status:** the first publishable version of the v4 technical-preview line. It
carries everything in the v4.0.0 entry below, plus the fix described here.

**Sharko v4.0.1 is a technical preview. Install only published `v4.0.1`-or-later
artifacts. `v3.0.0` and earlier remain retired and unsupported. Do not use Sharko
in production.** See the [v3.0.0 entry](#v300-first-public-release) below.

### Bug fixes

- **An addon pinned to a chart version without the leading `v` now finds its
  chart.** Helm repositories disagree about that leading `v`: the jetstack index
  publishes cert-manager as `v1.16.3` and carries no bare `1.16.x` entry at all.
  Sharko compared the pinned version against the index entry as an exact string,
  so an addon pinned to `1.16.3` came back as `version 1.16.3 not found for chart
  cert-manager` and every request that has to read the chart's own values failed
  with a 500 — the values editor, the merge preview, the AI annotation of values,
  and adding an addon from a pasted chart URL. Sharko now tries the pinned
  version exactly first and, only if that finds nothing, tries the same version
  with one leading lowercase `v` added or removed. Nothing else changed: a pinned
  pre-release or build suffix is never dropped, so `1.2.3-rc.1` still never
  becomes `1.2.3`; `V1.2.3` still does not match `v1.2.3`; and Sharko still never
  picks a nearby version on its own.

---

## v4.0.0 — tagged, never published

**Status:** the `v4.0.0` tag was created on 2026-08-27 and pushed, and it stays
exactly where it is. Its release run then failed at the release-evidence gate, so
none of the publishing steps ran: there is no `v4.0.0` release page and no
`v4.0.0` artifact of any kind, and none will be produced later — the tag is left
in place rather than moved onto a different commit. This entry lists everything
merged to `main` since v3.0.0 (released 2026-07-21), all of which is in the v4
code line. Install only published `v4.0.1`-or-later artifacts.

**Sharko v4.0.1 is a technical preview. Install only published `v4.0.1`-or-later
artifacts. `v3.0.0` and earlier remain retired and unsupported. Do not use Sharko
in production.** See the [v3.0.0 entry](#v300-first-public-release) below.

### Breaking changes

- **A brand-new Sharko no longer starts wide open.** If nobody has created a user
  yet, Sharko now generates a real admin account and password the first time it
  starts, instead of letting anyone in with any login. Anonymous login has been
  removed completely. Get the generated password from the Kubernetes Secret
  `sharko-initial-admin-secret` (or from `~/.sharko/initial-admin.json` if you're
  running Sharko locally, not in a cluster).
  ([#777](https://github.com/MoranWeissman/sharko/pull/777))
- **The important one: Sharko now verifies ArgoCD's own TLS certificate by
  default.** It never did before — the setting existed as a Helm value, an
  environment variable, and a connection field, but every call site ignored
  it, so ArgoCD's bearer token was exposed to anyone who could intercept the
  connection. If your ArgoCD uses a self-signed or internal-CA certificate
  over https, Sharko will now refuse to connect until you either add that CA
  to the Sharko pod's trust store, or explicitly tick "Skip TLS certificate
  verification" in Settings, or set `connection.argocd.insecure=true`.
  Connections saved through the old setup wizard or Settings page already
  had "skip verification" written into them, so most existing installs keep
  working as before — but you should go and untick that box on purpose now
  that it's an honest choice instead of the only option.
  ([#800](https://github.com/MoranWeissman/sharko/pull/800))
- **Taking over a cluster with no ownership markers now stops and makes you
  confirm.** Before, Sharko would let this through quietly. Now you have to read
  a warning about who might own the connection's Secret and confirm you want to
  proceed, every time.
  ([#788](https://github.com/MoranWeissman/sharko/pull/788),
  [#789](https://github.com/MoranWeissman/sharko/pull/789),
  [#790](https://github.com/MoranWeissman/sharko/pull/790))
- **If another tool's ownership marker is still on the cluster's connection
  Secret, takeover now refuses outright — there's no tick-a-box way past it
  anymore.** You have to turn the other tool off and remove its label by hand,
  then run the takeover check again before Sharko will let you continue.
  ([#788](https://github.com/MoranWeissman/sharko/pull/788),
  [#789](https://github.com/MoranWeissman/sharko/pull/789),
  [#790](https://github.com/MoranWeissman/sharko/pull/790))
- **AWS Secrets Manager and Kubernetes addon-secret paths now have a real
  boundary.** An AWS Secrets Manager connection must have a prefix configured,
  and every addon-secret path must sit under it — an empty prefix used to mean
  "the whole AWS account" and is now a configuration error instead. A
  Kubernetes addon-secret path can no longer name a namespace other than the
  one its connection is configured for. If you were relying on either of the
  old, looser behaviors, this breaks on upgrade.
  ([#791](https://github.com/MoranWeissman/sharko/pull/791))
- **A cluster whose connection skips certificate checks no longer receives
  addon secrets.** If a cluster's kubeconfig or ArgoCD connection has TLS
  verification turned off (`insecure-skip-tls-verify` / `insecure: true`),
  Sharko now refuses to deliver any secret to it — reads, deletes, and
  connectivity checks still work, only secret delivery stops. Fix the
  cluster's connection with real CA data to get delivery working again; a
  test estate that intentionally uses self-signed clusters can build the
  binary with the `sharko_unverified_dest_ok` tag instead.
  ([#792](https://github.com/MoranWeissman/sharko/pull/792))
- **Same rule now also covers a cluster registered with a plain `http://`
  address, not just one that skips certificate checks.** Plain http is
  worse than skip-verify — anyone on the network path can read the value
  with no attack needed at all — so it gets the same refusal.
  ([#799](https://github.com/MoranWeissman/sharko/pull/799))
- **Sharko no longer accepts a secret definition over the API.**
  `POST /api/v1/addon-secrets` and `DELETE /api/v1/addon-secrets/{addon}` are
  gone — every addon-secret refresh now reads its definition from the Git
  catalog only, the same source the scheduled sync already used. If anything
  in your setup called those two endpoints directly, define the secret in the
  catalog in Git instead.
  ([#794](https://github.com/MoranWeissman/sharko/pull/794))
- **The Helm chart no longer grants itself a cluster-wide read of every
  Secret on the host cluster.** Permissions are now scoped per namespace —
  ArgoCD's own namespace, plus the namespace of each configured Kubernetes
  secrets-backend connection — instead of one blanket cluster-wide rule. If
  you use the Kubernetes secrets backend in a namespace other than Sharko's
  own release namespace, list that namespace in the new chart setting
  `rbac.k8sSecretsProviderNamespaces`, or Sharko won't be able to read
  secrets there after upgrading.
  ([#793](https://github.com/MoranWeissman/sharko/pull/793))

### What's new

#### A new repo format for clusters and addons (v4)

Sharko can now run entirely off a flat, self-describing set of files in your
Git repo instead of the older nested layout: a root `managed-clusters.yaml`,
a root `catalog.yaml` (the approved-addons list), a root engine file, and one
`cluster-addons/<cluster-name>.yaml` per cluster. A small Helm chart (the
"engine") turns those files straight into ArgoCD ApplicationSets — no
generated/baked catalog step in between.
([#619](https://github.com/MoranWeissman/sharko/pull/619),
[#648](https://github.com/MoranWeissman/sharko/pull/648),
[#649](https://github.com/MoranWeissman/sharko/pull/649),
[#650](https://github.com/MoranWeissman/sharko/pull/650),
[#666](https://github.com/MoranWeissman/sharko/pull/666))

- Every everyday operation now works end to end on the new format: editing or
  deleting a catalog entry, adopting or unadopting a cluster, changing which
  addons are on for a cluster, and removing a cluster altogether all go
  through the same preview-then-pull-request flow.
  ([#774](https://github.com/MoranWeissman/sharko/pull/774),
  [#775](https://github.com/MoranWeissman/sharko/pull/775),
  [#779](https://github.com/MoranWeissman/sharko/pull/779))
- **A one-pull-request migration** converts an existing repo from the old
  layout to the new one in a single commit; nothing stops working while you
  decide when to run it, and the old layout keeps being read right up until
  you do.
  ([#636](https://github.com/MoranWeissman/sharko/pull/636))
- **The engine chart is signed and published as an OCI chart** on every
  Sharko release, and picks up version-pin upgrade pull requests on its own.
  ([#623](https://github.com/MoranWeissman/sharko/pull/623))
- **The CLI has full parity with the new format**, including `takeover`,
  `takeover-preflight`, `drop-legacy-labels`, `unregister-consequences`,
  `enable-addon`, `disable-addon`, and `upgrade-clusters` — and every dry-run
  in the CLI now prints the real file-by-file diff instead of a placeholder.
  ([#776](https://github.com/MoranWeissman/sharko/pull/776))
- **Every preview in the UI shows the real file content, not a placeholder** —
  adding an addon, editing the catalog, adopting or removing a cluster,
  editing values.
  ([#780](https://github.com/MoranWeissman/sharko/pull/780))

#### Taking over a cluster someone else set up

Sharko can now take over a cluster that was registered outside of it — a
safety preflight check first, then an in-place swap of the ArgoCD connection
Secret, dropping any legacy labels, and a clear list of what breaks if you
walk away instead of finishing the takeover.
([#643](https://github.com/MoranWeissman/sharko/pull/643)). This flow was
hardened further this arc — see Breaking changes above for what that means
for you.

#### Secret Sync (was "Managed Secrets")

A page that shows every secret Sharko manages on your clusters — both
connection secrets and the values secrets it writes for addons — with
refresh, sync, and diff actions, worst-first ordering, and a 30-second
self-refresh. Renamed from "Managed Secrets" partway through, with a real off
switch for the values engine and a docs page naming the boundary promise.
([#713](https://github.com/MoranWeissman/sharko/pull/713),
[#715](https://github.com/MoranWeissman/sharko/pull/715),
[#727](https://github.com/MoranWeissman/sharko/pull/727))

- **Sharko won't touch a secret it didn't create.** If it finds one with its
  label but no record of writing it, that secret is marked "Foreign" and left
  alone. ([#719](https://github.com/MoranWeissman/sharko/pull/719))
- **Leftover secrets are found, not just ignored.** If a catalog entry that
  used to push a secret gets deleted, the secret it left behind is now
  detected and shown as "Orphaned," with an operator-gated delete that names
  exactly what it's about to remove.
  ([#753](https://github.com/MoranWeissman/sharko/pull/753),
  [#754](https://github.com/MoranWeissman/sharko/pull/754))

#### Native Gitea support

Gitea is now a first-class Git provider option alongside GitHub and Azure
DevOps, with both read and write support (branches, files, pull requests,
merges).
([#607](https://github.com/MoranWeissman/sharko/pull/607),
[#608](https://github.com/MoranWeissman/sharko/pull/608))

#### Dashboard rebuilt

The dashboard was redesigned twice this arc and landed as a work queue: pull
requests and problems lead, and the old scattered stat cards collapse into
one clickable fleet strip.
([#670](https://github.com/MoranWeissman/sharko/pull/670),
[#674](https://github.com/MoranWeissman/sharko/pull/674)) A long series of
hands-on test walks then polished wording and behavior across almost every
screen — clusters, addons, observability, marketplace — too many individual
fixes to list here one by one.

#### Faster pages

The server stopped rebuilding its ArgoCD and Git clients on every single
request, and the UI now shows last-known data immediately and refreshes in
the background instead of blanking the screen while it waits.
([#673](https://github.com/MoranWeissman/sharko/pull/673)) Two slow
per-request lookup patterns were fixed and hot reads now answer from a short
cache. ([#692](https://github.com/MoranWeissman/sharko/pull/692))

#### Clearer errors

Every error the server returns now has the same shape — a plain headline,
the cause, a hint, and a machine-readable code — and the UI renders that
structure instead of gluing message fragments together.
([#694](https://github.com/MoranWeissman/sharko/pull/694)) If ArgoCD becomes
unreachable or rejects Sharko's token, you now get an honest "can't check
right now" state instead of being bounced back to first-time setup.
([#693](https://github.com/MoranWeissman/sharko/pull/693))

#### Release process, now backed by evidence

- **A tag can only publish once every real check has passed** — the full
  end-to-end suite, the docs build, lint, and the performance check — not
  just the everyday quick CI run.
  ([#781](https://github.com/MoranWeissman/sharko/pull/781))
- **arm64 images are now genuinely cross-compiled** instead of built under
  slow emulation (which had been silently failing), and every CLI release
  ships a real SBOM.
  ([#758](https://github.com/MoranWeissman/sharko/pull/758))
- **A new end-to-end suite runs against a real Gitea server in CI**, not just
  a stand-in, catching drift between the two.
  ([#757](https://github.com/MoranWeissman/sharko/pull/757))

#### Open source readiness

- Dependency and vulnerability scanning are now wired into CI; the first run
  caught and fixed several real advisories.
  ([#728](https://github.com/MoranWeissman/sharko/pull/728))
- The AI tooling used to help build Sharko (BMAD workflow skills, team role
  files) is now in the public repo with proper MIT attribution, plus a guide
  for anyone who wants to use the same setup.
  ([#745](https://github.com/MoranWeissman/sharko/pull/745))
- Version numbers, the changelog, and documentation links were swept to say
  the true, current thing.
  ([#729](https://github.com/MoranWeissman/sharko/pull/729))

#### Smaller things

- API tokens get a lifecycle: they expire (90 days by default) and can be
  renewed or revoked, and sessions now enforce their own expiry too.
  ([#632](https://github.com/MoranWeissman/sharko/pull/632))
- API tokens now survive a restart — they used to be wiped out every time the
  Sharko pod restarted; they're now saved (as a hash, never the raw value) so
  they keep working across restarts.
  ([#783](https://github.com/MoranWeissman/sharko/pull/783))
- A stuck fight over cluster labels now raises a real Kubernetes event
  (`DriftDetected`), visible with `kubectl get events`, instead of only
  showing up in an internal log line.
  ([#770](https://github.com/MoranWeissman/sharko/pull/770))

### Removed

- **Sharko explored a Kubernetes operator** (a CRD and controller for driving
  addon labels directly from a custom resource) and decided not to ship it —
  the direction settled on the engine chart instead. The code is kept on the
  `operator-shelf` branch, not deleted, but it is not part of the product.
  ([#619](https://github.com/MoranWeissman/sharko/pull/619))
- **The catalog-scanning bot is parked for good.** By default it only scans
  and reports now, even when triggered manually — it will not open a pull
  request unless you explicitly ask it to.
  ([#782](https://github.com/MoranWeissman/sharko/pull/782))

### Security

- **The addon-values engine refuses to update or delete a secret it did not
  create.** ([#719](https://github.com/MoranWeissman/sharko/pull/719))
- Dependency and vulnerability scanning caught and fixed three real,
  reachable advisories (gRPC, `x/text`, and sigstore-go).
  ([#728](https://github.com/MoranWeissman/sharko/pull/728))
- **Secret values never reach the logs.** Five lines that logged the length
  of a fetched value no longer do — including two that logged the length of
  a whole kubeconfig at the normal log level, not just the debug level. A
  new test drives a known fake value through every delivery path and
  confirms it never turns up in a log line, a metric, an audit entry, or an
  error message.
  ([#798](https://github.com/MoranWeissman/sharko/pull/798))
- **The shipped container image now runs on Go 1.25.12,** clearing eleven
  known high-severity CVEs in the Go standard library's certificate, TLS,
  HTTP/2, and DNS handling that were baked into the previous image. If
  you're running an older image, update it.
  ([#795](https://github.com/MoranWeissman/sharko/pull/795))
- See "The important one: Sharko now verifies ArgoCD's own TLS certificate
  by default," "AWS Secrets Manager and Kubernetes addon-secret paths now
  have a real boundary," "A cluster whose connection skips certificate
  checks no longer receives addon secrets" (and the plain-`http://` case
  right after it), and "The Helm chart no longer grants itself a
  cluster-wide read of every Secret" under Breaking changes above.

### Bug fixes

- **A secret that appeared on a cluster between Sharko's ownership check and
  its write is now correctly reported as somebody else's, not as a generic
  failure.** This closes a race in the ownership gate: Sharko never actually
  overwrote another tool's secret either way, but before this fix the
  refusal could show up as a plain error instead of the real "this belongs
  to something else" message.
  ([#797](https://github.com/MoranWeissman/sharko/pull/797))
- **A Secret Sync row that was refused now shows the real reason** instead
  of the generic "the last check didn't finish" — this covers rows refused
  by the new TLS-verification and secret-path-boundary checks above.
  ([#801](https://github.com/MoranWeissman/sharko/pull/801))

### Internal

- Consolidated ArgoCD cluster Secret writer to a single reconciler (`internal/clusterreconciler`). The legacy 3-minute `argosecrets.Reconciler` loop was retired. The `argosecrets.Manager` (pure CRUD writer for direct kubeconfig writes) remains. No user action needed; convergence behavior is unchanged. (Operator Phase 0: PR #588)
- The UI's code now runs through a linter in CI, so obvious front-end mistakes get caught before merge instead of after.
  ([#772](https://github.com/MoranWeissman/sharko/pull/772))
- Real security checks (Go static analysis, a container image scan, a UI
  dependency audit) now run on every pull request, not just at some other
  time.
  ([#795](https://github.com/MoranWeissman/sharko/pull/795))
- The CI job that only ever searched for forbidden text was renamed from
  "Security Scan" to "Forbidden Content Check," so its name matches what it
  actually does. If your fork's branch protection rules or your own
  automation reference the old job name, update them.
  ([#795](https://github.com/MoranWeissman/sharko/pull/795))

---

## v3.0.0 — First public release

!!! danger "v3.0.0 is retired and unsupported"
    `v3.0.0` and every earlier tag are unsafe, retired, and unsupported.
    Install only published `v4.0.1`-or-later artifacts. What went wrong and
    what to do:
    [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md#why-v300-is-retired).

**Status:** released 2026-07-21 — retired, unsafe, unsupported.

v3.0.0 is Sharko's **first public release** — the addon-management server for
Kubernetes clusters, built on ArgoCD. Sharko is a guest on your ArgoCD: Git stays
the source of truth, ArgoCD deploys the workloads, and Sharko owns the
assignment-and-secrets layer between Git and your clusters, with a UI/API/CLI, an
addon catalog, preview-before-change, and an audit trail on top.

### Breaking changes

None applicable — this is the first public release. Prior v2.x tags were
pre-public development milestones.

### What's new

- **GitOps agent** — drift detection + OutOfSync for Sharko-managed clusters, a
  read-only live label diff, and opt-in self-heal (default off).
- **Git-native config** — server settings and connection/AI non-secret fields are
  declarable from Git (Helm → env → git-wins), while encrypted secrets are always
  preserved and never written to Git.
- **Kubernetes events** — Sharko emits Warning/Normal events for AWS/ArgoCD/cluster/
  PR/reconciler activity so failures surface in `kubectl get events`.
- **Addons Marketplace** — searchable catalog with git-native sources and verified
  discovery.
- **"Cluster secret sync"** — the cluster-detail area that keeps each cluster's
  ArgoCD Secret matching Git (addon labels drive ArgoCD's ApplicationSet), renamed
  from the vaguer "GitOps Sync" with an honest explanation.
- **"Why Sharko" guide** — a new docs page explaining the two GitOps loops,
  guest-not-owner positioning, and how Sharko relates to ArgoCD.
- **"Sharko's home cluster" card** — the dashboard now shows where Sharko & ArgoCD
  run (Kubernetes version, node count, health).
- Dozens of first-impression UI fixes across the dashboard, clusters page, and
  addon catalog (the local-walk polish bundle).

### Security

- Secret values are never written to Git — only references/paths.
- Cluster connection credentials are encrypted at rest; the git-native config merge
  preserves encrypted material and emits zero secret env vars.

### Known limitations (honest)

- Status is visible via Sharko's UI/API + Kubernetes events today; a kubectl-native
  desired-state object (an operator) is on the roadmap, not in this release.
- ESO integration is optional and on the near-term roadmap; Sharko manages secrets
  directly today.

---

## v2.3.0 — UX overhaul + cross-account EKS identity

**Status:** released 2026-07-06

v2.3.0 is a UX-focused release that unifies how Sharko talks about status
across the whole UI, fixes the misleading first-run experience, and closes
a real bug where cross-account EKS clusters could mint tokens with the
wrong identity. No breaking changes.

### Breaking changes

No breaking changes — v2.3.0 is a minor release.

### What's new

- **One status vocabulary everywhere.** "Catalog Only" is now two honest
  states — "Not deployed yet" (fine) vs. "Missing from ArgoCD" (a real
  problem) — and every cluster gets a single composite status pill with an
  accessible breakdown instead of up to four separate hover-only pills.
  The confusing purple color is gone; a consistent color law (green =
  working, blue = in progress, amber = needs attention, red = problem, gray
  = inactive) now applies product-wide.
  (V2-cleanup-61.2: PR [#467](https://github.com/MoranWeissman/sharko/pull/467))
- **Honest first run.** A brand-new install no longer shows a green
  "all systems operational" dashboard with nothing connected — it now says
  "Nothing connected yet" and guides you to register a cluster or browse
  the Marketplace. The Marketplace is now the front door for adding
  addons, and a one-time note explains why a change opened a pull request
  instead of applying immediately.
  (V2-cleanup-61.3: PR [#468](https://github.com/MoranWeissman/sharko/pull/468))
- **Clearer navigation.** "Manage" is renamed "Monitor" (it only ever held
  read-only pages) and "Dashboards" is renamed "External Dashboards";
  non-admin users can now reach the Settings page they already had partial
  access to.
  (V2-cleanup-61.3: PR [#468](https://github.com/MoranWeissman/sharko/pull/468))
- **Register-cluster dialog reordered.** You now pick Direct or Discovery
  mode first, then only see the fields that mode needs; Discovery mode
  explains Role ARNs with a worked example. The old, separate Upgrade
  Checker page is gone — its checks now live inside each addon's own
  Upgrade tab, so there's one place to look instead of two.
  (V2-cleanup-61.4: PR [#469](https://github.com/MoranWeissman/sharko/pull/469))
- **Per-cluster Role ARN now works end-to-end.** A Role ARN entered at
  registration is saved and actually used when minting EKS tokens, so a
  cluster found through cross-account discovery authenticates with the
  right identity instead of a default one.
  (V2-cleanup-62.2: PR [#466](https://github.com/MoranWeissman/sharko/pull/466))
- **New EKS live-test harness** (`scripts/eks-live-test.sh`) with a
  companion runbook, for proving the EKS credential path against a real
  cluster end-to-end.
  (V2-cleanup-62.1: PR [#464](https://github.com/MoranWeissman/sharko/pull/464))

### Bug fixes

- **Dark mode, dead links, and stale UI copy fixed** across the app —
  broken deep links into the Clusters page, leftover light-mode-only
  borders, outdated command-palette entries, and addon names that were
  incorrectly capitalized.
  (V2-cleanup-61.1: PR [#465](https://github.com/MoranWeissman/sharko/pull/465))

### Removed

- **Dropped the unused `mkdocs-redirects` plugin.** It started injecting
  an unrelated docs-tool advertisement into every build, and our redirect
  map was empty anyway — removing it fixes the build and drops a
  dependency we never used.
  (PR [#470](https://github.com/MoranWeissman/sharko/pull/470))

## v2.2.1 — Post-release safety fixes

**Status:** released 2026-07-06

v2.2.1 is a patch release that closes five gaps found in a post-release
review of v2.2.0's cluster-connection changes — most importantly, two
ways cluster removal or a bad config file could have deleted an ArgoCD
secret Sharko didn't own. No breaking changes.

### Breaking changes

No breaking changes — v2.2.1 is a patch release.

### Bug fixes

- **Removing a cluster can no longer delete an ArgoCD secret Sharko
  doesn't own.** Removal now checks the secret's own ownership label
  before deleting it, instead of trusting Sharko's local records alone —
  closing a path where a retried removal (after the underlying pull
  request had already merged) could wipe out a connection someone else
  set up.
  (V2-cleanup-60.1: PR [#459](https://github.com/MoranWeissman/sharko/pull/459))
- **An unrecognized config version is now a hard error instead of being
  silently read as "zero clusters."** And if Sharko ever does compute zero
  clusters while ArgoCD secrets clearly still exist, the automatic cleanup
  sweep now holds instead of deleting everything — closing the class of
  bug that could wipe every managed cluster secret at once.
  (V2-cleanup-60.2: PR [#460](https://github.com/MoranWeissman/sharko/pull/460))
- **Fixed a label flip-flop between Sharko's two internal reconcilers** on
  self-managed clusters, where each one kept rewriting the other's legacy
  label spelling back and forth.
  (V2-cleanup-60.3: PR [#461](https://github.com/MoranWeissman/sharko/pull/461))
- **Per-cluster credential routing fixed** — clusters registered with an
  inline kubeconfig now work correctly no matter which secret backend
  (AWS Secrets Manager, Kubernetes Secrets, etc.) is configured for the
  rest of Sharko.
  (V2-cleanup-60.4: PR [#463](https://github.com/MoranWeissman/sharko/pull/463))
- **Cluster registration no longer silently defaults to the EKS-token
  credential path** — you now have to choose it explicitly.
  (V2-cleanup-60.4: PR [#463](https://github.com/MoranWeissman/sharko/pull/463))

### Security

- **Content-policy cleanup** across docs and historical planning files,
  plus assorted CI workflow hardening (matrix-version handling, guard
  against a false-empty check silently passing).
  (V2-cleanup-60.5: PR [#462](https://github.com/MoranWeissman/sharko/pull/462))

## v2.2.0 — sharko.dev identifiers + self-managed connections + System page

**Status:** released 2026-07-05

v2.2.0 moves Sharko's own API group and annotation identifiers to the
maintainer-owned `sharko.dev` domain, adds a first-class "self-managed"
option for ArgoCD connections, and ships the first read-only System page.
No breaking changes for existing installs — old `sharko.io`-prefixed
config is still read automatically.

### Breaking changes

No breaking changes — existing `sharko.io/v1` config and annotations
continue to be read; only newly written config switches to `sharko.dev/v1`.

### What's new

- **Identifiers move to the sharko.dev domain.** Sharko's API group and
  annotation prefixes now read old `sharko.io` names for backward
  compatibility, but emit the new `sharko.dev` names going forward — the
  old domain isn't one the project actually owns.
  (V2-cleanup-59: PR [#457](https://github.com/MoranWeissman/sharko/pull/457),
  V2-cleanup-58: PR [#456](https://github.com/MoranWeissman/sharko/pull/456))
- **"Connection managed by: me"** — a new first-class option for
  ArgoCD connections you set up and manage yourself. Sharko syncs only its
  addon labels onto the connection and never writes or rotates the
  underlying secret.
  (V2-cleanup-57.2: PR [#454](https://github.com/MoranWeissman/sharko/pull/454))
- **New System page (phase 1).** One read-only screen shows the whole
  chain — Sharko, ArgoCD, the Git repo, and clusters — plus the ArgoCD
  version Sharko detected against the range it's tested with.
  (V2-cleanup-57.3: PR [#453](https://github.com/MoranWeissman/sharko/pull/453))
- **Weekly ArgoCD compatibility testing.** A new CI job tests Sharko
  against the three newest ArgoCD minor versions every week and publishes
  the tested range — currently **v3.2–v3.4**.
  (V2-cleanup-57.1: PR [#452](https://github.com/MoranWeissman/sharko/pull/452),
  PR [#455](https://github.com/MoranWeissman/sharko/pull/455))
- **Status now says whose connection it's describing** — ArgoCD's view of
  a cluster vs. Sharko's — and the `/providers` endpoint reports its
  configured secret-name prefix.
  (PR [#450](https://github.com/MoranWeissman/sharko/pull/450),
  PR [#447](https://github.com/MoranWeissman/sharko/pull/447))
- **AI assistant is now opt-in** — hidden by default until enabled.
  (V2-cleanup-55.4: PR [#449](https://github.com/MoranWeissman/sharko/pull/449))

### Bug fixes

- **Certificate-authenticated clusters (kind, kubeadm, on-prem) get the
  right secret shape.** They were previously written as if they were an
  EKS cluster; the secret writer now recognizes client-certificate
  kubeconfigs and gives them a real TLS cluster secret.
  (V2-cleanup-56.1: PR [#451](https://github.com/MoranWeissman/sharko/pull/451))

## v2.1.0 — Secret-backend fix + credential-source clarity

**Status:** released 2026-07-04

v2.1.0 is a feature release that restores cluster registration through the
configured secret backend, adds an explicit choice for how each cluster's
credentials are supplied, and cleans up a large amount of unreachable UI.
No breaking changes.

### Breaking changes

No breaking changes — v2.1.0 is a minor release.

### What's new

- **Cluster registration uses your configured secret backend again.**
  Both AWS Secrets Manager and Kubernetes Secrets work as registration
  targets, and saving a connection now hot-reloads the credential provider
  without a restart.
  (V2-cleanup-53.1: PR [#444](https://github.com/MoranWeissman/sharko/pull/444))
- **Explicit choice for how Sharko gets a cluster's credentials** at
  registration time — paste a kubeconfig inline, point at a secret, or use
  an EKS token — instead of one silent default.
  (creds-reframe-1/2/3: PRs [#433](https://github.com/MoranWeissman/sharko/pull/433),
  [#434](https://github.com/MoranWeissman/sharko/pull/434))
- **UI labels whose connection a status is showing** (ArgoCD → cluster vs.
  Sharko → cluster) and explains each credential-source option in plain
  English.
  (V2-cleanup-55.3: PR [#447](https://github.com/MoranWeissman/sharko/pull/447))
- **Deleted ~1,900 lines of unreachable UI** — old Connections, Docs, and
  Version-Matrix views plus stale styling left over from earlier
  redesigns.
  (PR [#442](https://github.com/MoranWeissman/sharko/pull/442))
- **Pre-publicity documentation honesty pass** — accurate Apache-2.0
  license claim, a "why not just ApplicationSets?" explainer, honest
  CNCF-progress wording, and a new page explaining what happens if you
  remove Sharko.
  (V2-cleanup-54.1: PR [#443](https://github.com/MoranWeissman/sharko/pull/443))

### Bug fixes

- **Shared credential-lookup fix** — Diagnose, adopt, remove, addon
  operations, and the secrets endpoint all resolve a cluster's stored
  secret path correctly now, instead of some of them passing the raw
  cluster name.
  (V2-cleanup-55.1: PR [#448](https://github.com/MoranWeissman/sharko/pull/448))
- **Settings secrets-provider page only shows real backend choices** (no
  more confusing aliases), and its prefix field round-trips correctly on
  save.
  (V2-cleanup-55.2: PR [#446](https://github.com/MoranWeissman/sharko/pull/446))

## v2.0.3 — Bootstrap addon-deployment fix

**Status:** shipped as part of v2.1.0 (2026-07-04) — no standalone v2.0.3
build was ever tagged; the fix below landed under the v2.1.0 release
instead.

v2.0.3 is a patch release that fixes a high-severity bug in the bootstrap
Helm chart: it read the addon catalog at the wrong path, so the chart
generated no ArgoCD ApplicationSets and **no addon ever deployed to any
cluster**. No breaking changes.

### Breaking changes

No breaking changes — v2.0.3 is a patch release.

### Bug fixes

- **Bootstrap chart now actually deploys addons.** The chart that turns the
  addon catalog into ArgoCD ApplicationSets read the addon list from the
  top level (`.Values.applicationsets`), but Sharko writes the catalog under
  the `sharko.io/v1` envelope, so the list lives at
  `.Values.spec.applicationsets`. The mismatch meant the chart rendered zero
  AppProjects and zero ApplicationSets regardless of catalog contents — every
  addon showed "not deployed." The template (and the namespace helper, which
  additionally referenced a non-existent `appName` field instead of `name`)
  now read the catalog at the correct enveloped path. A new render regression
  test renders the bootstrap chart against an enveloped catalog and fails if
  it produces no ApplicationSet, and CI's Helm job now renders the bootstrap
  chart on every PR so this can never silently regress.
  (V2-cleanup-17)

### Upgrading — existing repos need a manual one-time fix

The fix above is in Sharko's source templates, so **newly initialized**
repos get the corrected chart automatically. But repos that were already
initialized by an earlier Sharko version still carry the old, broken
bootstrap chart committed in Git — upgrading Sharko does **not** rewrite
those files. Until you apply the fix below, addons in those repos will keep
showing "not deployed."

Sharko has no "re-bootstrap" or "update the bootstrap chart" command today:
`sharko init` refuses to run on an already-initialized repo, and no
reconciler owns the bootstrap chart files. So apply this 2-line correction
by hand in your config repo (the one Sharko commits to), then commit it:

1. In `bootstrap/templates/addons-appset.yaml`, change both
   `.Values.applicationsets` references to `.Values.spec.applicationsets`.
2. In `bootstrap/templates/_helpers.tpl`, change `$.Values.applicationsets`
   to `$.Values.spec.applicationsets` (and, if your copy of the
   `addon.namespacesCSV` helper references `$a.appName`, change it to
   `$a.name`).

Once committed and synced, ArgoCD will generate the missing ApplicationSets
and your enabled addons will deploy.

> **Backlog:** an automated "refresh the bootstrap chart in an existing
> repo" flow (e.g. a `sharko init --update` / re-bootstrap path, or a
> reconciler that owns the bootstrap chart) is tracked as a follow-up so
> future template fixes reach existing repos without manual edits.

## v2.0.2 — First-run smoke-test fixes

**Status:** released 2026-06-07

v2.0.2 is a patch release that closes the issues the maintainer found
during hands-on first-run testing of v2.0.1 — kubeconfig cluster
registration, cluster removal, the marketplace add-addon flow, the setup
wizard, and dev tooling. No breaking changes.

### Breaking changes

No breaking changes — v2.0.2 is a patch release.

### What's new

- **Setup wizard now probes the Git repo state before offering to
  initialize it** — Step 4 carries honest wording so operators aren't told
  an already-initialized repo is empty.
  (V2-cleanup-9: PR [#392](https://github.com/MoranWeissman/sharko/pull/392))
- **Marketplace "Add addon to catalog" flow reaches parity with the init
  flow** — an auto-merge/manual toggle that actually takes effect, a dry-run
  preview of the catalog files that will be written, step-by-step Git
  progress, a clickable pull-request link, and post-submit navigation (to
  the new addon if it auto-merged, or to the pending-PR list if manual).
  (V2-cleanup-14: PR [#397](https://github.com/MoranWeissman/sharko/pull/397))
- **Curated design-history docs page added; remaining v1.x user-facing
  cruft removed** from the docs.
  (V2-cleanup-7: PR [#388](https://github.com/MoranWeissman/sharko/pull/388))

### Bug fixes

- **Kubeconfig cluster registration now works end-to-end.** Three fixes
  combine: registration writes the ArgoCD cluster Secret directly
  ([#391](https://github.com/MoranWeissman/sharko/pull/391)); that Secret is
  protected from the orphan-sweep reconciler during the registration
  window, and the bootstrap probe lists ArgoCD apps instead of fetching one
  by name ([#394](https://github.com/MoranWeissman/sharko/pull/394)); and the
  reconcilers preserve the bearer token so a kubeconfig cluster's Secret
  keeps its correct shape instead of being rebuilt into a broken AWS
  exec-plugin form ([#395](https://github.com/MoranWeissman/sharko/pull/395)).
  (V2-cleanup-8.2/8.3, 11, 12)
- **Cluster removal now honors the auto-merge choice**, like the init and
  register flows — previously the removal pull request was silently left
  open even when auto-merge was selected.
  (V2-cleanup-13: PR [#396](https://github.com/MoranWeissman/sharko/pull/396))
- **Clearer ArgoCD permission handling in the dev environment** — the dev
  install grants the admin apiKey RBAC, and Sharko gives a clearer message
  when ArgoCD denies its token.
  (V2-cleanup-10: PR [#393](https://github.com/MoranWeissman/sharko/pull/393))

### Maintainer tooling

- **`scripts/sharko-dev.sh` reliability fixes** — corrected preflight
  arithmetic, logging routed to stderr, a next-steps footer, and the
  reconciler enabled in the dev install.
  (V2-cleanup-8.1: PR [#390](https://github.com/MoranWeissman/sharko/pull/390))

## v2.0.1 — Release pipeline fix

**Status:** released 2026-06-04

### Bug fixes

- **Release pipeline** — dropped Windows from GoReleaser so release runs
  cleanly on the GitHub Actions Linux runners. No functional change to
  the Sharko binary; Linux and macOS artifacts are unaffected.

## v2.0.0 — Production launch

**Status:** released 2026-06-03

### Breaking changes

No breaking changes — v2.0.0 is the first production release of Sharko.

### What's new

- **Performance baselines + SLO targets per critical path** — p50 / p95 / p99
  measurements per phase per surface across cluster registration, addon
  cycle, catalog scan, and dashboard read paths; SLO targets + error
  budgets + burn-rate thresholds documented; a workflow_dispatch
  perf-baseline-refresh job and a comparator binary with `-emit` mode
  gate every PR against the committed baselines.
  (V2-1: PRs [#362](https://github.com/MoranWeissman/sharko/pull/362),
  [#363](https://github.com/MoranWeissman/sharko/pull/363),
  [#364](https://github.com/MoranWeissman/sharko/pull/364),
  [#365](https://github.com/MoranWeissman/sharko/pull/365))
- **100% slog logging with correlation IDs and sensitive-field redaction** —
  all internal callers migrated from stdlib `log` to `log/slog`;
  `request_id` propagated across middleware, reconciler, prtracker,
  orchestrator, and API handlers; a slog.Handler wrapper redacts tokens,
  kubeconfigs, and secret bodies before they hit any sink.
  (V2-2: PRs [#367](https://github.com/MoranWeissman/sharko/pull/367),
  [#368](https://github.com/MoranWeissman/sharko/pull/368),
  [#369](https://github.com/MoranWeissman/sharko/pull/369))
- **Prometheus telemetry for SLO surfaces** — histogram + counter
  exposition with V2-1.2-sized buckets, OpenTelemetry-conventional
  metric naming, exemplars carrying `request_id`, a Helm-shipped
  PrometheusRule template with multi-window multi-burn-rate alerting
  rules, and an operator runbook covering every alert.
  (V2-3: PRs [#371](https://github.com/MoranWeissman/sharko/pull/371),
  [#372](https://github.com/MoranWeissman/sharko/pull/372),
  [#373](https://github.com/MoranWeissman/sharko/pull/373))
- **CNCF foundation docs and GitHub config** — `MAINTAINERS`,
  `GOVERNANCE`, `CODE_OF_CONDUCT` (Contributor Covenant 2.1),
  `CONTRIBUTING`, `SECURITY`, and `ADOPTERS` at the repo root; DCO
  `Signed-off-by` enforcement; YAML issue templates (bug / feature /
  docs / security); GitHub Discussions enabled with a Roadmap input
  category.
  (V2-6 subset: PR [#366](https://github.com/MoranWeissman/sharko/pull/366))
- **Operator runbook coverage for the 57 inventoried failure modes** —
  runbook style guide + failure-mode index (57 modes: P0=12, P1=28,
  P2=12) shipped first; 35 new runbooks landed in 3 sequential PRs
  (12 P0 + 11 P1 Providers/Catalog + 14 P1
  API/Orchestrator/Reconciler/Webhook/AI/Adopt); a style-compliance
  refresh closed the gap on 9 existing pages. Every operator-facing
  failure mode in the P0+P1 tiers now has a Symptoms → Diagnosis →
  Mitigation → Root cause → Prevention runbook.
  (V2-4: PRs [#375](https://github.com/MoranWeissman/sharko/pull/375),
  [#376](https://github.com/MoranWeissman/sharko/pull/376),
  [#377](https://github.com/MoranWeissman/sharko/pull/377),
  [#378](https://github.com/MoranWeissman/sharko/pull/378),
  [#379](https://github.com/MoranWeissman/sharko/pull/379))
- **Public roadmap + API stability contract** — a community roadmap
  page captures the v3+ trajectory (fine-grained RBAC, SSO,
  multi-ArgoCD, operator mode, rule-based auto-merge); an API
  stability page tiers all 128 endpoints (95 stable / 26 beta / 7
  alpha) with a deprecation policy (1 MINOR version lead time,
  `// Deprecated:` doc-comment + release-notes entry + WARN log +
  removal in subsequent minor).
  (V2-6.3: PR [#380](https://github.com/MoranWeissman/sharko/pull/380))
- **v2.0.0 threat model + 3rd-party security review bundle** — a
  STRIDE-per-trust-boundary threat model covering 6 primary boundaries
  × 6 STRIDE categories (36 cells), 40 mitigations (~95% citing
  V2-shipped artifacts), and 11 residual-risk gaps; a
  security-review-prep bundle ready for an external consultant
  (CNCF-coordinated or directly contracted). Disclosure SLO formalized:
  5 business days acknowledgment, 30-day HIGH fix, 90-day MEDIUM.
  (V2-6.5: PR [#381](https://github.com/MoranWeissman/sharko/pull/381))

### Removed

*Nothing removed. v2.0.0 is the first production release, so there is
no prior production line to drop compat code for.*

### Security

- **Bootstrap admin credential no longer in structured logs** — the
  auto-generated bootstrap admin password is now displayed on stdout at
  first start (visible to operators watching `kubectl logs`) but is
  structurally absent from slog emissions. The
  `sharko-initial-admin-secret` Kubernetes Secret remains the
  authoritative retrieval path. Defense-in-depth: a regression test
  asserts the password field cannot appear in the structured-log buffer
  even if the V2-2.4 RedactHandler wrapper is bypassed in a future
  refactor.
  ([#382](https://github.com/MoranWeissman/sharko/pull/382))
- **STRIDE threat model published** — see the V2-6.5 entry under
  "What's new" for the full surface analysis, mitigation inventory,
  and residual-risk gap catalogue.
  ([#381](https://github.com/MoranWeissman/sharko/pull/381))

### Bug fixes

- **`internal/auth/store.go::MaybeLogBootstrapCredential` no longer
  emits the bootstrap admin password as a structured slog attribute.**
  See the entry under "Security" for the full fix shape and
  defense-in-depth regression contract.
  ([#382](https://github.com/MoranWeissman/sharko/pull/382))
