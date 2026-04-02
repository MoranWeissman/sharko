# Section 2 — API, Auth & ArgoCD Integration

> Design decisions for the API surface, authentication for automation consumers, and ArgoCD integration scope.

---

## Decision: API Is the Product, No Platform-Specific Code

Sharko exposes a REST API. Backstage, Port, Terraform, bash scripts, Python bots, Slack workflows — they all just call HTTP endpoints. Sharko doesn't ship platform-specific plugins or integrations.

If the community wants a Backstage plugin, they build it using the API docs. That's community contribution, not Sharko's maintenance burden.

The IDP pitch: "Here's the API. Here's the docs. Call it from whatever you use."

---

## Decision: API Behavior Guarantees

For automation consumers, the API must be predictable. These are contractual guarantees for v1:

### Idempotency

- `POST /api/v1/clusters` with an existing cluster name → **409 Conflict**: `"cluster prod-eu already exists. Use PATCH to update or DELETE to remove."`
- Not idempotent by design. Create is create. Update is PATCH. This prevents accidental overwrites.
- For automation retry safety: 201 = done, 409 = already exists (move on), 502 = retry.

### Status Polling

After registering a cluster, ArgoCD takes ~3 minutes to sync addons. Sharko returns 201 when its work is done (credentials fetched, ArgoCD registered, Git committed). ArgoCD syncing is ArgoCD's job.

To check if addons are deployed: poll `GET /api/v1/clusters/{name}` and watch:
- `argocd_sync_status`: Unknown → OutOfSync → Synced
- `argocd_health_status`: Unknown → Progressing → Healthy

The UI shows this in real-time on the fleet dashboard. No special "readiness" concept needed — ArgoCD's own status model is sufficient.

### Error Recovery for Partial Success

When 207 is returned (e.g., cluster registered in ArgoCD but Git commit failed):
- **Do NOT retry the same POST** — ArgoCD registration already happened, retrying will get 409
- **Option A:** Fix the Git issue and retry just the Git part (not exposed as a separate endpoint yet — v1.x)
- **Option B:** `DELETE /api/v1/clusters/{name}` to clean up, then retry the full POST
- Document this flow in the API reference

### Backwards Compatibility

- Within `/api/v1/`: only additive changes. New fields in responses (old clients ignore them), new optional request fields, new endpoints. Never remove or rename existing fields.
- Breaking changes require `/api/v2/`. Old clients keep using v1 until deprecated.
- Once v1.0.0 ships, response shapes are locked.

---

## Decision: API Keys for Non-Interactive Consumers (v1.0.0)

Session tokens (24h expiry) work for humans. Automation needs long-lived tokens.

### Creating a Key

```bash
sharko token create --name "backstage-prod" --role admin
# → Token: sharko_a8f2b4c6d8e0f2a4b6c8d0e2f4a6b8c0
# → Store this securely. It won't be shown again.
```

### Token Format

- Prefix: `sharko_` — recognizable by security scanners (GitHub secret scanning, GitGuardian) if leaked
- Followed by 32 random hex characters
- Total: 39 characters

### Using a Key

```
Authorization: Bearer sharko_a8f2b4c6d8e0f2a4b6c8d0e2f4a6b8c0
```

Same header as session tokens. Server middleware checks: is this a session token? If not, is this an API key? Same authorization flow, different token type.

### Managing Keys

```bash
sharko token list
# NAME              ROLE    CREATED          LAST USED
# backstage-prod    admin   2026-04-01       2026-04-03
# ci-pipeline       admin   2026-03-28       2026-04-03

sharko token revoke backstage-prod
# → Token revoked.
```

### Storage

API keys are hashed (bcrypt) and stored in a K8s Secret. The plaintext is shown once at creation and never stored server-side. When a request comes in, the server hashes the provided token and compares against stored hashes.

### Scope

For v1.0.0: a token has a role (admin, operator, viewer) — same roles as user accounts. No fine-grained scopes. Simplicity over flexibility.

### Expiry

Permanent until revoked. Expiry-based tokens are a future enhancement if requested.

### UI + CLI

Both can create API keys:

**CLI:** `sharko token create --name "backstage" --role admin` — prints token once.

**UI:** Settings → API Keys → Create. Modal shows the token once with "Copy to Clipboard" button. Once dismissed, the key is gone — server only stores the hash. Same pattern as GitHub, Stripe, and every SaaS product.

---

## Decision: ArgoCD Integration Scope

### Principle: ArgoCD Is a Black Box Sharko Operates

