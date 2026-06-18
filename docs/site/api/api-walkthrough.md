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
  "version": "2.0.2",
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
| `inline-kubeconfig` | You paste a kubeconfig right in the request. Bearer-token auth only. | `kubeconfig` (the YAML) |
| `secret-kubeconfig` | You point at a kubeconfig already stored in your secret backend. Works for **any** cluster, including local / on-prem. | `secret_path` |
| `eks-token` | Sharko mints a short-lived token from your EKS cloud identity. Amazon EKS only. | `region` |

Required field in every case: `name` (alphanumeric, may contain hyphens, must
start with a letter or digit). The `addons` field is a map of addon name to
on/off.

#### Paste a kubeconfig (`inline-kubeconfig`)

You hand Sharko the kubeconfig YAML directly in the request. The kubeconfig must
use bearer-token auth.

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

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "my-cluster",
        "creds_source": "eks-token",
        "region": "us-east-1",
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
    Add `"dry_run": true` to see exactly which files would be written and which
    secrets would be created, with no side effects. The preview comes back under
    a `dry_run` key.

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

### Diagnose

Runs a deeper set of permission/namespace checks against the cluster and returns
a report with suggested fixes. No request body needed.

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
cluster. Each of these opens a PR (and may auto-merge per your global setting).
Once the PR is merged, ArgoCD creates or removes an Application named
`<addon>-<cluster>` and syncs it.

### Enable an addon

Turns one addon on for one cluster. Sharko flips the addon to `true` in the
cluster's values file and to "enabled" in `managed-clusters.yaml`, via a PR. If
the cluster has a credential provider, the addon's secrets are also created on
the remote cluster. **Confirmation is required — set `"yes": true`.**

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq
```

A good answer is HTTP 200 with `"status": "success"` and a `git` block with the
`pr_url`. Use `"dry_run": true` to preview without writing anything.

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

Turns one addon off for one cluster, with a configurable cleanup scope.
**Confirmation is required — set `"yes": true`.**

- `"cleanup": "all"` (default) — update values + labels and delete remote
  secrets.
- `"cleanup": "labels"` — update values + labels only.
- `"cleanup": "none"` — update values only.

```bash
curl -s "${auth[@]}" -X DELETE "$SH/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true, "cleanup": "all" }' | jq
```

After the PR merges, the `keda-my-cluster` Application is removed from ArgoCD.
Confirm it's gone:

```bash
kubectl --context kind-sharko-e2e get applications -n argocd | grep keda-my-cluster || echo "removed"
```

Add `"dry_run": true` to preview.

### Toggle several addons at once

Updates the on/off state of multiple addons for a cluster in a single PR. The
body's `addons` field is a map of addon name to the desired on/off value.

```bash
curl -s "${auth[@]}" -X PATCH "$SH/clusters/my-cluster" \
  -H "Content-Type: application/json" \
  -d '{ "addons": { "keda": true, "metrics-server": false } }' | jq
```

You can also use this same call to change the cluster's `secret_path` by adding a
`"secret_path": "..."` field. A request that names an addon not in your catalog
comes back as HTTP 422.

### Restart a stuck sync

If an addon's ArgoCD sync is wedged (stale or permanently failing), this
terminates any in-flight sync operation and immediately re-triggers a fresh one —
without you having to open the ArgoCD UI. No request body.

```bash
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/addons/keda/restart-sync" | jq
```

A good answer: `{"terminated": true|false, "synced": true}`. (`terminated` is
`true` only when there really was an operation in flight to cancel.) If the
Application doesn't exist in ArgoCD, you get a 404.

---

## 4. Catalog

The catalog is the set of addons Sharko knows how to deploy. These calls let you
browse it and change what's in it.

### List catalog addons

The curated, embedded marketplace list (read-only).

```bash
curl -s "${auth[@]}" "$SH/catalog/addons" | jq
```

### List catalog sources

The configured catalog sources (the embedded one plus any third-party URLs) with
per-source fetch status.

```bash
curl -s "${auth[@]}" "$SH/catalog/sources" | jq
```

### Add an addon to the catalog

Adds a new addon by creating its configuration in your GitOps repo via a PR.
**Required fields: `name`, `chart`, `repo_url`, `version`.** Common optional
fields are `namespace` and `sync_wave`.

```bash
curl -s "${auth[@]}" -X POST "$SH/addons" \
  -H "Content-Type: application/json" \
  -d '{
        "name": "keda",
        "chart": "keda",
        "repo_url": "https://kedacore.github.io/charts",
        "version": "2.14.0",
        "namespace": "keda"
      }' | jq
