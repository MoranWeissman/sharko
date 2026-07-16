# Sharko Kubernetes Events Reference

## Overview

Sharko emits Kubernetes Events to the cluster where it runs, making operational failures and successes visible without digging through pod logs. Events are written to the Sharko namespace and visible via `kubectl get events`.

**What Sharko emits:**
- AWS credential/service failures (IAM, Secrets Manager, EKS token generation)
- ArgoCD API connectivity and authentication failures
- Remote cluster connection/test/doctor failures
- Git provider and PR operation failures
- Cluster reconciler drift detection and self-heal outcomes
- Success milestones (cluster registered, PR merged, connection restored)

**What Sharko does NOT emit:**
- Per-application ArgoCD sync events (those are ArgoCD's responsibility)
- Per-addon deployment progress (use ArgoCD UI/CLI for that)
- Routine polling or reconciler ticks (events are emitted on state CHANGE or genuine failure, not on every loop)

**In-cluster only:** Events are only emitted when Sharko runs inside a Kubernetes cluster. Local/dev mode (out-of-cluster) is silent.

## Event Reasons

Every event has a stable `Reason` identifier in UpperCamelCase format. The table below lists all Reasons currently emitted by Sharko.

| Reason | Type | What it means | Triggered by |
|--------|------|---------------|--------------|
| **AWS Provider Failures** | | | |
| `AWSAssumeRoleFailed` | Warning | IAM role assumption failed | Cluster registration, EKS discovery, doctor operations requiring cross-account access |
| `AWSSecretsGetFailed` | Warning | Secrets Manager get operation failed | Cluster registration when credentials stored in AWS Secrets Manager |
| `AWSTokenMintFailed` | Warning | EKS token generation (STS GetCallerIdentity) failed | EKS cluster discovery or registration |
| `AWSConfigLoadFailed` | Warning | AWS SDK config load failed | Any AWS provider operation (credential chain resolution failure) |
| `AWSCredentialsInvalid` | Warning | AWS credentials invalid or expired | Any AWS provider operation after initial config load |
| **Host ArgoCD API Failures** | | | |
| `ArgoCDUnreachable` | Warning | ArgoCD server unreachable (network/DNS) | Cluster discovery, any operation requiring ArgoCD API (list clusters, create Application, etc.) |
| `ArgoCDAuthFailed` | Warning | ArgoCD authentication failed (403/401) | Cluster discovery, any authenticated ArgoCD API call |
| `ArgoCDAPICallFailed` | Warning | ArgoCD API call failed (non-auth error) | Any ArgoCD API operation (create/update/delete Application, etc.) |
| **Remote Cluster Connection Failures** | | | |
| `ClusterTestFailed` | Warning | Stage1 connectivity test failed | `/api/v1/clusters/{name}/test` or automatic test during registration |
| `ClusterDoctorFailed` | Warning | Doctor diagnostic failed | `/api/v1/clusters/{name}/diagnose` operation |
| `ClusterConnectionFailed` | Warning | General cluster connection failure | Any remote cluster operation (kubectl/client-go connection failure) |
| `ClusterRBACDenied` | Warning | RBAC permission denied on remote cluster | Remote cluster operation where Sharko's ServiceAccount lacks required permissions |
| **Git / PR Failures** | | | |
| `PROpenFailed` | Warning | Failed to open PR via git provider | Cluster or addon configuration changes that should open a PR but git provider API call failed |
| `PRMergeFailed` | Warning | Failed to merge PR | Auto-merge operation failed (conflict, branch protection, API failure) |
| `GitPushFailed` | Warning | Git push failed | Local commit succeeded but push to remote failed (auth, network, ref conflict) |
| `GitAuthFailed` | Warning | Git authentication failed | Clone, fetch, or push operation with invalid/expired credentials |
| `GitCloneFailed` | Warning | Git clone/fetch failed | Initial clone or fetch of configuration repository (network, not-found, corrupted) |
| **Reconciler** | | | |
| `ReconcileFailed` | Warning | Cluster reconciler failed | Reconciler tick encountered an unrecoverable error (placeholder for future wiring) |
| `DriftDetected` | Warning | Drift detected between Git and ArgoCD | Reconciler detected that ArgoCD cluster Secret differs from Git source of truth (placeholder for future wiring) |
| **Success Events** | | | |
| `ClusterRegistered` | Normal | Cluster successfully registered | Cluster registration completed (Secret created in ArgoCD, PR opened or committed) |
| `ClusterReconciled` | Normal | Cluster reconciled successfully | Reconciler self-healed drift or verified no drift exists |
| `PRMerged` | Normal | PR merged successfully | Auto-merge completed or webhook received merge notification |
| `ConnectionRestored` | Normal | Connection to external service restored | After a failure (ArgoCD, AWS, Git), connection succeeded again |

## Watching Events

### List recent events in Sharko namespace

```bash
kubectl get events -n sharko --sort-by='.lastTimestamp'
```

### Watch events in real time

```bash
kubectl get events -n sharko --watch
```

### Filter to warnings only

```bash
kubectl get events -n sharko --field-selector type=Warning --sort-by='.lastTimestamp'
```

### Show detailed event information

```bash
kubectl describe events -n sharko
```

## Event Message Format

Each event includes:
- **Reason:** Stable identifier (from table above)
- **Type:** `Normal` or `Warning`
- **Message:** Plain-English description with contextual details (cluster name, error message, operation)

**Security:** Event messages NEVER include secret material (tokens, kubeconfigs, credentials, secret values). Events are visible cluster-wide.

## Troubleshooting with Events

When investigating a Sharko operational issue:

1. **Check recent warnings:**
   ```bash
   kubectl get events -n sharko --field-selector type=Warning --sort-by='.lastTimestamp' | tail -20
   ```

2. **Cross-reference with pod logs:** Events show *what* failed; pod logs show *why*. Use events to identify the failure time and operation, then check pod logs for that time window:
   ```bash
   kubectl logs -n sharko deployment/sharko --since=10m | grep -C5 "<event-reason>"
   ```

3. **Match events to runbooks:** Many event Reasons have corresponding P0/P1 runbooks in the Operator Manual. For example:
   - `ArgoCDUnreachable` → [P0 Runbook — ArgoCD Upstream Unreachable](argocd-upstream-unreachable.md)
   - `AWSSecretsGetFailed` → [P1 Runbook — AWS-SM Secret Not Found](aws-sm-secret-not-found.md)
   - `ReconcilerCrashLoop` → [P0 Runbook — Reconciler Crash Loop](reconciler-crash-loop.md)

## Event Retention

Kubernetes Events have a default TTL of **1 hour** (controlled by the kube-apiserver `--event-ttl` flag, typically set to `1h`). After 1 hour, events are automatically garbage-collected.

**For long-term audit:** Events are ephemeral. For long-term visibility, use:
- Sharko's audit log API (`/api/v1/audit`) for user-initiated operations
- Centralized logging (ship pod logs to CloudWatch, Elasticsearch, etc.)
- Event exporters (e.g., [kubernetes-event-exporter](https://github.com/resmoio/kubernetes-event-exporter)) to persist events to external storage

## Related Documentation

- [Cluster Reconciler (architecture + embedded troubleshooting)](cluster-reconciler.md)
- [Connection Doctor](connection-doctor.md)
- [Troubleshooting](troubleshooting.md)
- [Failure Mode Index](failure-mode-index.md)
