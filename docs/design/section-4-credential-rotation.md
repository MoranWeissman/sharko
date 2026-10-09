# Section 4 — Credential Rotation

> Short section. This turned out to not be a real problem in practice.

---

## The Concern

Cluster credentials could expire or get rotated, causing ArgoCD to lose connectivity. Sharko has no automatic mechanism to detect stale credentials and refresh them.

## The Reality

In practice with EKS clusters, once a cluster is connected via ArgoCD, credentials don't rotate frequently. The auth method (IAM-based or long-lived service account tokens) is stable. No credential breakage has been observed in production across 50+ clusters over months of operation.

The "credentials expire and everything breaks" scenario is more relevant to:
- Clusters with manually created service account tokens that have explicit expiry
- Non-EKS clusters with certificate-based auth that expires
- Environments with forced token rotation policies

For most EKS users, this is not a problem.

## Decision: Manual Refresh, No Automation

**For v1.0.0:**
- `sharko refresh-cluster <name>` exists as a CLI command (and API endpoint `POST /api/v1/clusters/{name}/refresh`)
- Fetches fresh credentials from the secrets provider, updates the ArgoCD cluster secret
- No automatic rotation, no scheduled refresh, no event-driven detection
- Documented: "If a cluster becomes disconnected due to credential changes, run `sharko refresh-cluster <name>`"

**If users report this as a real problem:** build scheduled or detect-and-refresh automation. But don't build it preemptively for a problem that doesn't exist in practice.

## Important Clarification

Sharko does NOT register clusters. ArgoCD does. Sharko creates the ArgoCD cluster secret (K8s Secret with proper label, credentials, and addon labels). ArgoCD reads that secret and handles the actual cluster connection, authentication, and ongoing management. The credential lifecycle is ArgoCD's responsibility. Sharko just provides the initial credentials and can refresh them on demand.