```

A good answer is HTTP 201. An addon that already exists comes back as 409. There
are several more optional fields (sync options, extra Helm values, dependencies,
and a `dry_run` preview flag) — see `docs/swagger/swagger.json` for the full
`AddAddonRequest` body.

### Configure an addon

Updates an existing catalog addon's settings (for example its pinned `version`,
`sync_wave`, or `self_heal`), opening a PR. All fields except the path name are
optional — send only what you want to change.

```bash
curl -s "${auth[@]}" -X PATCH "$SH/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "sync_wave": 2 }' | jq
```

An unknown addon name comes back as 404. See swagger for the full
`ConfigureAddonRequest` field list.

### Upgrade an addon

Bumps an addon to a new chart version. Send `version` for a global upgrade, or
add `cluster` to upgrade just one cluster's pin. **`version` is required.**

```bash
# Global upgrade
curl -s "${auth[@]}" -X POST "$SH/addons/keda/upgrade" \
  -H "Content-Type: application/json" \
  -d '{ "version": "2.15.0" }' | jq

# Per-cluster upgrade
curl -s "${auth[@]}" -X POST "$SH/addons/keda/upgrade" \
  -H "Content-Type: application/json" \
  -d '{ "version": "2.15.0", "cluster": "my-cluster" }' | jq
```

### Remove an addon from the catalog

This is destructive, so it has a two-step shape. **Without** `?confirm=true` it
returns a dry-run impact report telling you what would be affected. **With**
`?confirm=true` it actually removes the addon via a PR.

```bash
# Step 1 — see the impact (safe, no changes)
curl -s "${auth[@]}" -X DELETE "$SH/addons/keda" | jq

# Step 2 — actually remove it
curl -s "${auth[@]}" -X DELETE "$SH/addons/keda?confirm=true" | jq
```

An unknown addon name comes back as 404.

---

## 5. Values

Values are the Helm values YAML that configure an addon. There are two layers:
the **global** defaults for an addon, and **per-cluster** overrides for one addon
on one cluster. Both write through a PR.

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
values edit lands here. Useful for confirming an action was recorded.

```bash
curl -s "${auth[@]}" "$SH/audit" | jq
```

---

## A full cycle in one block

This ties it all together: log in, enable an addon on a cluster, watch the
ArgoCD Application go healthy, then read the status back from Sharko and confirm
the two agree.

```bash
# 0. Shortcuts + token
eval "$(./scripts/sharko-dev.sh login --export)"
SH="http://localhost:8080/api/v1"
auth=(-H "Authorization: Bearer $TOKEN")

# 1. Confirm we're talking to Sharko
curl -s "$SH/health" | jq -r '.status'        # -> healthy

# 2. Enable keda on my-cluster (opens a PR; may auto-merge)
curl -s "${auth[@]}" -X POST "$SH/clusters/my-cluster/addons/keda" \
  -H "Content-Type: application/json" \
  -d '{ "yes": true }' | jq '{status, pr: .git.pr_url}'

# 3. Watch ArgoCD create + sync the Application
#    (re-run until SYNC STATUS = Synced and HEALTH STATUS = Healthy)
kubectl --context kind-sharko-e2e get application keda-my-cluster -n argocd \
  -o custom-columns='NAME:.metadata.name,SYNC:.status.sync.status,HEALTH:.status.health.status'

# 4. Read the status back from Sharko and confirm it matches ArgoCD
curl -s "${auth[@]}" "$SH/clusters/my-cluster" | jq '.addons // .'
```

When step 3 shows `Synced` / `Healthy` and step 4 shows `keda` enabled and
healthy for `my-cluster`, the loop is closed: you fired the call, ArgoCD
reacted, and Sharko's view agrees.
