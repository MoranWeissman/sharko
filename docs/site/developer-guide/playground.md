# Local Playground

> **Verified:** This page was updated by reading the current source
> (`cmd/playground/realdoors.go`, `Makefile`, `scripts/playground-*.sh`)
> after the playground gained a fourth real-API step — adding the addon to
> the catalog before enabling it — to match the "catalog = the approved
> list" rebuild (v4 Wave 2.5, design doc
> `.bmad/output/architecture/2026-07-31-catalog-approved-model.md`). It has
> NOT been re-walked end-to-end live in this pass — kind/docker/helm are
> not available in the authoring sandbox. Treat the commands below as
> accurate against the code as of this commit; a live re-walk (and a real
> measured timing) is still owed before the next substantive edit.

This guide covers the **local playground** — a one-command kind topology that
provisions a hub cluster running ArgoCD + Sharko, plus N spoke clusters
(default 2), connected via a real Git backend (Gitea by default, GitFake
optional). It proves the full Sharko GitOps loop end-to-end on your laptop:
register clusters, enable an addon, watch ArgoCD deploy it — no EKS, no cloud
secrets backend required.

**What this proves:** `make playground-up` only provisions infrastructure
(kind clusters, ArgoCD, Gitea, Sharko, and an empty Git repo). Every piece of
Sharko state after that — the v4 seed-bootstrap, cluster registration, addon
assignment — is created by calling Sharko's own REST API and merging the PRs
it opens, the same way a real operator would. Nothing is written directly
into Git by the playground process itself. That means the playground also
proves: the seed-bootstrap PR flow (`POST /api/v1/init`), the cluster
reconciler picking up a merged registration and creating the ArgoCD cluster
Secret, and the ApplicationSet picking up a merged v4 addon assignment.

**What it honestly can't prove:** fetching real credential *values* from a
cloud secrets backend (AWS Secrets Manager / Vault) — that code path needs a
real cloud backend and isn't exercisable locally. This is a laptop proof of
the registration + reconcile + sync loop, not a full production credentials
path.

## Step 1: Spin up the playground

```bash
make playground-up
```

This command (see `cmd/playground/cmd_up.go` + `cmd/playground/realdoors.go`)
splits cleanly into an infrastructure phase and a real-API-doors phase:

**Infrastructure (no Sharko state, no Git writes):**

1. Creates or reuses a persistent hub kind cluster (`sharko-play-hub`) and N
   spoke clusters (`sharko-play-spoke-1..N`, default N=2).
2. Installs ArgoCD on the hub.
3. Builds and loads the Sharko + GitFake images onto the hub.
4. Deploys the Git backend — Gitea by default (real Git server, in-cluster,
   with an empty repo — just what Gitea's own "create repository" gives you),
   or GitFake when `PLAYGROUND_GIT_BACKEND=gitfake` is set.
5. Installs Sharko on the hub via Helm.

**Real doors (Gitea backend only — see below for GitFake):**

6. Creates a `gitea`-typed Sharko connection via the REST API and sets it
   active.
7. **Seed-bootstrap.** Calls `POST /api/v1/init`. Sharko opens a real PR
   with the v4 seed files (an empty `catalog.yaml` is not part of the seed
   — the file does not exist yet, because a fresh repo starts with zero
   approved addons); the playground merges that PR through Gitea's own REST
   API — the same action a human takes clicking "Merge" in the Gitea UI —
   then polls until Sharko has bootstrapped the ArgoCD root Application and
   confirmed it's synced.
8. **Cluster registration.** Registers each spoke via
   `POST /api/v1/clusters`. Sharko opens a real PR per spoke; the playground
   merges it, then gives the cluster reconciler
   (`internal/clusterreconciler`) a window to pick up the merged
   `managed-clusters.yaml` entry and create the ArgoCD cluster Secret
   (`app.kubernetes.io/managed-by: sharko`).
9. **Add to catalog.** Adds one addon (`metrics-server`) to the org's
   approved list via `POST /api/v1/catalog/addons`. Sharko opens a real PR
   that adds a full entry to `catalog.yaml`; the playground merges it. This
   step exists because the enable gate is real: an addon that was never
   added to the catalog cannot be enabled on any cluster, so the playground
   proves that gate the same way a real org would clear it — approve first,
   enable second.
10. **Addon enable.** Enables the addon on the first spoke via
    `POST /api/v1/v4/clusters/{name}/addons/{addon}`. Sharko opens a real
    PR; the playground merges it and the ApplicationSet picks it up on its
    own sync loop.
11. Prints access instructions and runs a status snapshot.

**GitFake backend note:** `PLAYGROUND_GIT_BACKEND=gitfake` keeps the older
direct-seed path (a `managed-clusters.yaml` baked into the GitFake Pod at
startup) instead of the real-doors flow above, because GitFake
(`tests/e2e/harness/gitfake`) only implements the Git smart-HTTP protocol —
it has no PR/merge REST API for the playground to drive the way it drives
Gitea. **Gitea is the supported local playground flow.** GitFake mode does
not exercise the seed-bootstrap PR, the catalog approval gate, or any
PR-merge/reconcile path — it is a leftover from before the real-doors flow
existed, kept only because it is still cheap to run, not as a second
maintained way to demo the playground. If you are not intentionally testing
against GitFake, don't set `PLAYGROUND_GIT_BACKEND=gitfake`.

**Override spoke count:** `PLAYGROUND_SPOKES=3 make playground-up` to add
more spokes.

**Idempotent:** re-running on an existing playground upgrades in place.
Merging an already-merged PR, and registering an already-registered cluster,
are both safe no-ops on the Sharko/Gitea side.

**Old name still works:** `make operator-playground-up` is a quiet alias for
`make playground-up` — muscle memory from before the operator removal keeps
working.

## Step 2: Check the current state

```bash
make playground-status
```

This runs `scripts/playground-status.sh`, which prints:

- Each spoke's ArgoCD cluster Secret addon labels (addon-key labels only —
  the labels with no `/` or `:` in the key).
