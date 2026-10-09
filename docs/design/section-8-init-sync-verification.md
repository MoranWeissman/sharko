# Section 8 — Init Sync Verification

> How Sharko verifies that the bootstrap actually worked in ArgoCD.

---

## The Corrected Bootstrap Flow

The init sequence, in order:

```
Step 1 — Create repo structure, push to Git (via PR, auto-merge or manual)
  Sharko generates the addons repo from starter templates
  Pushes via PR (always — no direct commits)
  Result: Git repo has bootstrap/, configuration/, charts/

Step 2 — Add repo connection in ArgoCD
  POST /api/v1/repositories with Git URL + credentials from server config
  ArgoCD now has credentials to pull from this repo
  Result: ArgoCD can reach the Git repo

Step 3 — Create AppProject in ArgoCD
  POST /api/v1/projects
  Result: ArgoCD has a project scoped for Sharko's addons

Step 4 — Create root Application in ArgoCD
  POST /api/v1/applications pointing at bootstrap/ path in the Git repo
  Result: ArgoCD starts syncing the bootstrap Helm chart

Step 5 — ArgoCD syncs the bootstrap
  Bootstrap chart renders → creates ApplicationSets (one per addon in catalog)
  ApplicationSets have cluster label selectors
  No clusters registered yet → ApplicationSets match zero clusters → no Applications generated
  Nothing deploys. The structure is in place, ready for clusters.

Step 6 — Verify sync
  Sharko checks: did the root-app sync successfully?
  Success = ArgoCD rendered the bootstrap chart and created the ApplicationSets
  Failure = ArgoCD couldn't reach the repo, Helm rendering failed, permission issue, etc.
```

**Important:** At bootstrap time there are NO clusters registered. The ApplicationSets exist but match nothing. No addons deploy. The bootstrap just sets up the structure. Actual deployment happens later when the user runs `sharko add-cluster`.

---

## Verification Behavior — Different Per Interface

### CLI — Poll with Diagnostics

The CLI waits for ArgoCD to sync and shows real-time progress:

```
sharko init

Step 1/6 — Pushing repo structure to Git...          done
Step 2/6 — Adding repo connection to ArgoCD...        done
Step 3/6 — Creating AppProject in ArgoCD...           done
Step 4/6 — Creating root Application in ArgoCD...     done
Step 5/6 — Waiting for ArgoCD to sync bootstrap...    
  ⟳ Syncing... (15s)
  ⟳ Syncing... (30s)
  ✓ Synced and Healthy
Step 6/6 — Verifying ApplicationSets created...       done (3 ApplicationSets)

🦈 Init complete! Your addons repo is live.
   Run 'sharko add-cluster <name>' to register your first cluster.
```

If sync fails:

```
Step 5/6 — Waiting for ArgoCD to sync bootstrap...    
  ⟳ Syncing... (15s)
  ⟳ Syncing... (30s)
  ⟳ Syncing... (60s)
  ✗ Sync failed after 120s

  Error: ComparisonError
  Message: rpc error: code = Unknown desc = Manifest generation error:
           helm chart not found at path bootstrap/

  The root-app was created in ArgoCD but failed to sync.
  The repo structure and ArgoCD connection are in place.
  Fix the issue and ArgoCD will retry automatically.
  Run 'sharko status' to monitor.
```

The user gets the actual ArgoCD error without opening ArgoCD. Actionable.

**Timeout:** 2 minutes. ArgoCD's default sync interval is 3 minutes, but since we just created the app, ArgoCD should pick it up quickly. After 2 minutes, return with whatever status ArgoCD reports — don't hang forever.

**Poll interval:** Every 5 seconds. Check `GET /api/v1/applications/{root-app-name}` for sync status and health status.

### UI — Same Diagnostics, Visual Progress

The UI setup wizard shows the same steps with visual indicators:

```
✓ Git repo structure pushed
✓ ArgoCD repo connection added
✓ AppProject created
✓ Root Application created
⟳ Waiting for ArgoCD sync...
```

On success: green checkmarks, "Your addons repo is live" message, link to the fleet dashboard.

On failure: red X with the ArgoCD error message, guidance on how to fix it.

### API — Return Immediately, Caller Polls

An IDP or automation calling `POST /api/v1/init` should not wait 2 minutes. The API returns as soon as the Application is created in ArgoCD:

