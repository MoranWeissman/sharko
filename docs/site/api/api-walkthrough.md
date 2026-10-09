# API Walkthrough — Do Anything the UI Does, From the Command Line

This guide walks you through driving Sharko entirely through its HTTP API, the
same way the web UI does behind the scenes. The idea is simple: **fire a call,
watch ArgoCD react, then read the result back from Sharko and confirm the two
agree.**

It is written for the maintainer, not a developer. Every section first says, in
plain words, what the action *does* to your clusters, then gives you the exact
`curl` command, then shows what a good answer looks like and (where it matters)
the ArgoCD command to watch the effect land.

Almost every write here works by opening a pull request in your GitOps repo.
Depending on your global auto-merge setting, that PR either merges itself (and
the change goes live within a sync cycle) or waits for you to merge it by hand.
The API response always tells you the PR URL so you can find it.

!!! note "When in doubt, check the live spec"
    The authoritative request and response shapes live in
    `docs/swagger/swagger.json`, and you can click through them in your browser
    at `http://localhost:8080/swagger/`. Where a request body has many optional
    fields, this guide shows the common ones and points you to swagger for the
    rest rather than guessing.

---

## 1. Setup

### Where Sharko lives

In the dev environment, Sharko runs inside the kind cluster `sharko-e2e`, in the
`sharko` namespace, and is reached at `http://localhost:8080` through the dev
script's port-forward. If `http://localhost:8080` isn't responding, the
port-forward is probably down — bring it back with `./scripts/sharko-dev.sh
install` (or a rebuild).

You'll need `curl`, `jq`, `kubectl`, and (optionally) the `argocd` CLI on your
macOS/zsh shell.

### Log in and grab a token

This logs in as `admin` and exports two shell variables: `TOKEN` (your bearer
token for every authenticated call) and `ADMIN_PW` (the admin password).

```bash
eval "$(./scripts/sharko-dev.sh login --export)"
```

Under the hood that does a `POST /api/v1/auth/login` with
`{"username":"admin","password":"..."}` and reads the `token` field out of the
`{"token":"...","username":"...","role":"..."}` response. You never need to call
that endpoint by hand — the one-liner above is the clean way.

### Set up shortcuts

So the rest of this guide stays readable, set a base-URL variable and an auth
header array once:

```bash
SH="http://localhost:8080/api/v1"
auth=(-H "Authorization: Bearer $TOKEN")
```

Now every authenticated call looks like `curl "${auth[@]}" "$SH/..."`.

### Smoke-ping Sharko

Confirm you're actually talking to Sharko and your token works.

**Health** — no auth needed. Tells you Sharko is up, its version, and whether
cluster connectivity testing is available on this connection.

```bash
curl -s "$SH/health" | jq
```

A good answer:

```json
{
  "status": "healthy",
  "version": "<your running Sharko version>",
  "mode": "Kubernetes",
  "cluster_test_available": true
}
```

**Config** — authenticated. Returns the active connection's configuration that
the UI reads on load.

```bash
curl -s "${auth[@]}" "$SH/config" | jq
```

If `config` returns data (not a 401), your token is good and you're ready.

---

## 2. Clusters

### List all clusters

Shows every cluster Sharko knows about, with a high-level status for each. This
is what the Clusters page renders.

```bash
curl -s "${auth[@]}" "$SH/clusters" | jq
```

### Get one cluster and its addon statuses

The detail view for a single cluster — its server URL, region, and the per-addon
status (which addons are enabled and how their ArgoCD Applications are doing).
This is the call you'll use again and again to confirm an addon change took
effect.

```bash
curl -s "${auth[@]}" "$SH/clusters/my-cluster" | jq
```

### Register a new cluster

Adds a brand-new cluster: registers it in ArgoCD and creates its GitOps
configuration via a PR.

The first thing to settle is one question: **how should Sharko get this
cluster's credentials?** There are three honest answers, and you pick one with
the optional `creds_source` field:

| `creds_source` | What it means | You supply |
|----------------|---------------|------------|
| `inline-kubeconfig` | Legacy, **off by default**: you paste a kubeconfig right in the request. Bearer-token auth only. Refused unless an admin has turned on **Allow legacy inline credentials** in Settings. | `kubeconfig` (the YAML) |
| `secret-kubeconfig` | You point at a kubeconfig already stored in your secret backend. Works for **any** cluster, including local / on-prem. | `secret_path` |
| `eks-token` | Sharko mints a short-lived token from your EKS cloud identity. Amazon EKS only. | `region` (+ `role_arn` for a cross-account cluster) |

