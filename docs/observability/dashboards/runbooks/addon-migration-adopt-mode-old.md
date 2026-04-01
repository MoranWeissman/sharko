# Addon Migration Guide – Argo CD ApplicationSet Adopt Mode

This guide provides step-by-step instructions for **DevOps engineers** to migrate addons from the OLD ArgoCD instance to the NEW ArgoCD instance without service disruption.

Follow these steps exactly to ensure zero-downtime migration with no resource deletions or pod restarts.

---

## Prerequisites

### OLD ArgoCD (devops-argocd-addons-dev)

**Source Control:** Azure DevOps

**ApplicationSet Configuration (Already Configured):**

The OLD ArgoCD ApplicationSet has been configured to allow safe Application deletion without destroying cluster resources. These settings enable zero-downtime migrations:

1. **`syncPolicy.preserveResourcesOnDeletion: true`** (ApplicationSet level)
   - **Purpose:** When an Application is removed from the ApplicationSet, resources remain in the cluster
   - **Why critical:** Without this, removing an Application would delete all its resources (pods, services, etc.) causing service disruption
   - **Migration impact:** Allows OLD ArgoCD to "let go" of resources without destroying them

2. **`prune: false`** (Application template)
   - **Purpose:** Prevents ArgoCD from deleting resources that exist in cluster but not in Git
   - **Why critical:** Without this, ArgoCD would delete "unmanaged" resources during sync
   - **Migration impact:** Ensures resources remain untouched when Applications are removed

3. **No finalizers** (Application template)
   - **Purpose:** Allows Applications to be deleted immediately without waiting for resource cleanup
   - **Why critical:** Finalizers would trigger resource deletion before Application removal
   - **Migration impact:** Clean Application deletion without touching resources

**Result:** OLD ArgoCD can delete Applications while resources continue running, ready for NEW ArgoCD to adopt them.

**Status:** ✅ Configuration complete - no action needed by migration engineers

### NEW ArgoCD (devops-argocd-addons-dev-eks)

**Source Control:** GitHub
**Repository:** `github.com/merck-ahtl/argocd-cluster-addons`

**Configuration Files:**
- `configuration/addons-catalog.yaml` - Addon definitions and ApplicationSets
- `configuration/cluster-addons.yaml` - Cluster definitions and labels
- `configuration/addons-global-values/<addon>.yaml` - Global default Helm values per addon (applies to ALL clusters)
- `configuration/addons-clusters-values/<cluster-name>.yaml`  - Per-cluster Helm value overrides

