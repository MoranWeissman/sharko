# Kill-Sharko verification — 2026-08-08

## Verdict

**Guarantee HELD.** Deleting Sharko's Deployment did not touch anything ArgoCD was running.
ArgoCD kept every Application Synced through the whole observation window, the workloads on
both spoke clusters were never restarted (same pod, same restart count, same object UID
throughout), and — the strong proof — a git push made directly to the playground's Gitea repo
while Sharko was dead was picked up and applied by ArgoCD with no help from Sharko at all.
Sharko came back cleanly on a plain `helm upgrade --install` (the same command the playground
used to install it originally), read the state correctly including the change made while it was
dead, and started with no errors and no crash loop.

## What was tested

- **Main SHA tested:** `45503903f798d760d617a23a35c94e3c9f3e206b` (short `45503903`) —
  `feat(ui): Secret Sync tiles v2 — one box per secret, ArgoCD-style (#766)`.
  This was what `main` pointed to during the test — the playground images were built from it
  (image tag on the Sharko deployment: `sharko:playground-45503903`), and the checkout stayed
  clean and read-only for the whole run.
- **Playground:** fresh `kind` hub + 2 spokes (`spoke-eu`, `spoke-us`), ArgoCD on the hub,
  Gitea as the git backend, Sharko installed via Helm. Torn down and rebuilt from scratch for
  this walk (`make playground-down` then `make playground-up`).
- Two earlier `make playground-up` attempts were interrupted by my own tooling (a wall-clock
  timeout on the bash call, not a Sharko bug) and left partial cluster state that made a later
  Gitea bootstrap step fail non-idempotently (`CreateUser: user already exists`, retried 10
  times then gave up). That's worth a look separately — `execGiteaCmd` / the Gitea user-create
  step in `cmd/playground/cmd_up.go` isn't safe to re-run against a half-built cluster — but it
  is not a v4 code-path bug and doesn't affect this verdict. The clean, uninterrupted run (the
  one this report is based on) came up correctly on the first try.

## Timeline (UTC)

| Time | Event |
|---|---|
| 22:36:14 | Playground fully up (`Playground is ready!`), catalog seeded, `metrics-server` enabled on `spoke-eu`, connectivity check on `spoke-us` |
| 22:42:54 | BEFORE snapshot captured |
| 22:42:57 | **Sharko's Deployment deleted** (`kubectl -n sharko delete deployment sharko`) |
| 22:43:04 – 22:46:39 | Observation window, snapshot every ~30s (8 snapshots, ~4 minutes) |
| 22:48:13 | Git commit pushed directly to Gitea `main`, bypassing Sharko entirely (Sharko confirmed still dead at push time) |
| 22:50:20 | ArgoCD's `metrics-server-spoke-eu` Application synced to the new commit; live Deployment/Service in `spoke-eu` carry the new label |
| 22:53:15 | Recovery started: `helm upgrade --install sharko charts/sharko ...` (same command `cmd/playground` uses) |
| 22:53:21 | Sharko pod up, logs clean, reconcilers started |
| 22:54:06 | Logged into the recovered Sharko API; `GET /api/v1/clusters` shows `spoke-eu` reconciled against **the exact commit pushed while Sharko was dead** |
| 22:54:20 | AFTER snapshot captured; port-forwards closed; playground left running |

## BEFORE state

```
=== ArgoCD Applications (BEFORE) ===
NAME                          SYNC STATUS   HEALTH STATUS   REVISION   PROJECT
connectivity-check-spoke-us   Synced        Healthy         0.4.0      sharko-addons
metrics-server-spoke-eu       Synced        Progressing                sharko-addons
sharko-engine                 Synced        Healthy                    default

=== Sharko deployment (BEFORE) ===
deployment.apps/sharko   1/1   1   1   8m29s   sharko   sharko:playground-45503903
pod/sharko-76c96bb7b5-8f8qb   1/1   Running   0   8m29s

=== spoke-eu (sharko-play-spoke-1) — kube-system ===
deployment.apps/metrics-server-spoke-eu   0/1   1   0   6m24s
pod/metrics-server-spoke-eu-69665dfb85-s4dnb   0/1   Running   0   6m24s
```

Note on `metrics-server-spoke-eu` never going `Ready`: this is a known kind-cluster thing, not
a Sharko or ArgoCD problem — the chart's readiness probe hits the kubelet over HTTPS and kind's
kubelet certs aren't trusted by default (`metrics-server` needs `--kubelet-insecure-tls` on
kind). The catalog entry Sharko serves for this addon actually documents the same quirk
(`"On clusters with self-signed kubelet certificates (common on kind/k3s...) it needs
--kubelet-insecure-tls..."`). The pod runs the whole test (`Running`, 0 restarts, same pod
identity throughout) — it just never passes its own health check, in BEFORE, DURING, and AFTER
alike. ArgoCD's health field for it flipped from `Progressing` to `Degraded` about 4 minutes in
— that's ArgoCD's own timeout on a probe that was already failing before Sharko died, not
something Sharko dying caused.

`sharko-engine`'s ArgoCD Application only tracks the meta-GitOps objects (`AppProject` +
2 `ApplicationSet`s) that drive the addon rollout machinery — it does not track Sharko's own
`Deployment`. Sharko's `Deployment` was installed by the playground's `helm upgrade --install`
step, outside ArgoCD's management. That's why deleting it did not get self-healed back by
ArgoCD (its `syncPolicy.automated.selfHeal` is `true`, but self-heal only re-applies resources
it is actually tracking) — confirmed by checking `sharko-engine`'s resource list, which has no
`Deployment` entry.

## DURING (Sharko dead) — 8 snapshots over ~4 minutes