```json
{
  "status": "success",
  "repo": {
    "url": "https://github.com/org/addons",
    "branch": "main",
    "files_created": [
      "bootstrap/root-app.yaml",
      "bootstrap/templates/addons-appset.yaml",
      "bootstrap/Chart.yaml",
      "bootstrap/values.yaml",
      "configuration/addons-catalog.yaml"
    ]
  },
  "argocd": {
    "repo_connected": true,
    "project_created": true,
    "root_app_created": true,
    "root_app_name": "addons-bootstrap",
    "sync_status": "pending"
  }
}
```

`sync_status: "pending"` means ArgoCD hasn't synced yet. The caller can:

- **Option A:** Don't care. The structure is set up, ArgoCD will sync eventually. Move on to adding clusters.
- **Option B:** Poll `GET /api/v1/health` or `GET /api/v1/config` which includes ArgoCD app status information, until `sync_status` changes to `"synced"` or `"failed"`.

The API does NOT hang waiting for sync. It returns what it knows (all steps it performed) and lets the caller decide whether to wait.

---

## What ArgoCD Sync Actually Verifies

When the root-app syncs successfully, it means:

1. ArgoCD can reach the Git repo (repo connection works)
2. ArgoCD can read the bootstrap/ directory (path is correct)
3. The bootstrap Helm chart renders without errors (Chart.yaml, values.yaml, templates are valid)
4. The rendered resources (ApplicationSets) were applied to the cluster
5. The ApplicationSets exist and are ready to match clusters

This is a comprehensive health check of the entire init pipeline. If any of these fail, the sync error tells you exactly what broke.

---

## Sync Verification for Other Operations

Init is the only operation that polls for ArgoCD sync. Other operations (add-cluster, add-addon, update-cluster) do NOT poll:

- **add-cluster:** Sharko creates the ArgoCD cluster secret and merges the PR. ArgoCD will detect the new cluster and deploy addons within its sync interval (~3 minutes). The user sees the cluster appear in the Sharko UI dashboard with status progressing from "Registered" → "Syncing" → "Healthy."
- **add-addon:** Sharko adds the catalog entry and merges the PR. ArgoCD will render a new ApplicationSet entry. No immediate sync to verify.
- **update-cluster:** Sharko updates labels and merges the PR. ArgoCD picks up the label change on next sync.

Why only init? Because init is a one-time setup where failure means "nothing works at all." For day-to-day operations, the Sharko UI fleet dashboard shows ArgoCD status in real-time — that's the monitoring mechanism. No need to block the CLI for every operation.

If users want to verify a specific operation completed, they run `sharko status` or check the UI.

---

## Error Categories During Init

| Error | When | User Sees |
|-------|------|-----------|
| Git push failed | Step 1 | "Failed to push repo structure. Check Git credentials." |
| Repo connection failed | Step 2 | "ArgoCD couldn't connect to Git repo. Check URL and credentials." |
| Project creation failed | Step 3 | "Failed to create ArgoCD project. Check ArgoCD permissions." |
| Application creation failed | Step 4 | "Failed to create root Application. Check ArgoCD API access." |
| Sync timeout | Step 5 | "ArgoCD hasn't synced after 120s. The app was created — check ArgoCD for details." |
| Sync failed — Helm error | Step 5 | "Sync failed: [exact ArgoCD error message]. Fix the issue, ArgoCD will retry." |
| Sync failed — repo unreachable | Step 5 | "Sync failed: repository not accessible. Verify repo connection in ArgoCD." |
| Sync succeeded but ApplicationSets missing | Step 6 | "Sync completed but expected ApplicationSets not found. Check bootstrap chart templates." |

Each error is actionable. The user knows what broke and what to do about it.

---

## Summary

| Question | Answer |
|----------|--------|
| Does init verify ArgoCD synced? | Yes — CLI and UI poll with diagnostics. API returns immediately. |
| How long does it wait? | 2 minutes max, polling every 5 seconds. |
| What if sync fails? | Show the actual ArgoCD error. The root-app exists, ArgoCD will retry. |
| Do other operations verify sync? | No. Only init. Day-to-day status comes from the fleet dashboard. |
| Does the API block on sync? | No. Returns immediately with `sync_status: "pending"`. Caller polls if they want. |
