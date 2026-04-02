# ArgoCD Sync Failure Categorization — Datadog Log Pipeline

This log pipeline automatically categorizes ArgoCD controller error logs into failure types, enabling filtering and trend analysis in Datadog.

---

## Setup Instructions

### Step 1: Create the Pipeline

1. Go to **Datadog → Logs → Configuration → Pipelines**
2. Click **Add a new pipeline**
3. Set:
   - **Filter:** `source:argocd status:error`
   - **Name:** `ArgoCD Sync Failure Categorization`

### Step 2: Add the Category Processor

Inside the pipeline, click **Add Processor** → **Category Processor**

- **Name the processor:** `Sync Failure Categorization`
- **Set target category attribute:** `@failure_category`
- **Categories (add each one by pasting the query, typing the name, and clicking Add):**

| Category Name | Query (paste this into the "All events that match" box) |
|---|---|
| `helm_rendering` | `@msg:"helm template" OR @msg:"failed to generate manifests" OR @msg:"error converting YAML" OR @msg:"manifest generation"` |
| `k8s_api_error` | `@msg:"the server could not find the requested resource" OR @msg:"is invalid" OR @msg:"field is immutable" OR @msg:"exceeded quota" OR @msg:"no matches for kind"` |
| `rbac_permission` | `@msg:"forbidden" OR @msg:"cannot create" OR @msg:"cannot update" OR @msg:"cannot delete" OR @msg:"RBAC" OR @msg:"unauthorized"` |
| `admission_webhook` | `@msg:"admission webhook" OR @msg:"denied the request" OR @msg:"webhook" OR @msg:"validate" OR @msg:"mutate"` |
| `hook_failure` | `@msg:"hook failed" OR @msg:"job failed" OR @msg:"BackoffLimitExceeded" OR @msg:"PreSync" OR @msg:"PostSync"` |
| `cluster_connectivity` | `@msg:"TLS handshake timeout" OR @msg:"connection refused" OR @msg:"dial tcp" OR @msg:"cluster is not connected" OR @msg:"unable to connect"` |
| `timeout` | `@msg:"context deadline exceeded" OR @msg:"DeadlineExceeded" OR @msg:"timeout" OR @msg:"timed out"` |
| `resource_conflict` | `@msg:"already managed by" OR @msg:"resource already exists" OR @msg:"conflict" OR @msg:"the object has been modified"` |
| `secret_related` | `@msg:"SecretNotFound" OR @msg:"ExternalSecret" OR @msg:"secret" OR @msg:"decryption"` |
| `cache_error` | `@msg:"cache" OR @msg:"DiffFromCache" OR @msg:"key is missing"` |
| `other` | `*` |

### Step 3: Add the Status Remapper (optional)

Add another processor → **Status Remapper**
- **Name the processor:** `ArgoCD Log Level`
- **Set status attribute:** `level`
- This ensures the ArgoCD log `level` field (info/error/warn) maps correctly to Datadog log status.

### Step 4: Add the Application Name Extractor

Add another processor → **Grok Parser**
- **Name the processor:** `Extract Application Name`
- **Log samples:** paste one of these into the sample box:

```
time="2026-03-17T09:37:14Z" level=error msg="ComparisonError: helm template failed" app-namespace=argocd application=datadog-my-app-dev project=datadog
```

```
time="2026-03-17T09:37:14Z" level=error msg="Sync operation failed: one or more objects failed to apply" app-namespace=argocd application=istio-base-example-target-cluster project=istio-base
```

- **Parsing rule:** paste this into "Define parsing rules":

```
argocd_app %{data::keyvalue("=","","\"")}
```

- **Extract from:** `message` (default, leave as-is)

This extracts all key-value pairs from the log line as structured attributes: `application`, `project`, `app-namespace`, `level`, `msg`, etc.

### Step 5: Verify

1. Go to **Logs → Explorer**
2. Search: `source:argocd @failure_category:*`
3. You should see logs with the `@failure_category` facet
4. Click **Facets** panel → verify `failure_category` appears

---

## Using the Categories

### In Datadog Log Explorer

Filter by category:
```
source:argocd @failure_category:helm_rendering
source:argocd @failure_category:rbac_permission
source:argocd @failure_category:timeout
```

### In Dashboards

Use a **Pie Chart** or **Top List** widget with:
- Query: `source:argocd status:error`
- Group by: `@failure_category`

This shows the distribution of failure types over time.

### In Monitors

Create a log-based monitor:
- Query: `source:argocd @failure_category:helm_rendering`
- Alert when count > 5 in 15 minutes
- This catches spikes in a specific failure type

---

## Category Descriptions

| Category | What it means | Common cause |
|---|---|---|
| `helm_rendering` | ArgoCD can't render manifests from Helm/Kustomize | Bad values YAML, missing required values, broken template |
| `k8s_api_error` | Manifests are valid but K8s API rejects them | Immutable field change, CRD not installed, API version removed |
| `rbac_permission` | ArgoCD doesn't have permission to create/update resources | Missing ClusterRole/RoleBinding |
| `admission_webhook` | A webhook policy rejected the resource | OPA/Gatekeeper/Kyverno policy violation |
| `hook_failure` | PreSync/PostSync hook Job failed | Hook script error, OOMKill in hook container |
| `cluster_connectivity` | ArgoCD can't reach the target cluster | Network issue, TLS cert expired, cluster down |
| `timeout` | Operation took too long | Slow cluster API, large manifest set, controller overload |
| `resource_conflict` | Another controller manages the same resource | Conflicting operators, resource already exists |
| `secret_related` | Secret/ExternalSecret issues | AWS Secrets Manager unavailable, missing secret |
| `cache_error` | ArgoCD internal cache issues | Usually transient, resolves on next reconciliation |
| `other` | Doesn't match any known pattern | New error type, needs investigation |

---

## Maintenance

When new error patterns appear that don't match existing categories:
1. Check logs in Datadog: `source:argocd @failure_category:other`
2. Identify the new pattern
3. Add a new category rule in the pipeline (before `other`)
4. Update this document

---

## References

- [Datadog Log Pipelines](https://docs.datadoghq.com/logs/log_configuration/pipelines/)
- [Datadog Category Processor](https://docs.datadoghq.com/logs/log_configuration/processors/#category-processor)
- [Datadog Grok Parser](https://docs.datadoghq.com/logs/log_configuration/parsing/)
- [Sync Failure Categorization](../docs/observability/SYNC_FAILURE_CATEGORIZATION.md) — detailed failure taxonomy