Required field in every case: `name` (alphanumeric, may contain hyphens, must
start with a letter or digit). The `addons` field is a map of addon name to
on/off — **on a v4 (migrated) repo this field is silently ignored**;
registration is addon-free there, and you enable each addon afterward via
`POST /v4/clusters/{name}/addons/{addon}` (section 3 below).

#### Paste a kubeconfig (`inline-kubeconfig`) — legacy, off by default

You hand Sharko the kubeconfig YAML directly in the request. The kubeconfig must
use bearer-token auth.

!!! warning "Refused unless an admin enables the legacy setting"
    A pasted credential exists only in the live ArgoCD cluster Secret — it is
    never stored in Git and cannot be recovered from Git if that Secret is
    lost. That is why this path is **off by default**: the server refuses an
    inline registration with a plain message pointing at the supported
    credential providers, until an admin turns on **Allow legacy inline
    credentials** in Settings. To move an existing pasted connection onto a
    supported provider, see
    [Migrating Off Pasted (Inline) Credentials](../operator/migrate-inline-credentials.md).

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "my-cluster",
        "creds_source": "inline-kubeconfig",
        "kubeconfig": "apiVersion: v1\nkind: Config\n...",
        "addons": { "keda": true }
      }' | jq
```

#### Point at a stored kubeconfig (`secret-kubeconfig`)

The kubeconfig already lives in your configured secret backend (AWS Secrets
Manager, GCP Secret Manager, Azure Key Vault, or a Kubernetes Secret). You give
Sharko the path/name to look it up. The secret holds a raw kubeconfig YAML. This
works for **any** cluster type — including a local or on-prem cluster that has
nothing to do with EKS.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "my-cluster",
        "creds_source": "secret-kubeconfig",
        "secret_path": "clusters/prod/my-cluster",
        "addons": { "keda": true }
      }' | jq
```

#### Amazon EKS token (`eks-token`)

Sharko mints a short-lived token from your EKS cloud identity, so you don't store
or paste any long-lived credential. You give it the cluster's region.

For a cluster in **another AWS account** (or any cluster Sharko's own identity
can't reach directly), also pass `role_arn` — the IAM role Sharko should assume
when minting tokens for this cluster. The role is recorded on the cluster's
`managed-clusters.yaml` entry as `roleArn`, so every later token mint uses the
same identity. Discovery does this for you: clusters found by scanning a
cross-account role carry that role into registration automatically. If the
secret payload in your backend carries its own `roleArn`, that wins (it's the
most specific); then this per-cluster `role_arn`; then the connection-level
default.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "my-cluster",
        "creds_source": "eks-token",
        "region": "us-east-1",
        "role_arn": "arn:aws:iam::111122223333:role/example",
        "addons": { "keda": true }
      }' | jq
```

A good answer is HTTP 201 with a body whose `status` is `"success"` and a `git`
block containing the `pr_url`. A bad combination — for example
`inline-kubeconfig` with no `kubeconfig`, or `eks-token` with no `region` —
comes back as a 400 with a clear message telling you what's missing.

!!! note "`creds_source` is optional — old requests still work"
    `creds_source` was added on top of the existing fields, and it's optional.
    If you leave it out, Sharko figures out the credential source from the fields
    you do send, exactly as it always has: `provider: "kubeconfig"` with a
    `kubeconfig` still means paste, and a request with a `secret_path` still
    means look it up in the backend. **Every request that worked before keeps
    working unchanged.** When you do set `creds_source`, it wins, and `provider`
    becomes optional cluster-type metadata.

!!! tip "Preview first"
    Add `"dry_run": true` to see which files would be written, the line-by-line
    diff of each change (secret values redacted), and which secrets would be
    created, with no side effects. The preview comes back under a `dry_run` key.

### Test connectivity

Checks that Sharko can actually reach the cluster, by doing a small secret
create/read/delete cycle (Stage 1). Send `{"deep": true}` to additionally run an
ArgoCD round-trip (Stage 2).

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/test" \
  -H "Content-Type: application/json" \
  -d '{"deep": false}' | jq
```

