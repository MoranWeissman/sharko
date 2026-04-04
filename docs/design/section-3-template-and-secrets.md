# Section 3 — Template Features & Secrets Management

> Analysis of production template features, what's OSS-relevant, and the decision to have Sharko manage remote cluster secrets.

---

## Production Template Feature Analysis

Each feature from the production `addons-appset.yaml` analyzed for OSS relevance.

### 1. Addon Secrets Pattern (Datadog example)

**What it is:** Addons need secrets from external providers (API keys, license keys, certificates). Current production uses ESO to fetch from AWS SM and create K8s Secrets.

**Decision:** The specific Datadog secret wiring is work-custom. But the GENERIC pattern — "addon needs secrets from a provider" — is valuable and becomes a Sharko core feature (see "Sharko Manages Remote Cluster Secrets" below). This replaces the need for ESO in most cases.

**OSS relevance:** HIGH — as a generic Sharko feature, not as Datadog-specific wiring.

---

### 2. Sync Wave Ordering (Istio example)

**What it is:** Some addons must deploy in a specific order. Istio requires: istio-base → istio-cni → istiod → istio-ingress. ESO needs to deploy before anything that depends on cluster secrets.

**Decision:** Ordered addon deployment is a real, common need. Not just Istio — any addon with CRD dependencies, security tools, or shared infrastructure that must be in place first.

**For v1.0.0:** The starter template supports sync waves via ArgoCD's native `argocd.argoproj.io/sync-wave` annotation. Sharko's `add-addon` command should accept `--sync-wave <number>` to set the deployment order in the AppSet entry. Simple, no custom logic needed — just the annotation.

**OSS relevance:** HIGH.

---

### 3. Multi-Source Applications

**What it is:** Some addons need multiple Helm sources — main chart + secrets chart + CRDs + custom config. ArgoCD supports multi-source natively.

**Decision:** Important capability. For v1.0.0, the starter template supports single-source addons. Multi-source is documented as an advanced customization — the user edits the AppSet template to add additional sources. Sharko doesn't generate multi-source config automatically.

**Future:** `sharko add-addon --source <additional-chart>` could add secondary sources. But this is complex and rare enough to defer.

**OSS relevance:** MEDIUM-HIGH. The capability exists in ArgoCD. Sharko documents it, doesn't automate it yet.

---

### 4. Infrastructure Node Separation (EKS Auto Mode / Karpenter)

**What it is:** Deploy addons on dedicated node pools, separate from business workloads. Uses Karpenter tolerations and node selectors.

**Decision:** Very custom to the specific work setup. The CONCEPT of "deploy addon to specific nodes" is valid but the implementation is highly environment-specific (Karpenter vs node selectors vs taints).

**For v1.0.0:** Not included. Users who need node separation configure it in their per-cluster values files. Document the pattern in the advanced guide.

**OSS relevance:** LOW for the specific implementation. The concept could become a feature later: `sharko add-addon --node-selector infra=true`.

---

### 5. Host Cluster Special-Casing

**What it is:** The ArgoCD management cluster deploys addons to itself using `in-cluster` as the destination instead of a registered cluster name. This is because ArgoCD always has a built-in `in-cluster` destination for the cluster it runs on — no separate cluster registration needed.

**Decision:** This is a standard ArgoCD pattern. The starter template should handle it — when the addon AppSet evaluates the host cluster, the destination uses `in-cluster`. This is already in the production template as a conditional.

**For v1.0.0:** Include in the starter template. It's a one-line conditional in the AppSet destination field.

**OSS relevance:** HIGH — anyone running addons on their management cluster needs this.

---

### 6. Migration Mode

**What it is:** Custom wizard for migrating addons from old ArgoCD (Azure DevOps) to new ArgoCD (GitHub). Specific to the work migration project.

**Decision:** Remove entirely from Sharko. Not useful for anyone else. The migration system was already stripped in v0.1.0. The template flag and `ignoreDifferences` entries for migration mode should also be removed from any template.

**OSS relevance:** NONE.

---

### 7. Per-Addon ignoreDifferences

