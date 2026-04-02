# Section 1 — Bootstrap & Onboarding

> Design decisions for how Sharko gets deployed, configured, and initialized.

---

## Research Summary

Studied 6 tools: ArgoCD, Backstage, Rancher, Crossplane, Flux CD, Harbor.

**Key findings:**
- Every tool uses K8s Secrets for credentials — universal standard, no exceptions
- Almost nobody has a setup wizard — Rancher is the sole exception
- Flux has the fastest time-to-value: one env var + one command (~2-3 min)
- Rancher has the best UX: helm install → UI wizard guides everything (~5-10 min)
- Nobody passes secrets in helm values AND configures them in a UI — it's one or the other
- Backstage is the heaviest (requires custom Docker image for production)

| Tool | K8s Secrets | UI Wizard | Helm-only Config | Time to Value |
|------|-------------|-----------|------------------|---------------|
| ArgoCD | Heavy | No | Yes | ~10 min |
| Backstage | Env vars | No | Partial | Hours (prod) |
| Rancher | Yes | Yes (strongest) | Partial | ~5-10 min |
| Crossplane | Yes | No UI at all | Only base install | ~10-15 min |
| Flux CD | Yes | No UI at all | No (CLI bootstrap) | ~2-3 min |
| Harbor | Yes | No, but rich UI | Mostly yes | ~5-10 min |

---

## Decision: Secrets via K8s Secrets

Sharko gets all credentials from K8s Secrets. This is the universal Kubernetes pattern.

- **Git token** → K8s Secret
- **ArgoCD token** → K8s Secret
- **Provider credentials** → K8s Secret (or IRSA for AWS)

The Helm chart either creates these secrets from values or references pre-existing secrets (user creates them beforehand). Both patterns are supported.

---

## Decision: Hybrid Configuration — Helm Values + UI

Not one or the other. Both. The system is smart about what's configured and what's missing.

**If the user passes everything via Helm:**

```bash
helm install sharko --namespace sharko \
  --set git.repoURL=https://github.com/org/addons \
  --set git.token=<token> \
  --set git.branch=main \
  --set argocd.token=<token> \
  --set provider.type=aws-sm \
  --set provider.region=eu-west-1 \
  --set init.autoBootstrap=true
```

Sharko starts, all connections are configured, auto-initializes the repo, bootstraps ArgoCD. Zero UI interaction needed. This is the IDP/automation path — a platform team deploys via Terraform and Sharko is fully operational without a human.

**If the user does a bare install:**

```bash
helm install sharko --namespace sharko
```

Sharko starts with no connections. First visit to the UI shows the connections page with status for each:

```
Git Repository
  ✗ Not configured
  [Configure →]

ArgoCD  
  ✗ Not configured
  [Configure →]

Secrets Provider
  ✗ Not configured
  [Configure →]

Addons Repository
  ✗ Not initialized
  (requires Git + ArgoCD to be connected first)
```

**If the user passes some things in Helm and forgets others:**

```bash
helm install sharko --namespace sharko \
  --set argocd.token=<token>
```

First visit shows:

```
Git Repository
  ✗ Not configured
  [Configure →]

ArgoCD  
  ✓ Connected — argocd.sharko.svc.cluster.local (v2.13.1)
  Token: ••••••••8b1c

Secrets Provider
  ✗ Not configured
  [Configure →]
```

Green for what's there. Red for what's missing. Action buttons to fill the gaps. The "Initialize" button appears only when all prerequisites are met.

---

## Decision: No Separate Setup Wizard

The connections page IS the setup experience. It's not a one-time wizard — it's a persistent status page that shows connection health and allows configuration. On first run it effectively becomes a wizard because nothing is configured. On day two it's a health dashboard. Same component, two modes.

The existing connections page in the current codebase already does most of this — it shows ArgoCD and Git connection status with test buttons. It needs:
- Better empty/unconfigured state (prominent, actionable)
- Provider configuration section
- "Initialize" button when all connections are ready
- Status indicators (green check / red X) at a glance

---

## Decision: Auto-Bootstrap on First Startup

If all connections are configured (via Helm values) AND the Git repo is empty, Sharko auto-initializes on first startup. No manual step.

Controlled by Helm value:

```yaml
init:
  autoBootstrap: true  # default: false
```

When `true`: server starts → detects empty repo → runs InitRepo → bootstraps ArgoCD → ready for API calls. This enables fully automated deployment:

```
Terraform creates EKS cluster
  → Terraform helm-installs Sharko with all values
  → Sharko auto-initializes
  → IDP starts calling the API
  → No human touched anything
```

When `false` (default): server starts, waits for user to click "Initialize" in UI or run `sharko init` via CLI. This is safer for users who want to review the config before initialization.

---

## Decision: IDP-First, UI-Second

The primary consumer of Sharko is automation — IDP platforms, CI/CD pipelines, Terraform, scripts. The API is the product.

The UI exists for:
- **Observability** — fleet dashboard, version matrix, drift detection
- **Configuration** — connections page, provider setup
- **Demo/presentation** — the visual experience at conferences

But day-to-day operations come through the API. This means:
- The API must be rock solid (consistent response shapes, proper error codes, partial success)
- Auth must support non-interactive consumers (API keys, not just session tokens)
- The Helm install must be fully declarative (no UI wizard required for automated deployments)

---

## Decision: API Keys for Non-Interactive Consumers (v1.0.0)

Session-based auth (username/password → 24h token) works for humans using the CLI and UI. But an IDP calling the API can't re-authenticate every 24 hours.

API keys are needed for v1.0.0:
- `sharko token create --name backstage --scope read,write`
- Returns a long-lived API key
- Stored as K8s Secret
- Sent as `Authorization: Bearer <api-key>` — same header, different token type
- Manageable via CLI (`sharko token list`, `sharko token revoke`) and UI

This is how ArgoCD handles it (account tokens with `argocd account generate-token`) and how most API products work.

---

## Decision: Webhooks Are Post-v1

The API response tells the caller what happened synchronously. That's enough for v1. Webhook events (`cluster.registered`, `addon.drift.detected`) are a real feature for async IDP integration but deferred to a later release.

---

## Decision: No Platform-Specific Integrations

Sharko does NOT ship Backstage plugins, Port actions, Terraform providers, or any platform-specific code. The API is the integration surface. Any platform that makes HTTP calls can integrate.

If the community wants to build a Backstage plugin for Sharko, they use the API docs. That's community contribution, not something Sharko maintains.

The IDP pitch: "Here's the API. Here's the docs. Call it from whatever you use."

---

## What This Means for Implementation

Compared to what's currently built, the gaps are:

| Feature | Current State | Needed for v1.0.0 |
|---------|--------------|-------------------|
| Connections page | Exists (ArgoCD + Git config) | Add provider config section, init button, status indicators |
| Auto-bootstrap | Not implemented | Server detects empty repo on startup, calls InitRepo if `autoBootstrap=true` |
| Helm values for all config | Partial (some env vars) | Full values.yaml schema for git, argocd, provider, init |
| API key auth | Not implemented | Token create/list/revoke via CLI and API, long-lived Bearer tokens |
| Webhook events | Not implemented | Deferred post-v1 |

---

## Open Questions for Later Sections

- What exactly should the fleet dashboard show? (Section 2)
- What write operations should be exposed in the UI vs CLI-only? (Section TBD)
- How does the AI assistant fit into the IDP story? (Section TBD)
