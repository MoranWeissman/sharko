# ArgoCD Sync Failure Categorization & Troubleshooting Guide

**Related:** [APPLICATION_REQUIREMENTS.md](./APPLICATION_REQUIREMENTS.md) Section 4

---

## Failure Categories

### Category 1: Manifest Generation Errors (ComparisonError)

**What:** ArgoCD cannot render manifests from the source (Helm/Kustomize/Jsonnet).

**Causes:**
- Invalid YAML syntax in values files
- Helm template rendering failures (missing required values, bad template logic)
- Kustomize build errors
- Missing Helm chart dependencies (external repos unavailable)
- Non-existent paths in the source repository

**Detection:**
- `app.status.conditions` contains `ComparisonError`
- Sync status shows `Unknown` or `OutOfSync` without a sync operation running
- Repo-server logs contain generation errors

**Log Patterns:**
```
helm template failed
kustomize build failed
failed to generate manifests
error converting YAML to JSON
```

**Resolution:**
1. Check the ArgoCD UI for the specific error in the Application conditions
2. Verify the Helm values file syntax: `helm template <chart> -f values.yaml`
3. Check if all required values are provided in global/cluster values
4. Verify Helm repo is accessible: `helm repo update`
5. Check repo-server logs: `kubectl logs -n argocd deploy/argocd-repo-server`

---

### Category 2: Kubernetes API / Apply Errors

**What:** Manifests are valid but the K8s API rejects them during apply.

**Causes:**
- Schema validation failures (invalid field names, wrong types)
- Immutable field changes (Service type, PVC storage class, Job spec)
- API version deprecated or removed (e.g., `extensions/v1beta1`)
- CRD not installed (custom resource type unknown)
- Resource quota exceeded
- Resource name too long

**Detection:**
- `operationState.phase` = `Failed`
- Per-resource `syncResult.resources[].status` = `SyncFailed`

**Log Patterns:**
```
the server could not find the requested resource
is invalid
field is immutable
exceeded quota
no matches for kind
```

**Resolution:**
1. Check `operationState.message` for the specific resource and error
2. For immutable field errors: delete and recreate the resource, or use `Replace=true` sync option
3. For CRD errors: ensure CRDs are installed before the app that uses them
4. For quota errors: increase resource quotas or reduce resource requests

---

### Category 3: RBAC / Permission Errors

**What:** ArgoCD service account lacks permissions to create/modify resources.

**Causes:**
- Missing ClusterRole or RoleBindings for `argocd-application-controller`
- Namespace-scoped restrictions
- Pod Security Policy/Admission violations

**Detection:**
- `operationState.phase` = `Failed`
- Error contains `forbidden` or `cannot`

**Log Patterns:**
```
forbidden
User "system:serviceaccount:argocd:argocd-application-controller" cannot
is forbidden
RBAC: access denied
```

**Resolution:**
1. Check which resource/verb is forbidden from the error message
2. Verify ArgoCD ClusterRole has the required permissions
3. For namespace-scoped: ensure RoleBinding exists in target namespace
4. Check if PSP/PSA is blocking pod creation

---

### Category 4: Admission Webhook Rejections

**What:** Cluster admission controllers (Kyverno, OPA/Gatekeeper, custom webhooks) reject resources.

**Causes:**
- Policy violations (missing labels, disallowed images, security context)
- Webhook timeout or unavailability
- Mutating webhook conflicts

**Detection:**
- `operationState.phase` = `Failed`
- Error mentions `admission webhook`

**Log Patterns:**
```
admission webhook .* denied the request
validate.*.webhook
mutate.*.webhook
policy .* is violated
```

**Resolution:**
1. Read the webhook denial message for the specific policy violated
2. Fix the resource to comply with the policy, OR
3. Add a policy exception for the ArgoCD-managed resource
4. For webhook timeout: check the webhook service health

---

### Category 5: Hook Failures

**What:** Sync hooks (PreSync, Sync, PostSync) fail during execution.

**Causes:**
- PreSync Job fails (e.g., database migration error)
- PostSync health check fails
- Hook resource OOMKilled or crashes
- Hook timeout exceeded

**Detection:**
- `operationState.phase` = `Failed`
- `syncResult.resources[].hookPhase` = `Failed`
- Resource kind is typically `Job` with hook annotation

**Log Patterns:**
```
hook failed
job failed
BackoffLimitExceeded
DeadlineExceeded
OOMKilled
```

**Resolution:**
1. Check hook Job/Pod logs for the specific error
2. For OOMKilled: increase memory limits on the hook resource
3. For timeout: increase `argocd.argoproj.io/hook-delete-policy` timeout
4. For SyncFail hooks: check if cleanup hooks are configured