A good answer has `"success": true`. If the active connection has no secrets
backend wired up, you'll get a 503 with `"error_code": "no_secrets_backend"` —
that means the *test feature* is unavailable, not that the cluster is down.

### Check permissions

Runs a deeper set of permission/namespace checks against the cluster and returns
a report with suggested fixes. No request body needed. (This is the "Check
permissions" button on the Cluster Detail page — the endpoint name
(`/diagnose`) is unchanged.)

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/diagnose" | jq
```

### Adopt existing ArgoCD clusters

If a cluster is already registered in ArgoCD but Sharko isn't managing it yet,
adoption brings it under Sharko management: it verifies connectivity per cluster,
then creates the GitOps config via a PR. The body is a list of cluster names.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/adopt" \
  -H "Content-Type: application/json" \
  -d '{ "clusters": ["my-cluster"] }' | jq
```

Add `"dry_run": true` to preview. A mixed batch (some succeed, some fail) comes
back as HTTP 207 with a per-cluster `results` array.

### Un-adopt

Reverses adoption: removes Sharko management but **keeps** the ArgoCD secret.
Confirmation is required — set `"yes": true`.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/unadopt" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq
```

Without `yes: true` (and without `dry_run`) you get a 400 asking for
confirmation. If the cluster was never adopted, you get a 409.

### Remove a cluster

Removes a cluster, with a configurable cleanup scope. Confirmation is required —
set `"yes": true`.

- `"cleanup": "all"` (default) — remove the Git config and clean up ArgoCD plus
  remote secrets.
- `"cleanup": "git"` — remove the Git config only.
- `"cleanup": "none"` — drop the managed-clusters entry only.

```bash
curl -s "${auth[@]}" -X DELETE "$SH/clusters/my-cluster" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true, "cleanup": "all" }' | jq
```

Add `"dry_run": true` to preview the removal first.

---

## 3. Addons on a cluster — the core loop

This is the heart of day-to-day work: turning addons on and off for a given
cluster. Two words matter here, and Sharko uses them consistently everywhere:
the **Marketplace** is the read-only list of addons you *could* run; the
**Catalog** (`catalog.yaml` in your GitOps repo) is the list your org has
*approved*. A cluster can only run an addon that is already in the Catalog —
so the flow below is always: get it into the Catalog first (section 4), then
enable it on a cluster (this section). Every write here runs the same three
steps — Sharko validates the request, you can preview it with `dry_run`, and
a real call opens a pull request — so nothing lands outside a reviewable PR.

Each call opens a PR (and may auto-merge per your global setting). Once the
PR is merged, ArgoCD creates or removes an Application named
`<addon>-<cluster>` and syncs it.

!!! note "These are the v4 routes"
    The endpoints below (`/v4/clusters/...`) are for a repo that has gone
    through the [one-pull-request migration](../operator/migrating-to-the-new-format.md).
    The older `POST /clusters/{name}/addons/{addon}` and
    `DELETE /clusters/{name}/addons/{addon}` routes still exist for a repo
    that has not migrated yet, but on a migrated (v4) repo they refuse with
    HTTP 409 and a `repo_layout` code — they write files a v4 repo does not
    use. Same for `PATCH /clusters/{name}` (the old bulk on/off toggle):
    it is not yet available on v4 repos, so call the endpoint below once per
    addon instead.

### Enable an addon

Turns one addon on for one cluster by writing `cluster-addons/my-cluster.yaml`
(and, if you send `values`, `values/clusters/my-cluster/keda.yaml`).
**Validation runs first, before anything is written**: the addon must
already be in your Catalog, and every value or secret its catalog entry
requires must be present. A validation failure comes back as HTTP 422 naming
exactly what's missing — nothing is written, not even a branch.
**Confirmation is required — set `"yes": true`** (or use `dry_run` to preview,
which also runs validation).

```bash
curl -s "${auth[@]}" -X POST "$SH/v4/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq
```

A good answer is HTTP 200 with a `git` block carrying the `pr_url`. Use
`"dry_run": true` to see the same validation and a file-by-file preview with
no side effects.

A few things that go wrong, and how you'll know:

| What happened | Status | Body |
|---|---|---|
| Addon isn't in your Catalog yet | 422 | `code: "not_in_catalog"` — add it first, `POST $SH/catalog/addons` |
| Catalog entry is missing chart/repo/version | 422 | `code: "incomplete_entry"`, `problems` names each missing piece |
| Cluster is missing a required value or secret | 422 | `code: "validation_failed"`, `problems` names each one |
| Cluster isn't registered | 404 | `code: "cluster_not_found"` |
| Repo hasn't migrated, or has both layouts at once | 409 | `code: "repo_layout"` |

Branch on `code`, never on the message text — the wording can change.

**Watch ArgoCD react.** Once the PR merges, an Application appears and syncs:

```bash
kubectl --context kind-sharko-e2e get applications -n argocd | grep keda-my-cluster
kubectl --context kind-sharko-e2e get application keda-my-cluster -n argocd -o yaml
# or, if you're logged into the argocd CLI:
argocd app get keda-my-cluster
```

**Read the status back from Sharko** and confirm it agrees with what ArgoCD
shows (Synced / Healthy):

```bash
curl -s "${auth[@]}" "$SH/clusters/my-cluster" | jq '.addons // .'
```

### Disable an addon

Turns one addon off for one cluster. By default the addon's entry in
`cluster-addons/my-cluster.yaml` is kept with `enabled: false` set (a
one-word change to turn it back on later); send `"remove": true` to delete
the entry outright instead. **Confirmation is required — set `"yes": true`.**

```bash
curl -s "${auth[@]}" -X DELETE "$SH/v4/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq
```

After the PR merges, the `keda-my-cluster` Application is removed from ArgoCD.
Confirm it's gone:

```bash
kubectl --context kind-sharko-e2e get applications -n argocd | grep keda-my-cluster || echo "removed"
```

Add `"dry_run": true` to preview.

### Restart a stuck sync

If an addon's ArgoCD sync is wedged (stale or permanently failing), this
terminates any in-flight sync operation and immediately re-triggers a fresh one —
without you having to open the ArgoCD UI. No request body. Unchanged by the v4
data-file format — it talks to ArgoCD, not to your Git repo.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/addons/keda/restart-sync" | jq
```