**What it is:** ArgoCD shows "out of sync" for certain fields that are expected to drift (e.g., replicas managed by HPA, fields mutated by webhooks). Per-addon `ignoreDifferences` rules suppress these false positives.

**Decision:** Useful as a general capability. Should be part of the addon catalog config — when defining an addon, you can specify which fields to ignore for sync status.

**For v1.0.0:** The starter template supports `ignoreDifferences` in the AppSet spec. `sharko add-addon` could accept `--ignore-diff <group/kind/jsonPointers>` to configure it. Or it's a manual AppSet customization.

**OSS relevance:** MEDIUM. Nice to have, not critical for launch.

---

### 8. Migration Mode Flag

**What it is:** Same as #6. Custom work flag.

**Decision:** Remove. Already stripped.

**OSS relevance:** NONE.

---

### 9. goTemplateOptions (missingkey=zero)

**What it is:** ArgoCD ApplicationSet uses Go templates. `missingkey=zero` means missing keys return empty strings instead of errors. Essential when clusters don't have values files for every addon.

**Decision:** Already set in the starter template. This is a must-have, non-negotiable.

**Open question:** Should Sharko generate AppSet conditions using code logic (Go/server-side) instead of relying on Helm Go templates in the AppSet? This would give more control and better error handling. But it means Sharko generates AppSet YAML programmatically, which we decided against. Keep the current approach: Helm templates in Git, `missingkey=zero` for tolerance.

**OSS relevance:** HIGH (already implemented).

---

### 10. Datadog Project Mapping

**What it is:** Custom mapping of clusters to Datadog project names for secrets fetching.

**Decision:** Remove from Sharko. Purely work-specific. If deploying Sharko at work, add as a custom flavor.

**OSS relevance:** NONE.

---

### 11. IRSA Role Injection for ESO

**What it is:** ESO needs an IAM role (via IRSA) to fetch secrets from AWS. The role ARN is injected per-cluster into the ESO service account.

**Decision:** If Sharko replaces ESO for secret fetching, this becomes unnecessary for that specific use case. ESO would only be needed for users who want to use it for OTHER secrets beyond what Sharko manages.

**For users who still want ESO:** Document the IRSA injection pattern as an advanced customization.

**OSS relevance:** LOW — becoming less relevant if Sharko handles secrets directly.

---

## Major Decision: Sharko Manages Remote Cluster Secrets

### The Problem

Currently, deploying addon secrets to remote clusters requires:
1. ESO installed on every remote cluster (50+ installations)
2. Each cluster's ESO configured with a SecretStore pointing to AWS SM
3. ExternalSecret CRs deployed to each cluster
4. ESO pulls secrets and creates K8s Secrets locally

That's an entire operator running on every cluster just to fetch a few secrets.

### The Research

- **ArgoCD Vault Plugin (AVP)** — deprecated due to secrets leaking into ArgoCD's Redis cache as plaintext. Sharko's approach does NOT have this problem because secrets never enter ArgoCD's pipeline.
- **ESO** — only creates secrets on the cluster where it's installed. Cannot push to remote clusters. PushSecret pushes to external stores (AWS SM, Vault), NOT to other K8s clusters.
- **No existing tool** creates K8s Secrets on remote clusters from a central management plane.

### How Sharko Does It

Sharko already fetches kubeconfigs from the secrets provider (AWS SM, K8s Secrets) when registering clusters in ArgoCD. The same kubeconfig can be used to build a temporary Kubernetes client and create secrets on the remote cluster.

```
AWS Secrets Manager
        |
        v
Sharko (central, management cluster)
        |
        | (builds temporary K8s client per cluster using kubeconfig from provider)
        v
K8s API on cluster-1  →  creates K8s Secret (addon API keys, etc.)
K8s API on cluster-2  →  creates K8s Secret
...
K8s API on cluster-50 →  creates K8s Secret
```

**Per-operation flow:**
1. Sharko receives request: "create secret `datadog-keys` on cluster `prod-eu` with data from AWS SM path `secrets/datadog/api-key`"
2. Sharko fetches the kubeconfig for `prod-eu` from the provider
3. Sharko builds a temporary K8s client using that kubeconfig
4. Sharko fetches the secret data from AWS SM (or whatever provider)
5. Sharko creates the K8s Secret on `prod-eu` via the K8s API
6. Sharko disconnects

