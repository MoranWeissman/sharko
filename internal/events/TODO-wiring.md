# V3 E1 Event Wiring TODO

This file documents where Kubernetes events should be emitted once the EventRecorder is fully integrated.

## Wiring Status

- ✅ **EventRecorder initialized** in `cmd/sharko/serve.go` (in-cluster only; nil-safe no-op otherwise)
- ✅ **EventRecorder attached to Server** via `srv.SetEventRecorder()` and accessible via `s.EventRecorder()`
- ✅ **RBAC permissions granted** (`events` create in release namespace)
- ⏸️ **Handler-level event emissions** — TODOs below

## Surfaces to Wire (in priority order)

### 1. AWS Provider Failures (HIGH)

**Where**: `internal/api/cred_lookup.go`, `internal/api/clusters_doctor.go`

**Events to emit**:
- `ReasonAWSSecretsGetFailed` — AWS Secrets Manager GetSecretValue failure
- `ReasonAWSAssumeRoleFailed` — IAM role assumption failure (doctor check)
- `ReasonAWSTokenMintFailed` — EKS token generation (STS) failure
- `ReasonAWSCredentialsInvalid` — AWS credentials invalid or expired

**Pattern**:
```go
if err := /* AWS call */; err != nil {
    if s.eventRecorder != nil {
        s.eventRecorder.Event(
            events.ReasonAWSSecretsGetFailed,
            fmt.Sprintf("Failed to get cluster credentials from AWS Secrets Manager: %s", sanitizeError(err)),
            events.EventTypeWarning,
        )
    }
    // existing error handling
}
```

**CRITICAL**: Never include secret values, account IDs, or credentials in event messages.

### 2. Host ArgoCD API Failures (HIGH)

**Where**: `internal/argocd/client.go`, `internal/api/clusters_doctor.go`

**Events to emit**:
- `ReasonArgoCDUnreachable` — ArgoCD server unreachable (network/DNS)
- `ReasonArgoCDAuthFailed` — ArgoCD auth failed (403/401)
- `ReasonArgoCDAPICallFailed` — ArgoCD API call failed (non-auth)

**Pattern**:
```go
// In internal/argocd/client.go:
// Add optional EventRecorder field to Client struct (injected at construction time)
if c.eventRecorder != nil {
    c.eventRecorder.Event(
        events.ReasonArgoCDUnreachable,
        "ArgoCD server unreachable: connection timeout",
        events.EventTypeWarning,
    )
}
```

### 3. Remote Cluster Connection Failures (MEDIUM)

**Where**: `internal/api/clusters_test.go`, `internal/api/clusters_doctor.go`, `internal/verify/stage1.go`

**Events to emit**:
- `ReasonClusterTestFailed` — Stage1 connectivity test failed
- `ReasonClusterDoctorFailed` — Doctor diagnostic failed
- `ReasonClusterConnectionFailed` — General cluster connection failure
- `ReasonClusterRBACDenied` — RBAC permission denied on remote cluster

**Pattern**:
```go
if result.Status == verify.StatusFailed {
    if s.eventRecorder != nil {
        s.eventRecorder.Eventf(
            events.ReasonClusterTestFailed,
            "Cluster connectivity test failed for %s: %s",
            events.EventTypeWarning,
            clusterName, sanitizeError(result.Error),
        )
    }
}
```

### 4. Git / PR Failures (MEDIUM)

**Where**: `internal/orchestrator/git.go`, `internal/api/clusters.go` (PR open paths)

**Events to emit**:
- `ReasonPROpenFailed` — Failed to open PR via git provider
- `ReasonPRMergeFailed` — Failed to merge PR
- `ReasonGitPushFailed` — Git push failed
- `ReasonGitAuthFailed` — Git authentication failed

**Pattern**: Orchestrator would need EventRecorder injected. Alternatively, emit from handlers that call orchestrator and check the GitResult.

### 5. Reconciler Drift Detection (FUTURE — EPIC-1 G1/G3)

**Where**: `internal/clusterreconciler/reconcile_status.go:307` (already has TODO comment)

**Event to emit**:
- `ReasonDriftDetected` — Drift detected between Git and ArgoCD

**Pattern**: Inject EventRecorder into Reconciler.Deps, emit once per fight (track `fightEventEmitted` flag to avoid spam).

### 6. Success Events (EMIT SPARINGLY)

**Where**: `internal/orchestrator/cluster.go`, `internal/clusterreconciler/`

**Events to emit**:
- `ReasonClusterRegistered` — Cluster successfully registered (once per registration)
- `ReasonClusterReconciled` — Cluster reconciled successfully (NOT every tick; only after fixing a previously-failing state)
- `ReasonPRMerged` — PR merged successfully (only for major operations, not routine changes)

## Security Gates

Before emitting ANY event, the message MUST pass security review:
1. No secret values (tokens, kubeconfigs, credentials, secret ARNs, secret values)
2. No AWS account IDs (real or placeholder)
3. No internal domains or employee emails
4. Plain-English operational statements only

The security-auditor agent will review every Event() call site before merge.

## Helper Pattern

For handlers that emit events, consider a helper:

```go
// emitOperationalEvent emits an event if the recorder is available (in-cluster).
// Skips emission if recorder is nil (out-of-cluster / dev mode).
func (s *Server) emitOperationalEvent(reason, message string, eventType events.EventType) {
    if s.eventRecorder != nil {
        s.eventRecorder.Event(reason, message, eventType)
    }
}
```

Then call:
```go
s.emitOperationalEvent(events.ReasonClusterTestFailed, "Stage1 test failed: network timeout", events.EventTypeWarning)
```

## Testing Pattern

Every event emission should have a unit test that:
1. Creates a fake kubernetes clientset
2. Creates an EventRecorder with it
3. Calls the code path that emits the event
4. Validates that the event Reason and EventType are correct
5. Validates that the message contains no secret material

See `internal/events/recorder_test.go` for examples.

## Next Steps

1. Add `emitOperationalEvent` helper to `internal/api/router.go`
2. Wire AWS failure paths first (highest operator value)
3. Wire ArgoCD failure paths second
4. Wire cluster connection / PR failures third
5. Leave reconciler events for EPIC-1 G1/G3 follow-up
6. Run security-auditor agent over all Event() call sites before merge
