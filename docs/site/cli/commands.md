# CLI Commands

Full reference for all `sharko` commands.

## Authentication

### `sharko login`

Authenticate with a Sharko server. Stores the session token in `~/.sharko/config.yaml`.

```bash
sharko login --server https://sharko.your-domain.com

# Non-interactive (CI/CD):
sharko login --server https://sharko.your-domain.com --username admin --password mypassword
```

| Flag | Description |
|------|-------------|
| `--server <url>` | Sharko server URL (required) |
| `--username <user>` | Username (prompted if not provided) |
| `--password <pass>` | Password (prompted if not provided) |

### `sharko version`

Show the CLI version and the server version.

```bash
sharko version
# CLI: v2.0.1
# Server: v2.0.1
```

---

## Initialization

### `sharko init`

Initialize the addons repository. Creates the initial folder structure and bootstrap Application in your Git repo via a PR.

```bash
sharko init
```

Run once per repository. Requires an active Git connection in Settings.

Init is asynchronous — the CLI prints an operation ID and streams log lines until the operation completes:

```
Initializing repository...
Operation ID: op_a1b2c3d4
[10:01:05] Creating branch sharko/init-2026-04-06...
[10:01:06] Committing scaffold files (12 files)...
[10:01:08] Creating pull request...
[10:01:09] Auto-merging PR #42...
[10:01:12] Waiting for ArgoCD sync...
[10:01:30] Done. Root application is Healthy.
```

With auto-merge disabled (`SHARKO_CONN_GITOPS_PR_AUTO_MERGE=false`, or `connection.gitops.prAutoMerge: "false"` in Helm values), the init completes after the PR is created. Merge the PR manually, then re-run `sharko init` to continue ArgoCD bootstrap. The server recognizes the existing operation and resumes it automatically.

### `sharko validate`

Validate a catalog YAML file against the Sharko schema without pushing any changes.

```bash
sharko validate                   # validates addons-catalog.yaml in current directory
sharko validate path/to/catalog.yaml
```

Exits 0 on valid, 1 on schema errors (printed to stderr).