A good answer: `{"terminated": true|false, "synced": true}`. (`terminated` is
`true` only when there really was an operation in flight to cancel.) If the
Application doesn't exist in ArgoCD, you get a 404.

---

## 4. Catalog and Marketplace

**Marketplace** is what you could run — the curated list Sharko ships (plus
any third-party feeds an operator configured), read-only, discovery only.
Nothing here is deployed and nothing here is approved.

**Catalog** is what your org allows — your own `catalog.yaml`, read straight
from your GitOps repo. A brand-new repo has an **empty Catalog on purpose**:
nothing runs in your org that nobody in your org chose. The only door from
Marketplace into Catalog is the pull request `POST /catalog/addons` opens —
that's the approval, and it's the only way in, for every source, forever. See
[Marketplace Architecture](../operator/marketplace-architecture.md) for the
full model.

### Browse the Marketplace

The curated, discovery-only list Sharko ships. Read-only — nothing here
writes to your repo.

```bash
curl -s "${auth[@]}" "$SH/marketplace/addons" | jq
```

The feeds behind that list (the embedded one plus any third-party URLs) with
per-source fetch status:

```bash
curl -s "${auth[@]}" "$SH/marketplace/sources" | jq
```

### List your org's Catalog

What your org has actually approved — read straight from `catalog.yaml`. A
repo with no `catalog.yaml` yet returns an empty list, not an error.

```bash
curl -s "${auth[@]}" "$SH/catalog/addons" | jq
```

Each entry carries `origin: "curated"` when the Marketplace also knows this
addon by name, or `"internal"` when only your own entry describes it. An
entry with a piece missing (say, no chart repo yet) comes back with
`"deployable": false` and a `missing_fields` list instead of failing the
whole read.

### Add an addon to the Catalog

This is the approval step — the pull request it opens is the only way
anything enters your org. One endpoint, three shapes:

**Pick one from the Marketplace.** Set `from_marketplace: true` and Sharko
fills in the chart location, default namespace, and needed-secrets list from
the curated entry. Leave `version` empty and Sharko resolves it to the
newest version it knows for the chart — that resolved pin is what actually
lands in `catalog.yaml`, and the response tells you which one it picked:

```bash
curl -s "${auth[@]}" -X POST "$SH/catalog/addons" \
  -H "Content-Type: application/json" \
  -d '{
        "addons": [
          { "name": "keda", "from_marketplace": true }
        ]
      }' | jq
```