No ESO needed on the remote cluster. No persistent connections held.

### Security Analysis

**NOT the same problem as AVP:**
- AVP: secrets flow through ArgoCD rendering → Redis cache → plaintext leak
- Sharko: secrets go directly to the K8s API on the remote cluster. ArgoCD never sees them.

**Sharko is architecturally equivalent to ESO** — an external system creating K8s Secrets on a cluster. The difference: ESO runs locally on each cluster, Sharko runs centrally.

**The honest risk:** Sharko becomes a high-value target. Compromise of Sharko = access to secrets on all clusters. But Sharko already holds all kubeconfigs (for ArgoCD registration). Creating secrets doesn't expand the blast radius — it's the same access level.

**Mitigations:**
- Least-privilege RBAC: Sharko's kubeconfig access should be scoped to create/update/delete Secrets in specific namespaces only
- Audit logging: every secret creation is logged
- Short-lived K8s clients: connect, create, disconnect — no persistent sessions

### Comparison with ESO

| Aspect | ESO (current) | Sharko (proposed) |
|--------|---------------|-------------------|
| Operator per cluster | Yes (50+ installations) | No (central only) |
| Secrets in ArgoCD/Redis | No | No |
| Auto-rotation | Built-in (reconcile loop) | Manual for v1 (`sharko refresh-secrets`), auto in v2 |
| Drift detection | Built-in | Manual for v1, auto in v2 |
| Central management | No (per-cluster) | Yes |
| Blast radius | One cluster per ESO | All clusters (but same as existing kubeconfig access) |
| Additional dependency | ESO operator + CRDs per cluster | None — Sharko already exists |

### What This Means for the Dependency Chain

**Before (current):**
```
AWS EKS
  └── AWS Secrets Manager
        └── ESO (on every cluster)         ← REMOVED
              └── ArgoCD + ApplicationSets
                    └── Helm
                          └── Sharko
```

**After:**
```
AWS EKS
  └── AWS Secrets Manager
        └── ArgoCD + ApplicationSets
              └── Helm
                    └── Sharko (manages secrets directly)
```

ESO drops from a hard requirement to an optional addon. The dependency chain shrinks. The barrier to entry drops — users no longer need to understand, deploy, and configure ESO on every cluster before using Sharko.

### API Design for Secret Management

```
POST   /api/v1/clusters/{name}/secrets          → create a secret on a remote cluster
GET    /api/v1/clusters/{name}/secrets           → list Sharko-managed secrets on a cluster
DELETE /api/v1/clusters/{name}/secrets/{secret}   → delete a secret from a remote cluster

POST   /api/v1/addon-secrets                     → define a secret template for an addon
GET    /api/v1/addon-secrets                      → list addon secret definitions
```

**Addon secret definition example:**
```json
{
  "addon": "datadog",
  "secret_name": "datadog-keys",
  "namespace": "datadog",
  "data": {
    "api-key": { "provider_path": "secrets/datadog/api-key" },
    "app-key": { "provider_path": "secrets/datadog/app-key" }
  }
}
```

When a cluster has `datadog: enabled`, Sharko automatically creates the `datadog-keys` secret on that cluster by fetching the values from the provider. When `datadog` is disabled, Sharko removes the secret.

**CLI:**
```bash
sharko add-addon-secret datadog \
  --secret-name datadog-keys \
  --namespace datadog \
  --key api-key=secrets/datadog/api-key \
  --key app-key=secrets/datadog/app-key
```

### Critical: The Timing Problem — Secrets Must Exist Before ArgoCD Deploys

When Sharko adds a cluster with addons that need secrets, there's a race condition:

```
1. Sharko creates ArgoCD cluster secret with addon labels
2. ArgoCD detects new cluster, starts deploying addons (within seconds)
3. Addon pod starts, looks for K8s Secret... it doesn't exist yet
4. Pod crashes: "secret not found"
```