> **Note:** `sharko validate` is the legacy field-presence validator over the pre-envelope YAML
> shape. New work should use [`sharko validate-config`](#sharko-validate-config) below, which
> validates against the committed JSON Schemas at `https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/`. The legacy
> command is retained for back-compat through V125 and will be removed in V126.

### `sharko validate-config`

Validate Sharko configuration YAML against the committed JSON Schema. Auto-detects the schema
from the file's top-level `apiVersion: sharko.dev/v1` + `kind:` fields, validates against the
embedded `managed-clusters.v1.json` or `addons-catalog.v1.json`, and prints a human-readable
violation list with a direct link to the schema URL on failure.

This is the CLI front end for the same read-time validator that `sharko serve` runs against
every config it loads — running `validate-config` locally is equivalent to seeing what the
server would say at startup.

```bash
# Validate a single file
sharko validate-config managed-clusters.yaml

# Validate every YAML in a directory (recursive)
sharko validate-config templates/bootstrap/configuration/

# Quiet mode — only show failures + summary, no per-file pass lines
sharko validate-config --quiet .
```

| Flag | Description |
|------|-------------|
| `-q`, `--quiet` | Suppress per-file pass lines; only show failures + summary. |

**File selection in directory mode.** The walker descends recursively and picks up every
`*.yaml` / `*.yml` file. For each file it peeks at the top-level `apiVersion`:

- `apiVersion: sharko.dev/v1` → validated against the matching schema (`kind` decides which one).
- anything else (or missing) → skipped with a one-line `skip: <path> (not a Sharko-enveloped file)`.

Hidden directories (anything starting with `.`, e.g. `.git`, `.github`) are not descended
into, so `sharko validate-config .` in the repo root stays fast.

**Exit codes:**

- `0` — every Sharko-enveloped file validated; non-Sharko files were skipped.
- `1` — at least one Sharko-enveloped file failed schema validation.

**Example output (success)**, from `sharko validate-config .` at the root of a GitOps repo:

```
✓ cluster-addons/my-cluster.yaml
✓ managed-clusters.yaml
skip: sharko-engine.yaml (not a Sharko-enveloped file)
```

**Example output (failure):**

```
✘ managed-clusters.yaml: schema violations (kind: ManagedClusters)
   ✘ /clusters/0: missing properties: 'name' (line 4)
   → for details: https://raw.githubusercontent.com/MoranWeissman/sharko/main/docs/schemas/managed-clusters.v1.json

1 file(s) failed validation
```

**CI use.** A GitHub Actions job named *Sharko Config Validation* runs `validate-config` on
every changed YAML file in every PR (see `.github/workflows/ci.yml`). The job is permissive
by design: it only blocks PRs that change Sharko-enveloped YAML in a schema-invalid way.
PRs that touch only non-Sharko YAML (workflows, Helm templates, kind configs, etc.) pass
through untouched.

---

## Cluster Commands

### `sharko add-cluster`

Register a cluster with Sharko.

```bash
sharko add-cluster <name> [flags]
```

| Flag | Description |
|------|-------------|
| `--addons <list>` | Comma-separated list of addons to enable |
| `--region <region>` | Cloud region of the cluster. Used with the `eks-token` and `secret-kubeconfig` sources, not with `inline-kubeconfig` |
| `--creds-source <source>` | How Sharko gets the cluster's credentials: `eks-token`, `secret-kubeconfig` or `inline-kubeconfig`. These are the same choices as the register dialog in the UI. Leave it out and Sharko works it out from the other flags |
| `--secret-path <name>` | Name or path of the secret that holds the credentials. Used with `secret-kubeconfig` (the stored kubeconfig) and, optionally, `eks-token` |
| `--role-arn <arn>` | AWS role Sharko takes on to reach this cluster. With `eks-token` it is the role used to make the token. With `secret-kubeconfig` it is the role used to read the stored kubeconfig. Not allowed with `inline-kubeconfig` |
| `--kubeconfig-file <path>` | A kubeconfig file to hand to Sharko once. `inline-kubeconfig` only. This source is off by default. An admin has to turn on "Allow legacy inline credentials" in Settings first |
| `--connection-managed-by <who>` | Who looks after the ArgoCD cluster secret: `sharko` (the default) or `user`. With `user` you make the secret yourself and Sharko never writes it |
| `--dry-run` | Preview the registration without making changes — prints the PR title, effective addons, secrets that would be created, the connectivity check result, and per-file the exact diff the pull request would contain |

Sharko checks the mix of flags before it sends anything. For example,
`--kubeconfig-file` with `--creds-source eks-token` is refused, and so is
`--region` with `--creds-source inline-kubeconfig`.

One example for each source:

```bash
# Sharko makes a short-lived AWS token each time it connects (EKS only)
sharko add-cluster prod-eu --creds-source eks-token \
  --region eu-west-1 --role-arn arn:aws:iam::123456789012:role/sharko-access

# Sharko reads a kubeconfig kept in your secrets backend
sharko add-cluster prod-eu --creds-source secret-kubeconfig \
  --secret-path clusters/prod-eu

# You hand Sharko a kubeconfig file once
sharko add-cluster prod-eu --creds-source inline-kubeconfig \
  --kubeconfig-file ./prod-eu.kubeconfig

# You create the ArgoCD cluster secret yourself
sharko add-cluster prod-eu --creds-source eks-token --connection-managed-by user
```

With addons:

```bash
sharko add-cluster prod-eu \
  --addons cert-manager,monitoring,logging \
  --region eu-west-1
```

### `sharko add-clusters`

Batch register multiple clusters in a single API call (up to 10).

```bash
sharko add-clusters cluster-a,cluster-b,cluster-c \
  --addons cert-manager,metrics-server
```

To register every cluster your cloud account has that Sharko does not know
yet, use `--from-provider`. It needs no list of names. Sharko shows what it
found and asks before it registers them.

```bash
sharko add-clusters --from-provider
```

Sharko always asks before it registers. If there is no terminal (for example in
a script), it cannot ask, so it stops with an error and changes nothing. Add
`--yes` to go ahead without asking, or `--dry-run` to only check.

A batch can take a minute or two. If the command stops waiting for the server,
some clusters may be registered and some may not. Do not run it again straight
away. Run `sharko pr list` or open the Clusters page to see which ones went in.

### `sharko remove-cluster`

Deregister a cluster through a pull request. Requires `-y/--yes` — the server
refuses a removal without an explicit confirmation. See what the removal will
do first with `sharko unregister-consequences <name>`.

Nothing changes on the cluster until the pull request is merged. When the
pull request is still open, the command says `PR opened, waiting for merge:`
and gives the link. It prints `Cluster <name> removed.` only when the removal
really happened.

**Exit code:** the command exits `0` when the pull request is left waiting.
Waiting is not an error, and the command did what it can do now. This means
`sharko remove-cluster x -y && next-step` runs `next-step` while the cluster
is still there. If your script needs the cluster gone, wait for the merge
first with `sharko pr wait <PR number>`. The command prints the pull request link, not its number. The number is the last part of the link: for a link that ends in `/pull/12`, run `sharko pr wait 12`. If the pull request already merged, `sharko pr wait` says so at once and exits 0. The exit code is not `0`
only when something went wrong. `sharko unadopt-cluster` and `sharko adopt`
work the same way.

```bash
sharko unregister-consequences <name>   # read this first
sharko remove-cluster <name> -y
sharko remove-cluster <name> -y --cleanup git   # keep the ArgoCD connection
```

`--cleanup` takes `all` (default — repo files, remote addon secrets, and the
ArgoCD connection only if Sharko owns it; a connection owned by another tool
or by you is always left alone), `git` (repo files only), or `none` (only the
fleet-record entry).

When the removal keeps the ArgoCD connection, the connection gets a
`sharko.dev/remove-keep-pending-pr` mark while the pull request waits, so
nothing deletes it in the meantime. See
[A removal that keeps the connection](../operator/cluster-reconciler.md#a-removal-that-keeps-the-connection).

### `sharko update-cluster`

Update the addon assignments for a cluster.

```bash
sharko update-cluster <name> --add-addon cert-manager,metrics-server --remove-addon logging
```

| Flag | Description |
|------|-------------|
| `--add-addon <list>` | Comma-separated addons to enable |
| `--remove-addon <list>` | Comma-separated addons to disable |

### `sharko test-cluster`

Test connectivity to a cluster. Verifies that Sharko can reach the cluster's Kubernetes API, using the connection Sharko holds for it. For a cluster whose connection ArgoCD holds, that is the one stored in ArgoCD. It makes a small test secret on the cluster, reads it back and deletes it.

```bash
sharko test-cluster <name>
```

Example output:

```
Cluster: prod-eu
Reachable: yes
Kubernetes version: v1.29.3
```

If the cluster is unreachable, the error message from the provider or the Kubernetes API is shown.

### `sharko adopt`

Adopt one or more existing ArgoCD clusters into Sharko management. Opens a pull request that adds each cluster to `managed-clusters.yaml`. The clusters are already in ArgoCD but not yet managed by Sharko. Nothing is adopted until the pull request is merged. The command says `PR opened, waiting for merge:` with the link, and exits `0` while it waits. When the pull request is merged, Sharko takes over the ArgoCD connection and marks it adopted. If it is closed without a merge, nothing changes on the cluster.

```bash
sharko adopt <cluster1> [cluster2] ... [flags]
```

| Flag | Description |
|------|-------------|
| `-y`, `--yes` | Skip the confirmation prompt |
| `--dry-run` | Preview what would happen without making changes. Says `would adopt` for each cluster that is ready, and prints per-file the exact diff the pull request would contain |
| `--acknowledge <id,id,...>` | Ids of the warnings you have read. `--yes` does not skip warnings. `--dry-run` shows them |
| `--auto-merge` | Auto-merge the adoption PR (overrides the server default) |

### `sharko unadopt-cluster`

Reverse adoption of a cluster. Removes Sharko management (GitOps config, managed-by labels) but keeps the ArgoCD cluster secret intact. Nothing changes until the pull request is merged. While it is open the command says `PR opened, waiting for merge:` and exits `0`. It prints that the cluster is un-adopted only when that is really done. The cluster must carry the `sharko.dev/adopted` annotation, which Sharko puts on a cluster when you adopt it.

```bash
sharko unadopt-cluster <name>
```

| Flag | Description |
|------|-------------|
| `-y`, `--yes` | Skip the confirmation prompt |
| `--dry-run` | Preview what would happen without making changes — prints per-file the exact diff the pull request would contain |

### `sharko list-clusters`

List all registered clusters.

```bash
sharko list-clusters
```

### `sharko status`

Show cluster status overview: sync status, addon counts, health.

```bash
sharko status
```

---

## Addon Commands

### `sharko add-addon`

Add an addon to the catalog.

```bash
sharko add-addon <name> [flags]
```

| Flag | Required | Description |
|------|----------|-------------|
| `--chart <name>` | Yes | Helm chart name |
| `--repo <url>` | Yes | Helm repository URL |
| `--version <ver>` | Yes | Chart version |
| `--namespace <ns>` | No | Target namespace (defaults to addon name) |

Example:

```bash
sharko add-addon ingress-nginx \
  --chart ingress-nginx \
  --repo https://kubernetes.github.io/ingress-nginx \
  --version 4.9.0 \
  --namespace ingress-nginx
```

### `sharko remove-addon`

Remove an addon from the catalog.

```bash
sharko remove-addon <name> [--confirm]
```

Without `--confirm`, runs a dry-run and shows what the removal would change. With `--confirm`, opens the removal PR.

On a repo in the new format, Sharko will not remove an addon that is still switched on for a cluster. The command names those clusters. Switch it off on each one first with `sharko disable-addon <cluster> <name>`, then run `remove-addon` again.

### `sharko configure-addon`

Update one or more configuration fields for an existing addon. Only the flags you pass are sent to the server.

```bash
sharko configure-addon <name> [flags]
```

| Flag | Description |
|------|-------------|
| `--version <ver>` | Chart version |
| `--self-heal <true\|false>` | Self-heal setting |
| `--sync-option <opt>` | ArgoCD sync option (repeatable) |
| `--ignore-differences <json>` | ignoreDifferences entries as a JSON array |
| `--extra-helm-value <k=v>` | Extra Helm value (repeatable) |

Example:

```bash
sharko configure-addon kyverno --sync-option ServerSideApply=true
sharko configure-addon prometheus --self-heal=false
```

### `sharko describe-addon`

Show the full details of an addon, including its catalog defaults.

```bash
sharko describe-addon <name>
```

### `sharko upgrade-addon`

Upgrade an addon version, globally or per-cluster.

```bash
sharko upgrade-addon <name> --version <ver> [--cluster <name>]
```

| Flag | Description |
|------|-------------|
| `--version <ver>` | Target version (required) |
| `--cluster <name>` | Upgrade only this cluster (omit for global) |

Examples:

```bash
# Global upgrade
sharko upgrade-addon cert-manager --version 1.15.0

# Per-cluster upgrade
sharko upgrade-addon cert-manager --version 1.15.0 --cluster staging
```

**Repo format awareness.** Before doing anything, this command checks whether the connected repo
is on the v3 or v4 data-file format (`GET /api/v1/migration/status`):

- **v3 repo** (or the format check itself couldn't be answered) — works exactly as shown above,
  no change in behavior.
- **v4 repo, with `--cluster`** — v4 pins addon versions per cluster rather than in a global
  catalog entry, so this command transparently routes the request through
  [`sharko upgrade-clusters`](#sharko-upgrade-clusters) instead of the old (v3-only) upgrade route,
  and says so on stdout.
- **v4 repo, without `--cluster`** — there is no "global version" concept on a v4 repo, so the
  command refuses with a plain-English explanation and prints the exact `sharko upgrade-clusters`
  command to run once you've picked the clusters.
- **Mixed repo** (both layouts present) — the server's own migration-status message is shown
  as-is; finish the migration first.

### `sharko upgrade-addons`

Batch upgrade multiple addons in a single PR.

```bash
sharko upgrade-addons cert-manager=1.15.0,metrics-server=0.7.1
```

Same repo-format check as `upgrade-addon`. On a v4 repo there is no batch route — the command
refuses and prints one `sharko upgrade-clusters` command per addon in the batch instead. On a
mixed repo, the server's migration-status message is shown.

### `sharko upgrade-clusters`

Bump one addon's version pin on a chosen subset of clusters, in a single pull request (v4 format
only). This is the fleet-upgrade route the UI uses. Clusters left out of `--cluster` are
untouched — the diff is one small block per selected cluster. Every selected cluster must
already have the addon enabled; this never enables an addon as a side effect of upgrading it.

```bash
sharko upgrade-clusters <addon> --version <ver> --cluster <name> [--cluster <name> ...] [flags]
```

| Flag | Description |
|------|-------------|
| `--version <ver>` | Target version (required) |
| `--cluster <name>` | Cluster to upgrade (repeatable; at least one required) |
| `--dry-run` | Preview every file the PR would change, including the diff, without making changes |
| `-y`, `--yes` | Skip confirmation prompt |
| `--auto-merge` | Auto-merge the PR (overrides the server default; only sent when you pass this flag) |

Example:

```bash
sharko upgrade-clusters cert-manager --version 1.16.0 \
  --cluster prod-eu --cluster staging-us -y
```

### `sharko list-addons`

List all addons in the catalog. Use `--show-config` to include the full catalog configuration for each addon (secrets declarations, values, etc.).

```bash
sharko list-addons
sharko list-addons --show-config
```

---

## v4 / Takeover Commands

Sharko's v4 data-file format replaces the old global catalog with per-cluster addon files
(`cluster-addons/<cluster>.yaml`) and lets you take over a cluster ArgoCD already manages instead
of only registering brand-new ones. These commands only work against a v4-format repo.

### `sharko takeover-preflight`

Read-only. Runs the checks Sharko does before a takeover: who owns the cluster's ArgoCD
connection today, which ApplicationSets would react to it changing owners, what is deployed
there, and whether the name clashes with a cluster Sharko already has. Writes nothing — safe to
run as often as you like.

```bash
sharko takeover-preflight <cluster>
```

Each finding prints with a status glyph (✓ ok, ⚠ warning, ✗ blocked) plus what it means and what
to do about it.

### `sharko takeover`

Makes Sharko the owner of an existing cluster's ArgoCD connection, keeping the same name, the
same API address and — by default — every label the previous owner left on it. Adds the cluster
to Sharko's fleet through a pull request and creates an empty addon file for it; no addon is
turned on.

```bash
sharko takeover <cluster> [flags]
```

| Flag | Description |
|------|-------------|
| `--dry-run` | Preview the plan (files, diffs) without making changes |
| `-y`, `--yes` | Skip confirmation prompt |
| `--acknowledge <id>` | Finding id to acknowledge. Repeat the flag, or give several ids split by commas (`--acknowledge a,b`) |
| `--preserve-legacy-labels` | Carry the previous owner's labels over (only sent when you set this flag) |
| `--region <region>` | Region to record on the fleet entry |
| `--auto-merge` | Auto-merge the takeover PR (overrides the server default; only sent when you set this flag) |

Always fetches and prints the preflight report first. Every **warning** finding has to be named
in `--acknowledge` before the takeover is allowed to write anything — a **blocked** finding has
to be fixed instead, and re-running the command is how you check it turned green.

If a warning wasn't acknowledged, the server refuses with a 409 naming which finding ids are
still missing, and the CLI prints the exact rerun command to copy-paste:

```
$ sharko takeover prod-eu -y
Preflight for prod-eu: ready
  ⚠ [appset-deletion-safety] ApplicationSet reacts on removal
      ...

Rerun with: sharko takeover prod-eu -y --acknowledge appset-deletion-safety
1 warning has not been read yet. Look at appset-deletion-safety in the checks above, then run the command again with --acknowledge appset-deletion-safety
```

The checks print once, and the usage help does not print after an error.

Take over opens the pull request first, the same as adopt. When you run it, Sharko only puts one small mark on the ArgoCD connection, so it can find the pull request later. Nothing about the owner of the connection changes yet. When the pull request is merged, Sharko takes over the connection. If the pull request is closed without a merge, Sharko takes the mark off and changes nothing else. The command says `PR opened, waiting for merge:` with the link and exits `0` while it waits.

If you run it again while that pull request is open, Sharko says the take over is already waiting and gives the link. It does not open a second one.

If something goes wrong after the merge (the ownership swap fails), Sharko says so, nothing is
deleted, and the takeover is safe to run again.

### `sharko drop-legacy-labels`

Removes the labels a takeover carried over from the previous owner. Warns first, by name, about
any ApplicationSet that still picks clusters using one of those labels — removing it is what
makes this cluster fall out of that ApplicationSet.

```bash
sharko drop-legacy-labels <cluster> [flags]
```

| Flag | Description |
|------|-------------|
| `--label <key>` | Label key to remove (repeatable; omit for every carried-over label) |
| `-y`, `--yes` | Skip confirmation prompt |
| `--dry-run` | Preview what would be removed without making changes |
| `--acknowledge <id>` | Warning id to acknowledge. Repeat the flag, or give several ids split by commas (`--acknowledge a,b`) |

Same acknowledge-then-rerun pattern as `takeover`: every warning must be named in
`--acknowledge`, and an unacknowledged warning gets you the exact rerun command back.

### `sharko unregister-consequences`

Read-only. Reads out, one by one, what unregistering this cluster will do — what leaves the
repo, what happens to its ArgoCD connection, what is deployed there today, and which
ApplicationSets may react to labels a takeover carried over. Writes nothing and deletes nothing.

```bash
sharko unregister-consequences <cluster>
```

### `sharko enable-addon`

Enables an addon on a cluster (v4 format). Writes the cluster's addon-assignment entry and,
when values are supplied, the per-cluster values file.

```bash
sharko enable-addon <cluster> <addon> [flags]
```

| Flag | Description |
|------|-------------|
| `--version <ver>` | Pin the chart version for this cluster only (omit to leave any existing pin unchanged) |
| `--clear-version` | Clear the per-cluster version pin and follow the catalog default again |
| `--values-json <json>` | Inline JSON object of values to deep-merge onto the cluster's values file |
| `--dry-run` | Preview what would happen without making changes — prints per-file the exact diff the pull request would contain |
| `-y`, `--yes` | Skip confirmation prompt |
| `--auto-merge` | Auto-merge the PR (overrides the server default; only sent when you set this flag) |

`--version` and `--clear-version` are mutually exclusive. Example:

```bash
sharko enable-addon prod-eu cert-manager \
  --version 1.15.0 --values-json '{"installCRDs": true}' -y
```

A validation failure (missing required values, an addon not yet in the catalog, and similar)
comes back as a 422 naming exactly what's missing — the CLI prints the server's `code` and
`problems` list alongside the message.

### `sharko disable-addon`

Disables an addon on a cluster (v4 format) by setting `enabled: false` on its entry — the entry
(and its version pin and settings) is kept by default, so re-enabling later is a one-word change.

```bash
sharko disable-addon <cluster> <addon> [flags]
```

| Flag | Description |
|------|-------------|
| `--remove` | Delete the addon's entry entirely instead of keeping it disabled |
| `--dry-run` | Preview what would happen without making changes — prints per-file the exact diff the pull request would contain |
| `-y`, `--yes` | Skip confirmation prompt |
| `--auto-merge` | Auto-merge the PR (overrides the server default; only sent when you set this flag) |

---

## Catalog Commands

### `sharko add-to-catalog`

Add an addon to your org's catalog (`catalog.yaml`) and open a pull request. This is the approval step — nothing runs until the addon is in that file.

```bash
sharko add-to-catalog <name> [flags]
```

| Flag | Description |
|------|-------------|
| `--chart <name>` | Chart name in the repo (required unless `--from-marketplace`) |
| `--repo <url>` | Chart repository URL, `https://` or `oci://` (required unless `--from-marketplace`) |
| `--version <ver>` | Chart version (always required) |
| `--namespace <ns>` | Namespace to install into |
| `--from-marketplace` | Copy the chart location, namespace, and needed-secrets list from the Marketplace entry of the same name |
| `--enable-on <cluster>` | Also switch the addon on for this cluster, in the same pull request |

Example:

```bash
sharko add-to-catalog cert-manager --from-marketplace --version 1.14.5
```

### `sharko validate-catalog`

Validate a curated catalog file (`catalog/addons.yaml` format) against the same rules the running server enforces when it embeds the catalog at build time.

```bash
sharko validate-catalog <file>
```

Exits 0 on valid, 1 on validation errors (printed to stderr).

---

## Connection Commands

### `sharko connect`

Configure the active Git connection. Replaces the current connection.

```bash
sharko connect \
  --name my-git-connection \
  --git-provider github \
  --git-repo https://github.com/your-org/addons-repo \
  --git-token ghp_xxxx
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Connection name (required) |
| `--git-provider <provider>` | `github` or `azuredevops` (required) |
| `--git-repo <url>` | Addons repository URL (required) |
| `--git-token <token>` | PAT or access token (required) |
| `--argocd-url <url>` | ArgoCD server URL |
| `--argocd-token <token>` | ArgoCD API token |
| `--argocd-namespace <ns>` | ArgoCD namespace (default: `argocd`) |

### `sharko connect list`

Show the current active connection.

```bash
sharko connect list
```

### `sharko connect test`

Test the current active connection (validates credentials and repo access).

```bash
sharko connect test
```

### `sharko repair-connection`

Make a cluster's ArgoCD connection match the Git-defined connection. Checks a cluster's ArgoCD connection against what Sharko means it to be, shows you what does not match, and then puts it right.

```bash
sharko repair-connection <cluster>
```

| Flag | Description |
|------|-------------|
| `-y`, `--yes` | Skip the confirmation prompt |
| `--check-only` | Run the read-only check and stop; change nothing |

Use this when a cluster's ArgoCD Secret has drifted from what Sharko wrote to Git (for example, after manually editing the Secret or after a takeover).

---

## Secrets Commands

### `sharko refresh-secrets`

Trigger an immediate secrets reconcile. Useful after rotating a secret in your provider.

```bash
sharko refresh-secrets               # reconcile all clusters
sharko refresh-secrets prod-eu       # reconcile a specific cluster
```

### `sharko secret-status`

Show the current addon-secret sync status per cluster: last run time, hash comparison result, and any errors.

```bash
sharko secret-status
```

---

## API Key Commands

### `sharko token create`

Create a new API key.

```bash
sharko token create --name <name> --role <role> [--expires-in-days <n>]
```

| Flag | Description |
|------|-------------|
| `--name <name>` | Key name for identification (required) |
| `--role <role>` | `admin`, `operator`, or `viewer` |
| `--expires-in-days <n>` | How long the key stays usable, 1–365. Leave it out for the default of 90 days. |

Output includes the plaintext key — shown once only — plus the expiry date.

### `sharko token list`

List all API keys: names, roles, status, creation and expiry dates. Never the
key values.

```bash
sharko token list
```

The `STATUS` column reads `active`, `expired`, or `legacy-no-expiry` (a key
made before Sharko put expiry dates on keys — it keeps working, but has no
expiry).

### `sharko token renew`

Give an existing key a fresh window, counted from now. **The key value does not
change**, so anything already using it keeps working — nothing to redeploy.
Works on an expired key too.

```bash
sharko token renew <name> [--expires-in-days <n>]
```

| Flag | Description |
|------|-------------|
| `--expires-in-days <n>` | New window in days, 1–365. Leave it out for the default of 90 days. |

### `sharko token revoke`

Revoke an API key by name. Takes effect immediately — no grace period, no undo.

```bash
sharko token revoke <name>
```

---

## PR Commands

Manage pull requests Sharko has opened and is tracking.

```bash
sharko pr list                 # list tracked pull requests
sharko pr status <id>          # show details for a tracked PR
sharko pr refresh <id>         # force-refresh a tracked PR's status
sharko pr wait <id>            # wait for a PR to be merged or closed
```

---

## User Commands

Manage Sharko user accounts (admin only).

```bash
sharko user list                          # list all users
sharko user create <username>             # create a new user
sharko user delete <username>             # delete a user
sharko user update <username>             # update a user's role or enabled status
```

### `sharko reset-admin`

Reset the admin user's password by directly updating the Kubernetes Secret. Requires kubectl access to the namespace where Sharko is installed.

```bash
sharko reset-admin
```

| Flag | Description |
|------|-------------|
| `--kubeconfig <path>` | Path to kubeconfig file (defaults to `KUBECONFIG` env or `~/.kube/config`) |
| `--namespace <ns>` | Kubernetes namespace where Sharko is installed |
| `--secret <name>` | Name of the Sharko credentials Secret |

Use this when you have lost access to the admin account and need to recover it. The command generates a new random password and prints it.

---

## Server Commands

### `sharko serve`

Start the Sharko API server. This is the command the Helm chart runs inside the Sharko pod. Operators do not usually run this directly — it is for local development and testing.

```bash
sharko serve
```

| Flag | Description |
|------|-------------|
| `--port <port>` | HTTP server port (default: 8080) |
| `--config <path>` | Path to config file for local mode |
| `--static <path>` | Path to static files directory for the UI |
| `--demo` | Run with mock backends for testing (no external dependencies) |
| `--demo-clusters <n>` | Demo mode: number of clusters to generate |
