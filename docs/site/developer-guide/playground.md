# Local Playground

> **Verified:** This page was rewritten by reading the current source
> (`cmd/playground/*.go`, `Makefile`, `scripts/playground-*.sh`) after the
> ClusterAddons operator was removed from the build (v4 Wave 0, code
> preserved on the `operator-shelf` branch). It has NOT been re-walked
> end-to-end live in this pass — kind/docker/helm are not available in the
> authoring sandbox. Treat the commands below as accurate against the code
> as of this commit; a live re-walk is still owed before the next
> substantive edit.

This guide covers the **local playground** — a one-command kind topology that
provisions a hub cluster running ArgoCD + Sharko, plus N spoke clusters
(default 2), all connected via a real Git backend (Gitea by default, GitFake
optional). It proves the full Sharko GitOps loop end-to-end on your laptop:
register clusters, assign addons, watch ArgoCD deploy them — no EKS, no cloud
secrets backend required.

**What this proves:** cluster registration, addon assignment via
`managed-clusters.yaml`, the cluster reconciler writing ArgoCD cluster Secret
labels, and ArgoCD's ApplicationSet picking them up and deploying addons.

**What it honestly can't prove:** fetching real credential *values* from a
cloud secrets backend (AWS Secrets Manager / Vault) — that code path needs a
real cloud backend and isn't exercisable locally. This is a laptop proof of
the registration + label + sync loop, not a full production credentials path.

## Step 1: Spin up the playground

```bash
make playground-up
```

This command (see `cmd/playground/cmd_up.go`):

1. Creates or reuses a persistent hub kind cluster (`sharko-play-hub`) and N
   spoke clusters (`sharko-play-spoke-1..N`, default N=2).
2. Installs ArgoCD on the hub.
3. Builds and loads the Sharko + GitFake images onto the hub.
4. Deploys the git backend — Gitea by default (real git server, in-cluster),
   or GitFake when `PLAYGROUND_GIT_BACKEND=gitfake` is set.
5. Installs Sharko on the hub via Helm.
6. Registers the N spokes as **Sharko-managed** clusters via the REST API so
   each spoke's ArgoCD cluster Secret exists and carries
   `app.kubernetes.io/managed-by: sharko`.
7. Prints access instructions and runs a status snapshot.

**Override spoke count:** `PLAYGROUND_SPOKES=3 make playground-up` to add
more spokes.

**Idempotent:** re-running on an existing playground upgrades in place.

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
of addon labels on ArgoCD cluster Secrets, converging from
`configuration/managed-clusters.yaml` in git. ArgoCD's ApplicationSet reads
those labels and deploys addons — Sharko stays a **guest on ArgoCD**: Sharko
writes labels, ArgoCD owns ApplicationSets and deployment.

## Rebuild Loop (after code changes)

```bash
make playground-down && make playground-up
```

Or use the faster `scripts/dev-rebuild.sh` flow (see
`docs/site/developer-guide/personal-smoke-runbook.md`) if you're iterating
against a persistent cluster rather than the throwaway playground.

## Further Reading

- [ArgoCD ApplicationSet](https://argo-cd.readthedocs.io/en/stable/operator-manual/applicationset/) — the ArgoCD resource that reads cluster Secret labels and deploys addons