---

### Category 6: Cluster Connectivity / Infrastructure Errors

**What:** ArgoCD cannot reach the target cluster or Git repository.

**Causes:**
- Target cluster API server unreachable
- TLS certificate errors or expiry
- Network connectivity loss
- Git repository authentication failure
- Git repository unreachable

**Detection:**
- `operationState.phase` = `Error` (not Failed)
- `sync.status` = `Unknown`
- `argocd_cluster_connection_status` = 0

**Log Patterns:**
```
TLS handshake timeout
connection refused
dial tcp .* i/o timeout
authentication required
repository not found
context deadline exceeded
unable to connect to cluster
```

**Resolution:**
1. Check cluster connectivity: `kubectl cluster-info --context <cluster>`
2. For TLS errors: verify/renew cluster certificates
3. For Git auth: check deploy key or token validity
4. Check network policies or firewall rules
5. Monitor `argocd_cluster_connection_status` metric

---

### Category 7: Timeout / Deadline Exceeded

**What:** Sync or health check takes too long.

**Causes:**
- Large number of resources to sync
- Slow cluster API server
- Resource stuck in `Progressing` (e.g., ImagePullBackOff)
- Context deadline exceeded during manifest generation

**Detection:**
- `operationState.phase` = `Error`
- `health.status` = `Progressing` for extended periods

**Log Patterns:**
```
context deadline exceeded
DeadlineExceeded
timeout waiting for
operation timed out
```

**Resolution:**
1. Check if specific resources are stuck in Progressing state
2. For ImagePullBackOff: verify image exists and registry credentials
3. For large apps: consider splitting into smaller applications
4. Increase sync timeout if needed: `spec.syncPolicy.syncOptions: [Timeout=300]`

---

### Category 8: Resource Conflicts

**What:** Multiple applications or tools manage the same resource.

**Causes:**
- Two ArgoCD Applications target the same resource
- Resource managed by both ArgoCD and kubectl/Helm directly
- SharedResource annotation conflicts

**Detection:**
- App shows `OutOfSync` immediately after successful sync
- Error mentions "already managed by"

**Log Patterns:**
```
already managed by
resource already exists
unable to reconcile: conflict
```

**Resolution:**
1. Identify which applications manage the conflicting resource
2. Remove the resource from one application's scope
3. Use `shared-resource` annotation if intentional sharing
4. Use `ignoreDifferences` for fields managed externally

---

### Category 9: Secret-Related Failures

**What:** Secrets or sensitive data cause sync issues.

**Causes:**
- ExternalSecret provider errors (AWS Secrets Manager unreachable)
- Sealed Secrets decryption failures
- Secret data format issues

**Detection:**
- Health status of ExternalSecret/SealedSecret shows `Degraded`
- Error messages may be redacted for security

**Log Patterns:**
```
SecretNotFound
provider error
decryption failed
could not get secret data
```

**Resolution:**
1. Check ExternalSecret status: `kubectl get externalsecret -n <ns> -o yaml`
2. Verify AWS credentials/IAM role for the secret store
3. Check if the secret exists in AWS Secrets Manager
4. Verify all required keys exist in the secret (see cluster ExternalSecret template)

---

## Transient vs Persistent Failure Detection

| Characteristic | Transient | Persistent |
|---|---|---|
| Resolves on retry | Yes | No |
| Common causes | Connectivity, timeouts, rate limits | Config errors, missing permissions |
| Alert strategy | After 2+ consecutive failures | Immediately |
| Example | TLS handshake timeout | Helm template syntax error |

**Detection method:** Track consecutive failures per app. If sync fails again within the next reconciliation cycle without a success in between, classify as persistent.

---

## Datadog Log Pipeline Configuration

### Grok Parser for Failure Categorization

```
# ArgoCD application-controller log parsing
rule_1 %{data}helm template failed%{data}              # Category 1
rule_2 %{data}kustomize build failed%{data}             # Category 1
rule_3 %{data}failed to generate manifests%{data}       # Category 1
rule_4 %{data}is invalid%{data}                         # Category 2
rule_5 %{data}field is immutable%{data}                 # Category 2
rule_6 %{data}forbidden%{data}                          # Category 3
rule_7 %{data}admission webhook%{data}denied%{data}     # Category 4
rule_8 %{data}hook failed%{data}                        # Category 5
rule_9 %{data}connection refused%{data}                 # Category 6
rule_10 %{data}context deadline exceeded%{data}          # Category 7
rule_11 %{data}already managed by%{data}                # Category 8
```

### Facet: `@failure_category`

Map parsed rules to category names for dashboard filtering and alerting.