```json
{
  "added": ["keda"],
  "resolved_versions": { "keda": "2.17.2" },
  "git": { "pr_url": "https://github.com/example-org/sharko-addons/pull/42" }
}
```

**Type in your own chart.** No `from_marketplace` — give the chart location
and version yourself. Required: `repo_url`, `chart`, `version`.

```bash
curl -s "${auth[@]}" -X POST "$SH/catalog/addons" \
  -H "Content-Type: application/json" \
  -d '{
        "addons": [
          {
            "name": "keda",
            "repo_url": "https://kedacore.github.io/charts",
            "chart": "keda",
            "version": "2.17.2",
            "namespace": "keda"
          }
        ]
      }' | jq
```

**Add and enable in one pull request.** Set `enable_on_cluster` to also
switch every addon in the request on for that cluster — one PR touching
`catalog.yaml` and `cluster-addons/<cluster>.yaml` together, so a reviewer
sees both halves in one diff. This half changes what runs on a real cluster,
so it needs `"yes": true` (or `dry_run` to preview first):

```bash
curl -s "${auth[@]}" -X POST "$SH/catalog/addons" \
  -H "Content-Type: application/json" \
  -d '{
        "addons": [ { "name": "keda", "from_marketplace": true } ],
        "enable_on_cluster": "my-cluster",
        "yes": true
      }' | jq
```

A good answer is HTTP 201 with `added` (and `enabled` + `cluster` for the
combined form). Several addons in one `addons` list still produce exactly
one pull request — what the first-run wizard needs when someone ticks five
boxes at once. Every 4xx body carries a machine-readable `code` next to the
message, the same branch-on-`code`-not-text rule as enabling an addon:
`invalid_request` (400); `cluster_not_found` (404); `repo_layout` (409); and
on 422, one of `confirmation_required`, `empty_catalog_file`,
`not_in_marketplace`, `version_required`, `incomplete_entry` (with
`problems`), or `validation_failed` (with `problems`).

### Get one approved addon

```bash
curl -s "${auth[@]}" "$SH/catalog/addons/keda" | jq
```

A 404 means it isn't in your Catalog — add it first.

### Upgrade an addon's pinned version

Bumps a cluster's chart-version pin for an addon already enabled there.

```bash
curl -s "${auth[@]}" -X POST "$SH/addons/keda/upgrade-clusters" \
  -H "Content-Type: application/json" \
  -d '{ "clusters": ["my-cluster"], "version": "2.18.0", "yes": true }' | jq
```

!!! note "Older global-upgrade routes are v3-only"
    `POST /addons/{name}/upgrade` and `POST /addons/upgrade-batch` (a
    catalog-wide version bump, plus its multi-addon batch form) both write
    the pre-v4 `addons-catalog.yaml` and refuse with 409 `repo_layout` on a
    migrated repo. On v4 there is no fleet-wide version — each cluster pins
    its own version (or follows the Catalog's default) via
    `upgrade-clusters` above.

---

## 5. Values

Values are the Helm values YAML that configure an addon. There are two layers:
the **global** defaults for an addon, and **per-cluster** overrides for one addon
on one cluster. Both write through a PR.

!!! note "Not yet on v4 repos"
    The values-editor endpoints below all refuse with HTTP 409 on a
    migrated (v4) repo today — "this editor doesn't support the v4 layout
    yet". On a v4 repo, edit `values/global/<addon>.yaml` or
    `values/clusters/<cluster>/<addon>.yaml` directly in Git instead (`sharko
    validate-config` checks them before you commit), or set a per-cluster
    override at enable time via the `values` field on
    `POST /v4/clusters/{cluster}/addons/{addon}` (section 3 above). A
    dedicated v4 values editor is planned, not shipped yet.

### Get global addon values

Returns the addon's current global values YAML (plus an optional JSON Schema the
UI uses for form mode).

```bash
curl -s "${auth[@]}" "$SH/addons/keda/values-schema" | jq
```

### Set global addon values

Replaces the **full** global values file for an addon and opens a PR. The body is
JSON with a `values` field whose string is the entire YAML file (not a diff), so
the PR shows a clean before/after. Sharko validates that the YAML parses before
committing.