Sharko doesn't control ArgoCD's sync timing. ArgoCD could start deploying within seconds of seeing the cluster labels. The secret must be on the remote cluster BEFORE ArgoCD deploys the addon.

### Solution: The PR Is the Gate

The Git PR controls the entire flow. Nothing happens in ArgoCD until secrets are confirmed in place. The user experience stays simple — one command — but internally Sharko handles the ordering:

**Full orchestration flow for `sharko add-cluster prod-eu --addons datadog`:**

```
Step 1 — Fetch kubeconfig
  Sharko fetches kubeconfig for prod-eu from secrets provider (AWS SM / K8s Secret)

Step 2 — Open PR (ArgoCD sees nothing yet)
  Sharko creates branch sharko/add-cluster-prod-eu
  Sharko creates cluster values file: configuration/addons-clusters-values/prod-eu.yaml
  Sharko opens PR — this is just a file in Git, ArgoCD doesn't act on PRs
  
Step 3 — Create addon secrets on remote cluster
  Sharko checks: does datadog have addon-secrets defined?
  Yes → Sharko fetches secret values from provider (aws-sm:secrets/datadog/api-key, etc.)
  Sharko builds temporary K8s client using prod-eu kubeconfig
  Sharko creates K8s Secret "datadog-keys" in namespace "datadog" on prod-eu
  Sharko verifies the secret exists on the remote cluster
  Sharko disconnects from prod-eu
  
  If secret creation fails → PR stays open, Sharko returns partial success:
  "PR opened but addon secrets could not be created on cluster. 
   Fix the issue and run 'sharko retry-cluster prod-eu' or merge manually 
   (addon will fail until secrets are created)."

Step 4 — Create ArgoCD cluster secret
  Sharko creates K8s Secret in ArgoCD namespace with:
  - Cluster credentials (server URL, CA data, token)
  - Addon labels (datadog: enabled)
  ArgoCD now knows about the cluster and which addons to deploy
  BUT — the ApplicationSet won't match until the values file is on the main branch

Step 5 — Merge PR (or wait for human approval)
  If gitops.prAutoMerge: true → Sharko merges the PR automatically
  If gitops.prAutoMerge: false → PR stays open, human reviews and merges
  
  Either way: secrets are already on the cluster, ArgoCD cluster secret already exists

Step 6 — ArgoCD acts
  PR merge puts the values file on main
  ArgoCD ApplicationSet detects: cluster prod-eu has label datadog=enabled
  ApplicationSet generates the Datadog Application for prod-eu
  ArgoCD syncs → deploys Datadog to prod-eu
  Datadog pod starts → finds datadog-keys secret → works ✓
```

### Why This Ordering Is Safe

The key insight: **ArgoCD can't deploy addons until TWO things are true:**
1. The cluster secret exists in ArgoCD with the right addon labels
2. The cluster values file exists on the main branch (merged PR)

Sharko controls both. It creates the cluster secret (step 4) and it controls when the PR merges (step 5). Between those steps, it creates secrets on the remote cluster (step 3). The ordering is guaranteed regardless of ArgoCD's sync timing.

### What Happens When People Edit Git Manually

If someone manually edits the values file in Git and adds a cluster with addon labels, bypassing Sharko:
- ArgoCD will deploy the addon
- The addon secret won't exist (Sharko didn't create it)
- The addon will fail

**This is expected and by design.** The docs say: "Manage clusters through Sharko (CLI/API/UI), not by editing files directly. The GitOps files are the OUTPUT of Sharko, not the input."

If someone insists on manual Git edits, they also need to manually create the secrets on the remote cluster. That's their choice and their responsibility. Sharko's value is that it handles the entire chain.

### Auto-Merge vs Wait for Approval

The gitops config controls this:

```yaml
# Helm values
gitops:
  prAutoMerge: true    # Sharko merges PRs automatically after secrets are confirmed
  # or
  prAutoMerge: false   # PR stays open, human reviews and merges
```

**Auto-merge (IDP/automation path):** Terraform creates cluster → calls Sharko API → Sharko creates secrets → Sharko merges PR → ArgoCD deploys. Fully automated, no human in the loop.