Sharko talks to ArgoCD's API to do what it needs. The user doesn't need to open ArgoCD or run `argocd` CLI for addon fleet management. But if they want to, nothing breaks. ArgoCD is still there, fully functional.

The goal is not "replace ArgoCD UI." The goal is "you'll open ArgoCD less and less."

### What Sharko Does via ArgoCD API (v1.0.0)

| Operation | ArgoCD API Call | When |
|-----------|----------------|------|
| Register cluster | `POST /api/v1/clusters` | `sharko add-cluster` |
| Delete cluster | `DELETE /api/v1/clusters/{server}` | `sharko remove-cluster` |
| Update cluster labels | `PUT /api/v1/clusters/{server}` | `sharko update-cluster` |
| Add repo connection | `POST /api/v1/repositories` | `sharko init` (bootstrap) |
| Create AppProject | `POST /api/v1/projects` | `sharko init` (bootstrap) |
| Create Application | `POST /api/v1/applications` | `sharko init` (bootstrap) |
| Sync Application | `POST /api/v1/applications/{name}/sync` | After changes, on-demand |
| Refresh Application | `GET /api/v1/applications/{name}?refresh=true` | Force re-read from Git |
| Get Application status | `GET /api/v1/applications/{name}` | Fleet dashboard, status checks |
| List clusters | `GET /api/v1/clusters` | Fleet overview |
| List applications | `GET /api/v1/applications` | Addon status |
| Get version | `GET /api/v1/version` | Health check |
| Test connection | `GET /api/v1/version` | Connection test |

### What's Missing in the Current ArgoCD Client

The `AddRepository` method is not yet implemented. This is needed for `sharko init` — after pushing the addons repo to Git, Sharko needs to tell ArgoCD about the repo and provide credentials so ArgoCD can pull from it:

```
POST /api/v1/repositories
{
  "repo": "https://github.com/org/addons",
  "username": "x-access-token",
  "password": "<git-token>",
  "type": "git"
}
```

Without this, the root-app created during init will fail to sync because ArgoCD doesn't have repo credentials. This must be implemented for v1.0.0.

### What Users Might Still Do in ArgoCD Directly

- Investigate sync failures at the resource level (pod logs, events)
- Manage ArgoCD settings (RBAC, SSO, notifications)
- View the application DAG for complex multi-source apps
- Debug Helm rendering issues

These are power-user / troubleshooting workflows. Sharko doesn't need to replicate them. Over time, Sharko can absorb more (better sync failure diagnostics, resource-level views) but it's progressive enhancement, not v1 scope.

### ArgoCD API Versioning Risk

ArgoCD's API can change between versions. This is a maintenance burden but not a blocker:
- Sharko targets ArgoCD v2.x (the current stable)
- ArgoCD's API is relatively stable within major versions
- The ArgoCD client is isolated in `internal/argocd/` — if the API changes, only that package needs updating
- This is the same risk every ArgoCD integration tool accepts

---

## Decision: Core Default Addons

Server-level configuration for addons that every new cluster gets automatically:

```yaml
# Helm values
defaults:
  clusterAddons:
    monitoring: true
    logging: true
    cert-manager: true
```

When `sharko add-cluster prod-asia` is called without specifying addons, it gets these defaults. The user can override per-cluster:

```bash
sharko add-cluster prod-asia --addons monitoring,logging,cert-manager,istio
# Gets defaults + istio

sharko add-cluster test-eu --addons monitoring
# Only monitoring, overrides defaults
```

If no defaults are configured, clusters are registered with no addons unless explicitly specified.

---

## What This Means for Implementation

| Feature | Current State | Needed for v1.0.0 |
|---------|--------------|-------------------|
| API key auth | Not implemented | Token create/list/revoke, K8s Secret storage, middleware |
| API key UI | Not implemented | Settings page section, create modal, list/revoke |
| `AddRepository` ArgoCD method | Not implemented | Single method in client_write.go |
| Init calls AddRepository | Not implemented | Add to orchestrator InitRepo before CreateApplication |
| Core default addons | Not implemented | Server config, merge with per-cluster overrides in orchestrator |
| 409 for duplicate clusters | Partially (depends on ArgoCD error) | Explicit check in orchestrator before registration |
| API backwards compatibility | Not formalized | Document as policy, lock response shapes at v1.0.0 |

---

## Open Questions for Later Sections

- Starter template vs production template gap (Section 3)
- Credential rotation (Section 4)
- Git commit flow per-operation overrides (Section 5)
- Self-documenting repos (Section 6)
- UI write capabilities (Section 7)
- Init sync verification (Section 8)
- Batch operations (Section 9)