```bash
curl -s "${auth[@]}" -X PUT "$SH/addons/keda/values" \
  -H "Content-Type: application/json" \
  -d '{ "values": "keda:\n  resources:\n    limits:\n      memory: 256Mi\n" }' | jq
```

There's also a `"refresh_from_upstream": true` mode that regenerates the file
from the chart's upstream `values.yaml` at the pinned version, ignoring any
`values` you send — see swagger for that flow.

### Get per-cluster addon overrides

Returns the YAML for one addon's section in one cluster's overrides file.
`current_overrides` is empty when no overrides exist yet.

```bash
curl -s "${auth[@]}" "$SH/clusters/my-cluster/addons/keda/values" | jq
```

### Set per-cluster addon overrides

Replaces the overrides for one addon on one cluster and opens a PR. Here the
`values` field is **only that addon's section** (not the whole file) — Sharko
merges it into the cluster's overrides file, leaving other addons untouched. Send
an empty `values` string to remove this addon's overrides entirely.

```bash
curl -s "${auth[@]}" -X PUT "$SH/clusters/my-cluster/addons/keda/values" \
  -H "Content-Type: application/json" \
  -d '{ "values": "replicaCount: 2\n" }' | jq
```

A request naming an addon not in your catalog comes back as 422.

---

## 6. Status, dashboard, audit, and PRs

These are all read-only views that tell you the state of your fleet.

### Dashboard stats

The headline numbers the dashboard home page shows (cluster counts, addon
counts, how many things need attention, and so on).

```bash
curl -s "${auth[@]}" "$SH/dashboard/stats" | jq
```

### Pull requests

The PRs Sharko has opened against your GitOps repo and is tracking — this is how
you see whether a change you fired is still open, merged, or waiting on you.

```bash
curl -s "${auth[@]}" "$SH/dashboard/pull-requests" | jq
```

### Audit log

A record of who did what, when — every register, enable, disable, upgrade, and
values edit lands here. Useful for confirming an action was recorded. It holds
what happened **since Sharko started**: the feed is held in memory only and empties on
a pod restart, and nothing else keeps a copy — see
[what Sharko keeps and for how long](../operator/audit-log.md).

```bash
curl -s "${auth[@]}" "$SH/audit" | jq
```

---

## A full cycle in one block

This ties it all together: log in, approve an addon into the Catalog, enable
it on a cluster, watch the ArgoCD Application go healthy, then read the
status back from Sharko and confirm the two agree.

```bash
# 0. Shortcuts + token
eval "$(./scripts/sharko-dev.sh login --export)"
SH="http://localhost:8080/api/v1"
auth=(-H "Authorization: Bearer $TOKEN")

# 1. Confirm we're talking to Sharko
curl -s "$SH/health" | jq -r '.status'        # -> healthy

# 2. Approve keda into the Catalog (opens a PR against catalog.yaml; may auto-merge)
#    Skip this step if keda is already in your Catalog — GET $SH/catalog/addons/keda
#    tells you (404 means not yet approved).
curl -s "${auth[@]}" -X POST "$SH/catalog/addons" \
  -H "Content-Type: application/json" \
  -d '{ "addons": [ { "name": "keda", "from_marketplace": true } ] }' \
  | jq '{added, resolved_versions, pr: .git.pr_url}'

# 3. Enable keda on my-cluster (opens a second PR; may auto-merge)
curl -s "${auth[@]}" -X POST "$SH/v4/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq '{pr: .git.pr_url}'

# 4. Watch ArgoCD create + sync the Application
#    (re-run until SYNC STATUS = Synced and HEALTH STATUS = Healthy)
kubectl --context kind-sharko-e2e get application keda-my-cluster -n argocd \
  -o custom-columns='NAME:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status'

# 5. Read the status back from Sharko and confirm it matches ArgoCD
curl -s "${auth[@]}" "$SH/clusters/my-cluster" | jq '.addons // .'
```

Steps 2 and 3 can be combined into one pull request — send
`"enable_on_cluster": "my-cluster", "yes": true` on the step-2 call instead
of making a separate step-3 call (see [Add and enable in one pull
request](#add-an-addon-to-the-catalog) above).

When step 4 shows `Synced` / `Healthy` and step 5 shows `keda` enabled and
healthy for `my-cluster`, the loop is closed: you fired the call, ArgoCD
reacted, and Sharko's view agrees.