- Gitea state (deployment readiness + service reachability).
- A one-line summary of how many spokes have addon labels present.

## Step 3: Open browser tunnels

```bash
make playground-tunnels
```

This runs `scripts/playground-tunnels.sh`, opening three `kubectl
port-forward` tunnels at once:

- Sharko — `http://localhost:8080` (login: `admin` / `admin`)
- ArgoCD — `https://localhost:18443` (login: `admin` / the generated admin
  password, printed at tunnel-open time; accept the self-signed cert)
- Gitea — `http://localhost:13000` (login: `sharko` / `sharko-play`)

Press Ctrl+C to close all three tunnels cleanly.

## Step 4: Tear down the playground

```bash
make playground-down
```

This deletes ONLY `sharko-play-*` kind clusters (hub + all spokes). It
guards by exact name-prefix match so it can NEVER touch `sharko-e2e-*` or any
other cluster. Safe to run with nothing present (no-op clean exit).

---

## Advanced: Inspect the Kubernetes resources directly

```bash
# Switch to the hub context
kubectl config use-context kind-sharko-play-hub

# Get the ArgoCD cluster Secret labels for a spoke
kubectl -n argocd get secret -l argocd.argoproj.io/secret-type=cluster --show-labels

# Check the cluster reconciler logs
kubectl logs -n sharko -l app.kubernetes.io/name=sharko -f | grep "cluster reconciler"

# Watch ArgoCD Applications sync
kubectl -n argocd get applications -w
```

The cluster reconciler (`internal/clusterreconciler`) is the single writer
of addon labels on ArgoCD cluster Secrets. On this playground's v4 repo it
converges cluster identity from `managed-clusters.yaml` and addon on/off
state from `cluster-addons/<name>.yaml` (both written via the real-doors flow
above, never by the playground directly), emitting labels of the form
`addons.sharko.dev/<addon>: enabled`. ArgoCD's ApplicationSet reads those
labels and deploys addons — Sharko stays a **guest on ArgoCD**: Sharko writes
labels, ArgoCD owns ApplicationSets and deployment.

## Rebuild Loop (after code changes)

```bash
make playground-down && make playground-up
```

Or use the faster `scripts/dev-rebuild.sh` flow (see
`docs/site/developer-guide/contributor-smoke-walk.md`) if you're iterating
against a persistent cluster rather than the throwaway playground.

## Further Reading

- [ArgoCD ApplicationSet](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/) — the ArgoCD resource that reads cluster Secret labels and deploys addons