Every snapshot: `connectivity-check-spoke-us` stayed `Synced`/`Healthy`, `sharko-engine` stayed
`Synced`/`Healthy`, `metrics-server-spoke-eu` stayed `Synced` (health went `Progressing` →
`Degraded` around minute 4, explained above — same pod, no restart). The Gitea pod in the
`sharko` namespace kept running the whole time (`1/1 Running`, restarts never incremented). The
`spoke-eu` metrics-server pod's age and restart count climbed with wall-clock time and nothing
else — no restarts, no replacement pod.

Full timestamped output: see `DURING.txt` captured during the run (kept alongside this report
in the same evidence run; not re-embedded here to keep this doc readable — the summary above is
the complete, honest picture, nothing trimmed changed the story).

## The strong proof — GitOps kept working with Sharko dead

1. Cloned the playground's Gitea repo directly (`git clone
   http://sharko:sharko-play@localhost:8097/sharko/sharko-playground.git`, reached through my
   own `kubectl port-forward` on port 8097 — not one of the maintainer's reserved ports).
2. Edited `values/global/metrics-server.yaml` — this is the Helm values file ArgoCD's
   `metrics-server-spoke-eu` `ApplicationSet`-generated `Application` reads via its `$values`
   git source. Changed:
   ```diff
   -commonLabels: {}
   +commonLabels: {kill-sharko-proof: "sharko-dead-when-applied"}
   ```
   This is exactly the kind of edit an operator would make by hand through a normal PR — a
   real Helm value, applied through the same values file Sharko itself would have written to,
   just done directly since Sharko wasn't there to open the PR.
3. Confirmed Sharko's Deployment was still gone right before pushing.
4. `git commit` + `git push origin main` — landed on Gitea as commit `b04964d`.
5. Polled ArgoCD (no help from Sharko) until it picked up the new revision:
   - `metrics-server-spoke-eu`'s `status.sync.revisions` moved from `6b06e25...` to
     `b04964df450fb91917a6c88ef79a41b1073e6175` at 22:50:20 UTC — about 2 minutes after the
     push, on ArgoCD's own polling interval.
   - `status.operationState`: `phase: Succeeded`, `message: "successfully synced (all tasks
     run)"`, `initiatedBy.automated: true` — ArgoCD's own auto-sync did this, nobody triggered
     it by hand.
   - The **live** Deployment and Service objects in `spoke-eu`'s `kube-system` namespace both
     carry the new label:
     ```
     "kill-sharko-proof":"sharko-dead-when-applied"
     ```
     (Helm's `commonLabels` applies to top-level object metadata for this chart version, not
     the pod template — so the label lands on the Deployment/Service objects themselves, which
     is exactly what was checked and confirmed.)

Sharko was dead for the entire sequence above — verified again immediately before the push
(`kubectl -n sharko get deploy,pods` showed only Gitea running).

## Recovery

Brought Sharko back with the exact install path the playground uses
(`cmd/playground/cmd_up.go:installSharko`):

```
helm upgrade --install sharko charts/sharko \
  --kube-context kind-sharko-play-hub \
  --namespace sharko --create-namespace \
  -f charts/sharko/values.yaml \
  --set image.repository=sharko \
  --set image.tag=playground-45503903 \
  --set image.pullPolicy=Never \
  --set bootstrapAdmin.password=admin \
  --set e2e.gitHostsAllowlist=gitea.sharko.svc.cluster.local \
  --wait --timeout 5m
```

Result: `Release "sharko" has been upgraded`, rollout completed clean, pod `Running` `1/1`,
**0 restarts**.

Checked, all clean:
- **Logs** (`kubectl -n sharko logs deploy/sharko`): normal startup sequence only — catalog
  loaded, gitea connection ok, cluster reconciler started, PR tracker started, HTTP server
  listening. No panics, no error-level lines, no crash loop.
- **API answering**: reached it through my own `kubectl port-forward` on port 8098 (not one of
  the reserved ports). `GET /` → 200. `POST /api/v1/auth/login` (admin/admin, the bootstrap
  password set above) → 200 with a bearer token. Unauthenticated `GET /api/v1/version` → 401
  (correct — proves the server is answering and enforcing auth, not that it's broken).
- **Reconcilers running**: log line `"cluster reconciler started"` with the right
  `managed_clusters_path` and tick interval; PR tracker started too.
- **Reads the state correctly, including the change made while it was dead**: `GET
  /api/v1/clusters` (authenticated) returned, for `spoke-eu`:
  ```json
  "last_reconcile": {
    "outcome": "succeeded",
    "compared_revision": "b04964df450fb91917a6c88ef79a41b1073e6175",
    "compared_path": "managed-clusters.yaml"
  }
  ```
  `b04964d...` is the exact commit pushed while Sharko was dead. Sharko picked it up and
  reconciled against it cleanly on its very first reconcile after coming back — it did not
  need to be told about the change, and it did not choke on it.
- **Nothing else moved**: the `spoke-us` connectivity-check ConfigMap has the same
  `metadata.uid` (`25a78004-cfdf-4809-ac59-ca1ca744d201`) and the same `resourceVersion`
  (`717`) after recovery as it had before the kill — never touched. The `spoke-eu`
  metrics-server pod is still the same pod (`metrics-server-spoke-eu-69665dfb85-s4dnb`, 0
  restarts throughout).

## Cleanup

Both `kubectl port-forward` sessions I opened (Gitea on 8097, Sharko on 8098) were killed at
the end of each use. No changes were made to the repo checkout — the git worktree stayed clean
throughout (only the write was to the playground's own Gitea repo, a separate throwaway git
remote created by the playground, not this repo). The playground (hub + 2 spokes, ArgoCD,
Gitea, and the now-recovered Sharko) is left running for the maintainer to look at.