**Required Setup:**
- ✅ ApplicationSet deployed and generating Applications
- ✅ ApplicationSet configured with `automated.prune: false` (prevents accidental resource deletion)
- ✅ `migrationIgnoreDifferences` defined globally in `addons-catalog.yaml` (pod template ignore rules - see [Understanding migrationIgnoreDifferences](#understanding-migrationignoredifferences))
- ✅ Per-addon `inMigration` flag support in ApplicationSet template (controls when to inject ignore rules)
- ✅ All target clusters registered with proper credentials

**Important Note on Auto-Sync:**
- Auto-sync can remain **enabled** during migrations
- The combination of `prune: false` + `migrationIgnoreDifferences` provides full protection
- Auto-sync will NOT cause resource deletion (prevented by `prune: false`)
- Auto-sync will NOT cause pod restarts (prevented by `migrationIgnoreDifferences`)
- No need to disable auto-sync or switch to manual sync mode

### Required Access

**GitHub:**
- Write access to `github.com/merck-ahtl/argocd-cluster-addons`
- Ability to create branches and push commits

**Azure DevOps:**
- Write access to OLD ArgoCD repository
- Ability to create PRs and merge

**ArgoCD Access:**
- **UI Access:** Both OLD and NEW ArgoCD web interfaces
- **CLI Access (optional):** `argocd` CLI installed and authenticated to both instances
  - UI can perform all migration operations
  - CLI provides faster verification and scripting capability

**Kubernetes:**
- **kubectl access to TARGET clusters** - Required to verify resources (pods, deployments) still running during migration
- **kubectl access to ArgoCD management clusters** - NOT required (ArgoCD UI handles all Application operations)

---

## Goals

* Migrate addons from **OLD ArgoCD → NEW ArgoCD**
* Zero downtime and no pod restarts
* No resource deletions during migration
* ArgoCD takes ownership of existing resources

---

## Migration Process (10 Steps)

This migration uses ArgoCD's adoption capability to transfer ownership from OLD ArgoCD to NEW ArgoCD without recreating resources.

### Step 1: Deploy Addon in NEW ArgoCD

In the NEW ArgoCD repository on GitHub, deploy the addon to the target cluster:

**1.1 Enable migration mode for addon (addons-catalog.yaml):**
```yaml
applicationsets:
  - appName: istiod
    repoURL: https://istio-release.storage.googleapis.com/charts
    chart: istiod
    version: 1.22.0
    namespace: istio-system
    inMigration: true  # ← Add this flag to enable migration protection
```

**What this does:**
- Sets `inMigration: true` on the specific addon being migrated
- ApplicationSet will inject `migrationIgnoreDifferences` (pod template ignore rules) into this addon's Application
- Prevents pod restarts during adoption by ignoring template differences
- Should be removed after migration completes (Step 10)

**1.2 Enable addon on cluster (cluster-addons.yaml):**
```yaml
clusters:
  - name: target-cluster
    labels:
      istiod: enabled
      istiod-version: "1.22.0"
```

This causes the ApplicationSet to create the Application.

---

### Step 2: Configure Values (If Needed)

If the addon requires global or per-cluster values, define them.

**CRITICAL:** Values **must match** the OLD ArgoCD configuration exactly - **no diffs allowed**.

**2.1 Global Default Values (applies to ALL clusters)**

Edit `configuration/addons-global-values/<addon>.yaml`:

```yaml
# Example: configuration/addons-global-values/istiod.yaml
istio:
  cni:
    enabled: true
```

These values apply to **every cluster** that enables this addon.

**2.2 Per-Cluster Overrides (optional)**

Edit `configuration/addons-clusters-values/<cluster-name>.yaml`:

```yaml
# Example: configuration/addons-clusters-values/feedlot-dev.yaml

# clusterGlobalValues section (YAML anchors for reuse)
clusterGlobalValues:
  env: &env dev
  clusterName: &clusterName feedlot-dev
  region: &region eu-west-1
  projectName: feedlot

# Addon overrides (override global defaults)
istiod:
  pilot:
    resources:
      requests:
        cpu: 500m
        memory: 2048Mi

datadog:
  datadog:
    clusterName: *clusterName  # Use YAML anchor
```

> **Note on Datadog API key:** The API key is handled automatically. A dedicated `charts/datadog-apikey`
> chart is deployed as a multi-source alongside the Datadog Helm chart. It creates an ExternalSecret
> that fetches the key from AWS Secrets Manager using `{projectName}-{env}` as the property name
> (e.g., `feedlot-dev`). The resulting K8s secret `datadog-api-key` is injected via the
> `datadog.parameters` helper. No manual `apiKeyExistingSecret` configuration is needed.

**How Values Merge:**
1. Start with addon defaults from `addons-global-values/<addon>.yaml`
2. Override with cluster-specific values from `addons-clusters-values/<cluster-name>.yaml`
3. Result: Per-cluster customization without duplicating common config

Commit and push to GitHub.

---

### Step 3: Verify Application Created in NEW ArgoCD

At this point:
- ApplicationSet has created the Application
- Application is looking at resources already deployed and managed by OLD ArgoCD
- Application shows **OutOfSync** (expected - hasn't taken ownership yet)

**Check in NEW ArgoCD UI:**
1. Navigate to Applications page
2. Find the Addon application
3. Verify Status: **OutOfSync** (this is normal - hasn't taken ownership yet)

**We now have TWO applications managing the same resources.**

---

### Step 4: Remove Addon from OLD ArgoCD

Go back to the OLD ArgoCD repository (Azure DevOps).

**Comment out the addon in clusters.yaml:**
```yaml
clusters:
  - name: target-cluster
    labels:
      # istiod: enabled           # ← Commented out
      datadog: enabled  # Other addons remain
```

Commit, create PR, and merge.

---

### Step 5: Sync Clusters Application in OLD ArgoCD

After merge completes, you have two options:

**Option A - Wait (3 minutes):**
ArgoCD will automatically sync.

**Option B - Force sync (immediate):**

In OLD ArgoCD UI:
1. Navigate to Applications
2. Find and click on **clusters** application
3. Click **Sync** button
4. Click **Synchronize**

This updates the cluster secret and removes the addon label.

---

### Step 6: ApplicationSet Removes Application (Resources Stay)

When the label is removed from the cluster secret:
- ApplicationSet detects the change
- ApplicationSet **deletes the Application**
- Resources **remain in the cluster** (not deleted)

**Why resources stay:**
- `syncPolicy.preserveResourcesOnDeletion: true` at ApplicationSet level
- `prune: false` in Application template
- No finalizers on Applications

**Verify:**

**In OLD ArgoCD UI:**
1. Navigate to Applications page
2. Search for the Addon application
3. Expected: Application should NOT be found

**In Target Cluster (via kubectl):**
```bash
# Resources still running
kubectl get deployment istiod -n istio-system
kubectl get pods -n istio-system
# Expected: All running, no restarts
```

---

### Step 7: Verify Application Exists in NEW ArgoCD

Go back to NEW ArgoCD and verify:

**In NEW ArgoCD UI:**
1. Navigate to Applications page
2. Find and click on the Addon application
3. Verify application exists and shows OutOfSync status

**In Target Cluster (via kubectl):**
```bash
# Check resources in cluster
kubectl get deployment istiod -n istio-system
kubectl get pods -n istio-system
```

**Expected state:**
- Application exists in NEW ArgoCD
- Application is OutOfSync (resources not yet owned by NEW ArgoCD)
- All resources present and running

---

### Step 8: Hard Refresh - Take Ownership

Hard refresh forces NEW ArgoCD to re-discover resources and take ownership.

**In ArgoCD UI:**
1. Navigate to Applications page
2. Click on the Application (istiod-target-cluster)
3. Click **Refresh** button
4. Select **Hard Refresh**

**What happens:**
- ArgoCD queries cluster for all resources
- Takes ownership by updating tracking labels/annotations
- Application now recognizes resources as managed

---

### Step 9: Sync - Make Application Healthy

Now sync the Application.

**In ArgoCD UI:**
1. On the Application page
2. Click **Sync** button (top right)
3. Review changes in the sync preview window
4. Click **Synchronize**

**Expected result:**
- Sync Status: **Synced**
- Health Status: **Healthy**
- Resources: **No restarts** (adopted, not recreated)

**Verify:**
```bash
# Verify no pod restarts
kubectl get pods -n istio-system -o wide
# AGE should match pre-migration (no restarts)
```

**Check Application status in UI:**
- Status should show: **Synced** + **Healthy**
- No resources in "Progressing" state

---

### Step 10: Disable Migration Mode (Cleanup)

After the addon migration is complete and verified, remove the migration flag.

**Edit `configuration/addons-catalog.yaml`:**
```yaml
applicationsets:
  - appName: istiod
    repoURL: https://istio-release.storage.googleapis.com/charts
    chart: istiod
    version: 1.22.0
    namespace: istio-system
    inMigration: false  # ← Set to false or remove this line entirely
```

**Commit and push:**
```bash
git add configuration/addons-catalog.yaml
git commit -m "Disable migration mode for istiod - migration complete"
git push
```

**What this does:**
- Removes the `migrationIgnoreDifferences` from the Application
- NEW ArgoCD will now enforce pod template specifications normally
- Future changes to pod templates will trigger proper reconciliation
- Keeps the Application managing resources with full drift detection

**When to do this:**
- After migration is verified successful (Step 9 complete)
- After 24-48 hours of stability monitoring (optional but recommended)
- Before migrating the next addon to same cluster

**Migration complete!**

---

## Understanding migrationIgnoreDifferences

**Location:** `configuration/addons-catalog.yaml` (NEW ArgoCD)

**Configuration:**
```yaml
# ================================================================ #
# Migration / Adoption Mode Configuration
# ================================================================ #
migrationIgnoreDifferences:
  - group: apps
    kind: Deployment
    jsonPointers:
      - /spec/template

  - group: apps
    kind: StatefulSet
    jsonPointers:
      - /spec/template

  - group: apps
    kind: DaemonSet
    jsonPointers:
      - /spec/template

  - group: batch
    kind: Job
    jsonPointers:
      - /spec/template

  - group: batch
    kind: CronJob
    jsonPointers:
      - /spec/jobTemplate/spec/template
```

### Why This Is Needed

During adoption, the NEW ArgoCD Application encounters resources that:
1. Already exist in the cluster
2. Were deployed by OLD ArgoCD
3. May have subtle differences in pod templates (annotations, labels, etc.)

**Without ignoreDifferences:**
- ArgoCD sees template differences as drift
- Attempts to reconcile by recreating pods
- **Causes service disruption** (pod restarts)

**With ignoreDifferences:**
- ArgoCD ignores pod template differences during adoption
- Focuses on adopting existing resources as-is
- **No pod restarts** - resources remain untouched
- Works seamlessly with auto-sync enabled (auto-sync can safely apply metadata changes while ignoring template diffs)
- After all migrations complete, you can remove this config to re-enable template enforcement

### When to Use

**During Migration:**
- Keep `migrationIgnoreDifferences` in place while migrating addons
- Prevents unexpected pod restarts during adoption phase

**After Migration:**
- Once ALL addons are migrated, you can remove this block
- NEW ArgoCD will then enforce template specifications
- Any future changes will trigger normal reconciliation

### How ApplicationSet Uses This

The ApplicationSet template (`bootstrap/templates/addons-appset.yaml`) uses the `inMigration` flag to control when to inject `migrationIgnoreDifferences`:

```yaml
{{- if or (eq $appset.appName "datadog") $appset.ignoreDifferences $appset.inMigration }}
ignoreDifferences:
  {{- if eq $appset.appName "datadog" }}
  {{- include "datadog.ignoreDifferencesItems" . | nindent 8 }}
  {{- end }}
  {{- if $appset.ignoreDifferences }}
  {{- toYaml $appset.ignoreDifferences | nindent 8 }}
  {{- end }}
  {{- if and $appset.inMigration $.Values.migrationIgnoreDifferences }}
  {{- toYaml $.Values.migrationIgnoreDifferences | nindent 8 }}
  {{- end }}
{{- end }}
```

**How it works:**
1. Set `inMigration: true` on specific addon in `addons-catalog.yaml`
2. ApplicationSet injects the global `migrationIgnoreDifferences` into that addon's Application
3. Addons with `inMigration: false` don't get the migration ignore rules
4. Datadog always gets its own operator-specific ignore rules (regardless of migration state)
5. Addon-specific `ignoreDifferences` (e.g., Karpenter NodePool limits) are always preserved
6. After migration, set `inMigration: false` to remove the migration rules

---

## Example: Migrating istiod to devops-automation-dev-eks

This example shows migrating the `istiod` addon from OLD ArgoCD to NEW ArgoCD for cluster `devops-automation-dev-eks`.

### Initial State

**OLD ArgoCD (Azure DevOps):**
```yaml
# clusters.yaml
clusters:
  - name: devops-automation-dev-eks
    labels:
      istiod: enabled
      istiod-version: "1.22.0"
      datadog: enabled
```

**NEW ArgoCD (GitHub):**
```yaml
# cluster-addons.yaml
clusters:
  - name: devops-automation-dev-eks
    labels:
      datadog: enabled
      # istiod not yet enabled
```

---

### Migration Steps

**1. Deploy in NEW ArgoCD**

First, enable migration mode in `configuration/addons-catalog.yaml`:
```yaml
applicationsets:
  - appName: istiod
    repoURL: https://istio-release.storage.googleapis.com/charts
    chart: istiod
    version: 1.22.0
    namespace: istio-system
    inMigration: true  # ← Add this flag
```

Then, edit `configuration/cluster-addons.yaml`:
```yaml
clusters:
  - name: devops-automation-dev-eks
    labels:
      datadog: enabled
      istiod: enabled          # ← Add
      istiod-version: "1.22.0" # ← Add
```

Check global values in `configuration/addons-global-values/istiod.yaml`:
```yaml
istio:
  cni:
    enabled: true
```

Check cluster-specific overrides in `configuration/addons-clusters-values/devops-automation-dev-eks.yaml` (if any):
```yaml
# No istiod overrides needed for this cluster
# Values from addons-global-values/istiod.yaml will be used
```

Commit and push:
```bash
git add configuration/cluster-addons.yaml
git commit -m "Add istiod to devops-automation-dev-eks for migration"
git push origin main
```

Check Application created in NEW ArgoCD UI:
1. Navigate to Applications page
2. Find **istiod-devops-automation-dev-eks** application
3. Verify Status: OutOfSync (expected)

---

**2. Remove from OLD ArgoCD**

Edit `clusters.yaml` in OLD ArgoCD repo:
```yaml
clusters:
  - name: devops-automation-dev-eks
    labels:
      # istiod: enabled          # ← Comment out
      # istiod-version: "1.22.0" # ← Comment out
      datadog: enabled
```

Commit, create PR, and merge:
```bash
git add clusters.yaml
git commit -m "Remove istiod from devops-automation-dev-eks for migration"
git push
# Complete PR process
```

Force sync clusters application in OLD ArgoCD UI:
- Navigate to **clusters** application → Click **Sync** → Click **Synchronize**

Verify Application removed in OLD ArgoCD UI:
1. Navigate to Applications page
2. Search for **istiod-devops-automation-dev-eks**
3. Expected: Application should NOT be found

Verify resources still exist in target cluster:
```bash
kubectl get deployment istiod -n istio-system
kubectl get pods -n istio-system
# All should still be running
```

---

**3. Adopt in NEW ArgoCD**

Hard refresh in NEW ArgoCD UI:
1. Navigate to **istiod-devops-automation-dev-eks** application
2. Click **App Details** → **Refresh** → **Hard Refresh**

Sync in NEW ArgoCD UI:
1. Click **Sync** button
2. Review changes
3. Click **Synchronize**

Verify:
```bash
# Watch pods (should NOT restart)
kubectl get pods -n istio-system -w

# Check AGE - should match pre-migration (no restarts)
kubectl get pods -n istio-system -o wide
```

Check status in NEW ArgoCD UI:
- Status should show: **Synced** + **Healthy**

---

**Migration Complete:**
- ✅ istiod managed by NEW ArgoCD
- ✅ No service disruption
- ✅ No pod restarts
- ✅ Resources adopted successfully

---

## Troubleshooting (should not happen since prerequisites were already taken care pre-migration process)

### Issue: Pods restart after sync in NEW ArgoCD

**Symptoms:**
- Pods restart when syncing Application in NEW ArgoCD (Step 9)
- Application shows many changes during sync (not just metadata)

**Cause:**
- `migrationIgnoreDifferences` not configured or not working properly
- ArgoCD sees differences in pod templates and attempts to roll out changes

**Root Causes:**
1. **Missing `migrationIgnoreDifferences` in addons-catalog.yaml**
   - ApplicationSet template not including the ignore rules
   - Generated Application lacks the ignoreDifferences configuration

2. **Values mismatch between OLD and NEW ArgoCD**
   - Image tags differ
   - Resource limits/requests differ
   - Configuration values differ
   - These are REAL differences, not just metadata

**Fix:**

**If caused by missing ignoreDifferences:**
1. Verify `migrationIgnoreDifferences` exists in `configuration/addons-catalog.yaml` (see Prerequisites section)
2. Delete and recreate the Application:
   - Remove addon label from `cluster-addons.yaml`
   - Commit and push (ApplicationSet removes Application)
   - Add addon label back
   - Commit and push (ApplicationSet creates Application with correct ignoreDifferences)
3. Proceed with hard refresh and sync

**If caused by values mismatch:**
1. Compare values between OLD and NEW ArgoCD carefully
2. Update values in NEW ArgoCD to match OLD exactly
3. The pod restarts were legitimate because values actually changed
4. This is expected if you intentionally changed values

**Prevention:**
- Always verify `migrationIgnoreDifferences` is in addons-catalog.yaml BEFORE starting migrations
- Always compare values between OLD and NEW to ensure they're identical

---

### Issue: Resources deleted when removing from OLD ArgoCD

**Symptoms:**
- Pods deleted when Application removed from OLD ArgoCD
- Service disruption

**Cause:**
- OLD ArgoCD ApplicationSet not configured for safe deletion
- Missing `preserveResourcesOnDeletion: true`
- Or finalizers still present

**Verification in OLD ArgoCD UI:**
1. Navigate to **Settings** → **ApplicationSets**
2. Find and click on **cluster-addons** ApplicationSet
3. Click **Manifest** or **YAML** view
4. Check for:
   ```yaml
   syncPolicy:
     preserveResourcesOnDeletion: true
   ```
5. In the Application template section, verify:
   ```yaml
   prune: false
   ```

**Fix:**
This should have been configured before migrations started. Contact the platform team to update OLD ArgoCD ApplicationSet configuration.

---

### Issue: Unexpected pod restarts after sync

**Symptoms:**
- Pods restart when syncing in NEW ArgoCD
- Application shows many changes during sync

**Cause:**
- Configuration differences between OLD and NEW ArgoCD
- Values don't match exactly
- Missing `migrationIgnoreDifferences` configuration

**Diagnosis:**

In ArgoCD UI:
1. Navigate to the Application
2. Click **App Diff** button
3. Review changes - look for template changes, image differences, resource limits

**Fix:**
1. If not yet synced, pause and review:
   - Compare values between OLD and NEW ArgoCD
   - Update NEW ArgoCD values to match OLD exactly
   - Delete and recreate Application with correct values

2. If already synced and pods restarted:
   - This is permanent - pods won't recover
   - Document the issue for future migrations
   - Ensure values match for remaining addons

**Prevention:**
- Always compare values between OLD and NEW before enabling addon
- Use `kubectl diff` or ArgoCD UI diff view
- Test on non-production clusters first

---

### Issue: Application stuck OutOfSync after hard refresh

**Symptoms:**
- Application remains OutOfSync after hard refresh
- Sync doesn't complete or fails

**Cause:**
- Resource ownership conflicts
- Resources have annotations/labels from OLD ArgoCD
- ArgoCD can't determine resource state

**Fix:**
1. Try hard refresh multiple times:
   - In ArgoCD UI: **App Details** → **Refresh** → **Hard Refresh**
   - Wait 10-15 seconds
   - Repeat hard refresh 2-3 times

2. If still OutOfSync, check resource annotations:
   ```bash
   kubectl get deployment istiod -n istio-system -o yaml | grep -A 5 "annotations:"
   ```

3. Remove old ArgoCD tracking annotations manually:
   ```bash
   kubectl annotate deployment istiod -n istio-system \
     argocd.argoproj.io/tracking-id- \
     argocd.argoproj.io/instance-
   ```

4. Hard refresh again and sync

---

### Issue: migrationIgnoreDifferences not working

**Symptoms:**
- ArgoCD shows template differences during sync
- Attempting to update pod specs

**Cause:**
- ApplicationSet template not properly configured to use `migrationIgnoreDifferences`
- Values not being merged correctly

**Verification in NEW ArgoCD UI:**
1. Navigate to the Application (istiod-cluster-name)
2. Click **App Details** button
3. Scroll down to **Ignore Differences** section
4. Verify it includes:
   - Addon-specific ignore rules (if any)
   - Migration ignore rules for pod templates

Alternatively, view the full Application manifest:
1. Click on the Application
2. Click the three-dot menu → **Manifest**
3. Search for `ignoreDifferences:` section

**Fix:**
Verify the addon has `inMigration: true` in `addons-catalog.yaml` and that `migrationIgnoreDifferences` is defined. Then delete and recreate the Application (remove addon label, commit, add back, commit) so ApplicationSet regenerates it with the correct ignoreDifferences.

---

## Best Practices

### 1. Migrate One Addon at a Time

**Never** migrate multiple addons simultaneously on the same cluster.

**Why:**
- Easier troubleshooting if something goes wrong
- Clear cause-and-effect relationship
- Reduced risk of conflicts or cascading issues
- Ability to roll back individual addons

### 2. Values Configuration Must Match Exactly

**Critical:** NEW ArgoCD values must produce **identical** manifests to OLD ArgoCD.

**Comparison Process:**

In ArgoCD UI:
1. **In OLD ArgoCD:** Navigate to Application → Click **App Diff** → Review current manifests
2. **In NEW ArgoCD:** Navigate to Application → Click **App Diff** → Review proposed manifests
3. **Compare:** Differences should be ONLY metadata (labels, annotations, tracking info)

Alternatively, use kubectl to compare actual resources vs desired state before syncing.

**Common Differences to Match:**
- Helm chart versions
- Image tags
- Resource limits/requests
- Replica counts
- ConfigMap values
- Environment variables

### 4. Documentation

**For Each Migration, Record:**
- Date and time of migration
- Addon name and cluster
- Any issues encountered
- Resolution steps taken
- Duration of migration process

---

## Migration Checklist

Use this checklist for each addon migration:

### Pre-Migration Verification
- [ ] Verify OLD ArgoCD ApplicationSet has `preserveResourcesOnDeletion: true`
- [ ] Verify OLD ArgoCD ApplicationSet has `prune: false`
- [ ] Verify NEW ArgoCD has `migrationIgnoreDifferences` in addons-catalog.yaml (prevents pod restarts)
- [ ] Identify target cluster and addon to migrate
- [ ] Compare values between OLD and NEW ArgoCD (must be identical)

### Step 1: Deploy in NEW ArgoCD
- [ ] Set `inMigration: true` on addon in `configuration/addons-catalog.yaml`
- [ ] Edit `configuration/cluster-addons.yaml` - add addon labels
- [ ] Check global default values in `addons-global-values/<addon>.yaml`
- [ ] Configure per-cluster overrides (if needed) in `addons-clusters-values/<cluster-name>.yaml`
- [ ] Verify no diffs compared to OLD ArgoCD values
- [ ] Commit and push to GitHub
- [ ] Verify Application created in NEW ArgoCD
- [ ] Confirm Application status: OutOfSync (expected)

### Step 2: Remove from OLD ArgoCD
- [ ] Edit `clusters.yaml` in OLD ArgoCD repo - comment out addon labels
- [ ] Commit, create PR, and merge
- [ ] Sync clusters application (wait 3 min or force sync)
- [ ] Verify Application removed from OLD ArgoCD
- [ ] Verify resources still running in target cluster
- [ ] Verify NO pod restarts occurred

### Step 3: Adopt in NEW ArgoCD
- [ ] Verify Application exists in NEW ArgoCD UI
- [ ] Perform hard refresh: App Details → Refresh → Hard Refresh
- [ ] Review diff: Click App Diff button
- [ ] Verify diff shows only metadata changes (labels/annotations)
- [ ] Sync Application: Click Sync → Synchronize
- [ ] Monitor sync completion in UI
- [ ] Verify NO pod restarts during sync (check kubectl)

### Step 4: Cleanup (Disable Migration Mode)
- [ ] Set `inMigration: false` (or remove) on addon in `configuration/addons-catalog.yaml`
- [ ] Commit and push to GitHub
- [ ] Verify ApplicationSet updates Application (ignoreDifferences removed)
- [ ] Monitor for drift detection working normally

### Post-Migration Verification
- [ ] Application status: Synced + Healthy
- [ ] All pods running with same AGE as before migration
- [ ] Application functionality tested
- [ ] Document migration completion date
- [ ] Update migration tracking system
- [ ] Monitor for 24-48 hours

### After All Migrations Complete
- [ ] Optionally remove `migrationIgnoreDifferences` block from addons-catalog.yaml (no longer needed)
- [ ] Verify all addons have `inMigration` disabled or removed

---

## Summary

This migration process provides:

* **Zero downtime** - Services remain available throughout migration
* **No pod restarts** - Resources adopted, not recreated
* **Predictable behavior** - Same process for all addons
* **Easy rollback** - Can revert by reversing steps
* **Audit trail** - All changes tracked in Git

**Key Success Factors:**
1. Values configuration matches exactly between OLD and NEW
2. Hard refresh performed before sync
3. One addon at a time
4. Thorough verification at each step