**Manual merge (cautious path):** User runs `sharko add-cluster` → Sharko creates secrets and opens PR → human reviews the PR in GitHub → human merges → ArgoCD deploys. Human stays in control.

Either way, secrets are created BEFORE the merge. The merge is the final gate that activates ArgoCD.

### Addon Removal — Reverse Flow

When an addon is disabled on a cluster (`sharko update-cluster prod-eu --remove-addon datadog`):

```
Step 1 — Remove addon label from ArgoCD cluster secret
  Sharko updates the cluster secret: datadog label removed
  ArgoCD ApplicationSet no longer matches prod-eu for Datadog
  ArgoCD removes the Datadog Application from prod-eu

Step 2 — Delete addon secrets from remote cluster
  Sharko connects to prod-eu
  Sharko deletes K8s Secret "datadog-keys" from namespace "datadog"
  Sharko disconnects

Step 3 — Update values file in Git
  Sharko removes datadog section from prod-eu.yaml
  Sharko commits (direct or PR)
```

The secret is cleaned up after ArgoCD removes the addon. Order doesn't matter as much here — the addon is already gone.

### Implementation for v1.0.0

| Component | What to Build |
|-----------|---------------|
| Remote K8s client builder | Given a kubeconfig from the provider, build a temporary `kubernetes.Interface` client. Connect, perform operations, disconnect. No persistent connections. |
| Secret creation on remote cluster | Create/update/delete K8s Secrets via remote client. Label with `app.kubernetes.io/managed-by: sharko` so ArgoCD ignores them. |
| Addon secret definitions | Stored in server config (Helm values or ConfigMap). Maps addon name → secret name + namespace + provider paths for each key. |
| Orchestrator ordering | Modified `RegisterCluster` flow: open PR → create secrets on cluster → create ArgoCD cluster secret → merge PR. Partial success if secret creation fails (PR stays open). |
| Auto-create on cluster add | When `sharko add-cluster --addons datadog`, orchestrator checks if datadog has addon-secret definitions. If yes, creates them before enabling ArgoCD labels. |
| Auto-delete on addon removal | When disabling an addon via `sharko update-cluster --remove-addon`, delete the addon's secrets from the cluster after ArgoCD removes the Application. |
| Secret verification | After creating a secret on a remote cluster, read it back to confirm it exists. Don't proceed to ArgoCD/merge until verified. |
| ArgoCD annotation | All Sharko-created secrets labeled with `app.kubernetes.io/managed-by: sharko`. Configure ArgoCD to ignore resources with this label (resource exclusion). |
| API endpoints | `POST/GET/DELETE /api/v1/clusters/{name}/secrets` — manage secrets on a specific cluster. `POST/GET /api/v1/addon-secrets` — manage addon secret definitions. |
| CLI commands | `sharko add-addon-secret` — define what secrets an addon needs. `sharko list-secrets <cluster>` — see what secrets Sharko manages on a cluster. `sharko refresh-secrets <cluster>` — re-fetch and update all Sharko-managed secrets on a cluster. |

---

## Summary: What Goes in the Starter Template

| Feature | In Starter | How |
|---------|-----------|-----|
| Basic single-chart addons | Yes | Default AppSet pattern |
| Sync wave ordering | Yes | `--sync-wave` flag on `add-addon` |
| Multi-source apps | No | Document as advanced customization |
| Node separation | No | Document as advanced customization |
| Host cluster `in-cluster` | Yes | Conditional in AppSet destination |
| Migration mode | No | Removed entirely |
| Per-addon ignoreDifferences | Later | Document for now, automate in v1.x |
| goTemplateOptions | Yes | Already set to `missingkey=zero` |
| Addon secrets via Sharko | Yes | Core feature — replaces ESO for most use cases |

---

## Open Questions for Later Sections

- Credential rotation for cluster kubeconfigs (Section 4)
- Git commit flow per-operation overrides (Section 5)
- Self-documenting repos (Section 6)
- UI write capabilities (Section 7)
- Init sync verification (Section 8)
- Batch operations (Section 9)
