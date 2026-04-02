# Sharko — Framework Vision

> **Name:** Sharko — the shark alongside the Argo. ArgoCD's octopus + Sharko's shark.
> Two ocean predators, same ecosystem. The "-o" suffix mirrors ArgoCD — companion tools.
>
> **Logo:** Dual-tone shark fin (cyan #00C9E0 + deep blue #1E62D0).
> Clean version (icon/favicon) and fin with waterline wave (README/marketing).
>
> **Pronunciation:** SHAR-koh. Unambiguous in any language. 6 letters, 2 syllables.

---

## 1. What Exists Today

Two repos, tightly related:

- **`argocd-cluster-addons`** — A GitOps blueprint for managing 50+ EKS clusters from a single config file using ArgoCD ApplicationSets + Helm
- **`argocd-addons-platform`** — A control plane UI: drift detection, version matrix, cluster health, observability

### Dependency Chain

```
AWS EKS
  └── AWS Secrets Manager
        └── ESO (External Secrets Operator)
              └── ArgoCD + ApplicationSets
                    └── Helm
                          └── argocd-cluster-addons (the pattern)
                                └── argocd-addons-platform (the UI)
```

### Honest Categorization

This is a **blueprint** — an opinionated reference implementation for a specific stack. Not a framework (too many hardwired dependencies), not a plugin (plugins extend an API).

"Blueprint" isn't a demotion. AWS EKS Blueprints is literally called that and is well-respected. Platform engineers understand the term: opinionated, production-tested, adopt within a specific ecosystem.

The community independently converged on the same ArgoCD ApplicationSet pattern for fleet-scale addon management (~60% adoption). This solution validates that best practice. The UI is the real differentiator — nobody else does fleet-wide drift detection and version matrix well.

---

## 2. The Gap in the OSS Landscape

A thorough investigation (March 2026) across ArgoCD, Flux, Rancher Fleet, OCM, Crossplane, KubeVela, Cluster API, and commercial solutions found:

> **No project provides a pluggable secrets/credentials backend for cluster addon management.**

Everyone wires ESO or SOPS manually. Every organization hardcodes their secrets provider. There is no `secretsProvider: aws-sm` / `secretsProvider: vault` abstraction for fleet management.

The delivery mechanism (ArgoCD vs Flux) is a solved problem — ArgoCD won. Abstracting it away means building for a minority. **The secrets backend is where the real abstraction belongs.**

---

## 3. The Sharko Framework

### Server-First Architecture

Sharko is a **server** that runs in-cluster, next to ArgoCD. The CLI is a thin HTTP client — like `kubectl` to the Kubernetes API server, or `argocd` CLI to the ArgoCD server.

**`sharko`** — One repo, one binary, one product:

```
sharko/
  cmd/sharko/       → Cobra CLI entry point (serve, init, add-cluster, etc.)
  internal/
    api/            → HTTP handlers (thin glue code)
    orchestrator/   → Workflow engine (the brain)
    providers/      → Secrets provider interface + implementations
    argocd/         → ArgoCD REST client
    gitprovider/    → Git provider interface (GitHub, AzureDevOps)
    ...
  ui/               → React frontend (fleet dashboard)
  templates/        → Reference addons repo structure
    starter/        → Clean scaffold embedded in binary (what sharko init generates)
    bootstrap/      → Full production AppSet templates (reference)
    charts/         → Addon Helm charts (reference)
    configuration/  → Cluster values, global values (reference)
    monitoring/     → Datadog CRDs, dashboards, monitors (reference)
  charts/sharko/    → Helm chart for deploying Sharko itself
  docs/
```

**The server holds all credentials:**
- ArgoCD account token (Bearer auth, stored in K8s Secret)
- Git token (configured once in Helm values)
- Secrets provider access (AWS IRSA, K8s service account)

**Nobody stores credentials on their laptop.** The CLI sends requests to the Sharko API. The server does all the work.

- Fleet observability UI (drift detection, version matrix, cluster health)
- REST API — the core product. Every consumer (UI, CLI, Backstage, Port, Terraform, CI/CD) talks to the same API
- CLI as thin client for terminal workflows
- Pluggable secrets provider interface
- AI assistant for troubleshooting and fleet insights
- Orchestrator engine that executes complete GitOps workflows (not just file generation)

### The Provider Interface

```go
type ClusterCredentialsProvider interface {
    GetCredentials(clusterName string) (Kubeconfig, error)
    ListClusters() ([]string, error)
}
```

| Provider | Who ships it |
|---|---|
| `AWSSecretsManagerProvider` | Core |
| `KubernetesSecretProvider` | Core — static kubeconfigs, no cloud needed, enables local dev |
| `VaultProvider` | Community |
| `GCPSecretManagerProvider` | Community |
| `AzureKeyVaultProvider` | Community |

The `KubernetesSecretProvider` is critical — anyone can try Sharko without a cloud account. The interface is two methods. Trivial overhead vs hardcoding AWS.

---

## 4. How Sharko Works

### Bootstrap — One Manual Step

Like ArgoCD itself, the first install is manual:

```bash
helm install sharko oci://ghcr.io/your-org/sharko/charts/sharko \
  --namespace sharko \
  --set argocd.token=<argocd-account-token> \
  --set git.token=<github-token> \
  --set secretsProvider.type=aws-sm \
  --set secretsProvider.region=eu-west-1
```

After that, everything goes through the server.

### CLI Workflow

```bash
# Connect to the Sharko server
sharko login --server https://sharko.internal.company.com

# Initialize the addons repo (server creates it, pushes to Git, bootstraps ArgoCD)
sharko init

# Add an addon to the catalog
sharko add-addon cert-manager --chart jetstack/cert-manager --version 1.14.x

# Onboard a cluster (server fetches creds, registers in ArgoCD, commits to Git)
sharko add-cluster prod-eu --addons monitoring,logging,cert-manager

# Check fleet status
sharko status
```

Every command sends a request to the Sharko API. The server does the work — fetches credentials, registers in ArgoCD, creates values files, commits to Git, opens PRs. The user's laptop has zero credentials except their Sharko login.

### All Consumers, One API

```
Developer laptop:
  sharko CLI ---------> Sharko Server API

Backstage / Port.io:
  plugin -------------> Sharko Server API

Terraform / CI:
  curl / CLI ---------> Sharko Server API

Sharko Server (in-cluster):
  ├── UI (fleet dashboard, observability)
  ├── API (read + write endpoints)
  ├── Orchestrator (workflow engine)
  ├── ArgoCD client (account token auth)
  ├── Git client (configured token)
  └── Secrets provider (AWS IRSA, K8s Secrets, Vault)
```

### GitOps for All Changes

Every change Sharko makes is a Git operation — configurable via server-side Helm values:

```yaml
# Helm values (server config, not in the addons repo)
gitops:
  defaultMode: pr          # "pr" or "direct"
  branchPrefix: sharko/
  commitPrefix: "sharko:"
```

When a user runs `sharko add-cluster prod-eu`, the server:
1. Fetches credentials from the secrets provider
2. Verifies cluster connectivity
3. Registers the cluster in ArgoCD
4. Creates the values file
5. Commits to a branch, opens a PR (or direct commits to main)

The PR shows exactly what will change. A teammate reviews and approves. Merge → ArgoCD deploys addons.

### End-to-End Automation

```
Terraform creates EKS cluster
        ↓
CI pipeline: curl -X POST https://sharko.example.com/api/v1/clusters ...
        ↓  (or: sharko add-cluster prod-eu --addons monitoring,logging)
Sharko server fetches credentials, registers in ArgoCD, commits to Git
        ↓
ArgoCD picks up the new cluster, deploys all enabled addons
        ↓
Sharko UI shows the new cluster with real-time addon status
```

No human touches a YAML file. No credentials on laptops. One API call bridges "infrastructure exists" → "infrastructure has all its addons."

---

## 5. Why AppSet Templates Stay in Git

The current AppSet template (`addons-appset.yaml`) contains deeply custom, evolved production logic:

- Sync wave ordering (Istio install sequence, ESO priority)
- Multi-source applications (Datadog has 3 sources, ESO has 2)
- Dynamic log collection scope computed from enabled addons
- EKS Auto Mode conditionals
- Host cluster special-casing
- Per-addon `ignoreDifferences` and migration mode

This logic cannot — and should not — be generated by an operator or CLI. The AppSet template IS the GitOps pattern. It belongs in Git: reviewable, versionable, forkable, customizable.

**Sharko generates data files (values, config). It never touches AppSet template logic.**

Adding sync waves, multi-source apps, custom conditions — all safe. Sharko won't conflict. Changing the directory structure? Update the server's Helm values — repo paths and gitops preferences are all server-side configuration, not stored in the addons repo.

**The generated repo is plain GitOps YAML.** No Sharko-specific files live in the repo. If Sharko goes away, the repo still works — ArgoCD doesn't know or care about Sharko. Worst case: users manage YAML by hand, exactly like today.

---

## 6. The Coupling Contract

The AppSet templates and the `templates/` repo are one thing — the AppSet references specific paths (`configuration/addons-clusters-values/{{.name}}.yaml`). The single coupling point between the CLI/API and the GitOps repo is:

> **`cluster name` must match the values file name.**

Sharko creates `prod-eu.yaml` when you run `sharko add-cluster prod-eu`. The AppSet finds it via `{{.name}}`. That naming convention is the entire framework contract.

---

## 7. API & IDP Integration — The Full Picture

### Philosophy: The API IS the Product

Sharko is not a UI with an API bolted on. The API is the core product. Every consumer — the UI, the CLI, Backstage, Port, Terraform, CI/CD pipelines — talks to the same API. The UI is just one client among many.

This means the API must be comprehensive: it serves both **read operations** (observability, dashboards, fleet status) and **write operations** (register clusters, manage addons, update labels).

### Full API Surface

**Read Operations (observability — what the UI currently does):**

These endpoints already exist in the current `argocd-addons-platform` backend. They power the UI dashboard. In Sharko, they become first-class public API endpoints that any IDP can consume.

```
GET  /api/v1/clusters                    → list all managed clusters with health status
GET  /api/v1/clusters/:name              → cluster detail: addons, sync status, health
GET  /api/v1/clusters/:name/addons       → addons deployed on this cluster with versions
GET  /api/v1/addons                      → addon catalog: what's available, versions, descriptions
GET  /api/v1/addons/:name                → addon detail: which clusters have it, version spread
GET  /api/v1/addons/:name/drift          → version drift across clusters for this addon
GET  /api/v1/fleet/status                → fleet-wide overview: health, sync, drift summary
GET  /api/v1/fleet/version-matrix        → version matrix: addon × cluster grid
GET  /api/v1/connections                 → configured ArgoCD/Git connections
```

**Write Operations (management — new in Sharko):**

These are the new endpoints that enable cluster lifecycle management and IDP integration.

```
POST   /api/v1/clusters                  → register a new cluster
         Body: { name, secretsProvider, region, addons: {monitoring: true, ...} }
         Action: fetch credentials via provider, create ArgoCD cluster secret, create values file
DELETE /api/v1/clusters/:name            → deregister cluster from ArgoCD
PATCH  /api/v1/clusters/:name            → update cluster: change addon labels, metadata
         Body: { addons: {istio: true} }
         Action: update ArgoCD cluster secret labels → AppSet picks up the change
POST   /api/v1/clusters/:name/refresh    → re-fetch credentials from secrets provider
POST   /api/v1/addons                    → add addon to catalog and AppSet config
         Body: { name, chart, repo, version, namespace }
DELETE /api/v1/addons/:name              → remove addon from catalog
POST   /api/v1/init                      → generate a new addons repo structure (API equivalent of CLI init)
         Body: { repoUrl, secretsProvider, region }
```

**System/Config Operations:**

```
GET    /api/v1/config                    → current Sharko config (provider, region, paths)
GET    /api/v1/providers                 → available secrets providers and their status
POST   /api/v1/providers/test            → test connectivity to a secrets provider
GET    /api/v1/health                    → Sharko server health + ArgoCD connectivity
```

### Authentication

**Dual auth model:**
- **UI users:** session cookies (existing login flow with username/password)
- **CLI / API consumers:** Bearer tokens via `POST /api/v1/auth/login` (username/password → token, stored in `~/.sharko/config`)

The server middleware accepts both cookies and `Authorization: Bearer` headers on protected endpoints.

```bash
# CLI login — prompts for username/password, saves token
sharko login --server https://sharko.internal.company.com

# API access from CI/CD or IDP
curl -H "Authorization: Bearer <token>" https://sharko.example.com/api/v1/clusters
```

**v1.x:** API keys for non-interactive consumers (`sharko token create --name "backstage" --scope read,write`).

### Concrete IDP Integration Examples

**Backstage Plugin — what it would actually do:**

A Backstage plugin that shows Sharko fleet data in the Backstage catalog. Each Kubernetes cluster registered in Backstage gets a "Sharko" tab showing:
- Which addons are deployed on this cluster
- Health/sync status of each addon
- Version drift compared to fleet
- Button to enable/disable addons (calls PATCH /api/v1/clusters/:name)

The plugin makes GET requests to the Sharko API for read data and PATCH requests for management actions. Zero custom backend needed in Backstage — just API calls.

**Port.io Self-Service Action:**

A Port action called "Onboard Cluster to Sharko" that:
1. Takes cluster name + region as input from the developer
2. Calls `POST /api/v1/clusters` with the payload
3. Sharko fetches credentials, registers in ArgoCD, generates values file
4. The action returns success with a link to the Sharko UI cluster page

This turns cluster onboarding from "file a ticket to the platform team" into a 30-second self-service action.

**Terraform Integration:**

```hcl
# After creating an EKS cluster, register it with Sharko
resource "null_resource" "sharko_register" {
  depends_on = [module.eks]

  provisioner "local-exec" {
    command = <<-EOT
      curl -X POST https://sharko.example.com/api/v1/clusters \
        -H "Authorization: Bearer ${var.sharko_api_key}" \
        -d '{"name": "${module.eks.cluster_name}", "secretsProvider": "aws-sm", "region": "${var.region}", "addons": {"monitoring": true, "logging": true, "cert-manager": true}}'
    EOT
  }
}
```

Or via CLI in CI/CD (CLI talks to the server, not directly to ArgoCD/AWS):
```yaml
# GitHub Actions step after Terraform apply
- name: Register cluster with Sharko
  run: |
    sharko login --server https://sharko.example.com --token ${{ secrets.SHARKO_TOKEN }}
    sharko add-cluster ${{ env.CLUSTER_NAME }} --addons monitoring,logging,cert-manager
```

### Webhook/Event Support (v1.x)

For IDPs that want to react to Sharko events rather than poll:

```
POST /api/v1/webhooks                    → register a webhook
  Body: { url, events: ["cluster.registered", "cluster.degraded", "addon.drift"] }

Events emitted:
  cluster.registered    → new cluster added to fleet
  cluster.deregistered  → cluster removed
  cluster.degraded      → cluster health changed from healthy to degraded
  addon.drift           → version drift detected across fleet
  addon.sync.failed     → addon sync failure on a cluster
```

This enables Backstage/Port to show real-time notifications without polling, and Slack/PagerDuty integrations for alerting.

### CLI Command → API Mapping

The CLI is a thin client. Every command maps to an API call:

| CLI Command | API Endpoint |
|---|---|
| `sharko login` | `POST /api/v1/auth/login` |
| `sharko version` | `GET /api/v1/health` |
| `sharko init` | `POST /api/v1/init` |
| `sharko add-cluster <name>` | `POST /api/v1/clusters` |
| `sharko remove-cluster <name>` | `DELETE /api/v1/clusters/{name}` |
| `sharko update-cluster <name>` | `PATCH /api/v1/clusters/{name}` |
| `sharko list-clusters` | `GET /api/v1/clusters` |
| `sharko add-addon <name>` | `POST /api/v1/addons` |
| `sharko remove-addon <name>` | `DELETE /api/v1/addons/{name}` |
| `sharko status` | `GET /api/v1/fleet/status` |

The UI, Backstage, Port.io, Terraform, and CI/CD all use the same API endpoints. One API, many consumers.

---

## 8. Branding & Visual Identity

### Name
- **Sharko** — always capitalized as "Sharko" in prose, lowercase `sharko` in code/CLI/repo names
- The "-o" suffix mirrors ArgoCD intentionally — they are companion tools in the same ocean
- Tagline: *"Addon management for Kubernetes fleets, built on ArgoCD"*

### Logo
- **Shape:** Dual-tone shark dorsal fin. Reads as a fin cutting through water.
- **Colors:** Cyan (#00C9E0) front face + Deep blue (#1E62D0) back face. Dark navy background (#0B1426) optional.
- **Two versions:**
  - **Icon version** (no wave): clean fin silhouette on transparent background. Used for favicon, app icon, GitHub avatar, small sizes (16px–64px).
  - **Banner version** (with waterline wave): fin with a subtle wave line at the base. Used for README hero image, marketing, documentation headers, large display.
- **Logo files location in repo:** `assets/logo/`
  - `sharko-icon.svg` — master vector, transparent background
  - `sharko-icon-512.png` — GitHub README
  - `sharko-icon-256.png` — general use
  - `sharko-icon-64.png` — app icon
  - `sharko-icon-32.png` — small icon
  - `sharko.ico` — favicon for the UI
  - `sharko-banner.svg` — wave version for marketing
  - `sharko-banner-dark.png` — wave version on dark background

### Color Palette
- **Primary cyan:** #00C9E0 (the bright face of the fin)
- **Primary blue:** #1E62D0 (the shadow face of the fin)
- **Dark navy:** #0B1426 (background for dark mode / marketing)
- **White:** #FFFFFF (background for light mode)
- The UI currently uses a cyan accent that aligns with the logo — keep this consistent

### Brand Story (for README / landing page / conference talks)
ArgoCD is the octopus — many arms, reaching many clusters, delivering applications.
Sharko is the shark — fast, intelligent, always moving, managing the fleet of addons that ArgoCD delivers.
Two ocean predators, same ecosystem. ArgoCD handles delivery. Sharko handles what gets delivered and where.

---

## 9. Key Decisions & Rationale

These are the critical design decisions made during the brainstorming sessions and the reasoning behind each. A future AI session should understand not just WHAT was decided, but WHY — so it doesn't re-litigate settled questions.

### Why ArgoCD only (no Flux abstraction)
ArgoCD has ~60% adoption and won the GitOps delivery war. Abstracting over Flux would mean building for a minority with no ability to test. The delivery mechanism is a fixed dependency, not a pluggable interface. This is a deliberate decision, not a limitation.

### Why server-first, not standalone CLI
The server holds all credentials (ArgoCD token, Git token, AWS IRSA). Nobody stores credentials on their laptop. The CLI is a thin HTTP client — like `kubectl` to the API server. This is more secure (one place for secrets), simpler for users (one `sharko login` vs configuring ArgoCD + Git + AWS locally), and enables IDP integration (Backstage, Port, Terraform all call the same API). A Kubernetes operator (CRDs, reconcile loop) is v2 if adoption justifies it.

### Why ArgoCD auth via account token, not ServiceAccount
ArgoCD has its own account system and RBAC (`argocd-rbac-cm` ConfigMap). Sharko authenticates using an ArgoCD account token (Bearer auth), not Kubernetes ServiceAccount auth. This is how most tools integrate with ArgoCD — the token is stored in a K8s Secret, the Helm chart injects it, and ArgoCD's own RBAC controls permissions.

### Why the CLI should NOT generate ApplicationSets
The current AppSet template (`addons-appset.yaml` in `argocd-cluster-addons`) contains deeply evolved production logic: sync wave ordering for Istio, multi-source applications for Datadog (3 sources) and ESO (2 sources), dynamic `containerIncludeLogs` computation, EKS Auto Mode conditionals, host cluster special-casing, per-addon `ignoreDifferences`, and migration mode flags. This logic CANNOT be captured in a generic template generator. The AppSet template IS the GitOps pattern — it belongs in Git, owned by the user, evolving freely. The CLI only generates DATA files (values files, config entries). It never touches template LOGIC.

### Why one repo, not two or three
Early design had three repos (operator + addons + UI) then two (tool + addons). For a solo maintainer, multiple repos means multiple CI pipelines, release cycles, and READMEs that drift apart. The reference addons structure ships as `templates/` inside the Sharko binary — like how `helm create` bundles its chart template. One repo, one release, one place to star and discover.

### Why the repo structure isn't a real limitation
Sharko generates a specific directory structure (`configuration/addons-clusters-values/`, etc.). If users change this structure, Sharko breaks. But: (1) repo paths are configurable via Helm values (`sharko.repo.paths.*`), (2) the generated repo is plain GitOps YAML that ArgoCD manages independently — Sharko is not required for the repo to work, (3) most customization is to template LOGIC not directory STRUCTURE, which is safe.

### Why no auto-rollback of ArgoCD state
Write operations like `RegisterCluster` involve multiple steps (fetch creds → register in ArgoCD → commit to Git). If a late step fails (e.g., Git push) after ArgoCD registration succeeds, auto-deregistering the cluster could trigger cascade deletion of addons ArgoCD already started deploying. Instead, Sharko returns a partial success: "Cluster registered in ArgoCD but Git commit failed. Run `sharko remove-cluster` to clean up, or retry." Partial success is safer than automatic cleanup.

### Why the provider interface is worth it even without community providers
The `KubernetesSecretProvider` (static kubeconfigs in K8s Secrets) alone justifies the interface. It means anyone can try Sharko without a cloud account — just put kubeconfigs in K8s Secrets manually. The interface is two methods (`GetCredentials`, `ListClusters`). Trivial overhead vs hardcoding AWS. It changes the README from "requires AWS" to "supports AWS, Vault, GCP, Azure."

### Why the coupling contract is one simple rule
The ONLY coupling point between Sharko (CLI/API) and the GitOps repo is: **cluster name must match the values file name**. `sharko add-cluster prod-eu` creates `configuration/addons-clusters-values/prod-eu.yaml`. The AppSet finds it via `{{.name}}`. This convention is the entire framework contract. Everything else is flexible.

### The current solution is a "blueprint" not a "framework"
Today's `argocd-cluster-addons` + `argocd-addons-platform` is an opinionated reference implementation tightly coupled to AWS EKS + Secrets Manager + ESO + ArgoCD + Helm. That's a blueprint. Sharko evolves it toward a framework by making the secrets backend pluggable and adding CLI/API tooling. But the word "blueprint" is accurate and respected — AWS EKS Blueprints is literally named that.

### GitOps protection for write operations
In the API/operator model, the concern was "what if someone accidentally deletes monitoring from a cluster via kubectl apply?" The answer: ManagedCluster CRDs (operator v2) would live in Git, applied by ArgoCD, protected by ValidatingAdmissionWebhook that blocks direct kubectl writes. Three layers: Kubernetes RBAC (only ArgoCD service account can write), webhook (rejects non-ArgoCD requests with clear message), ArgoCD self-healing (reverts drift). This is stronger than Git branch protection alone — it's structural, not procedural.

---

## 10. Challenges

| Challenge | Severity | Mitigation |
|-----------|----------|------------|
| Credential rotation | High | `POST /api/v1/clusters/{name}/refresh` re-fetches from provider. Operator (v2) can auto-rotate on a schedule |
| Multi-step operation failures | High | Partial success responses. No auto-rollback of ArgoCD state. Human decides cleanup vs retry |
| ArgoCD write permissions | Medium | ArgoCD account token needs cluster admin role. Document in setup guide |
| AppSet template customization | N/A | Sharko never touches templates. Users own all AppSet logic |
| Repo structure changes | Medium | Repo paths configurable via Helm values. Generated repo works without Sharko |
| Provider interface versioning | Medium | Strict semver. Two methods only — minimal surface |
| Community provider quality | Medium | Providers are separate repos. Core team owns the interface |
| Production data in templates | Medium | `templates/starter/` is clean scaffold. Full `templates/` is reference only, stripped before public release |
| Audience size | Reality check | Hundreds, not thousands. Fine for portfolio + genuine niche value |

---

## 11. Roadmap

### v0.1.0 — Clean Foundation

Strip dead code (migration system, Datadog client, GPTeal), rename module path, cobra entry point, rebrand everything (env vars, UI, Helm chart). Same functionality, new identity.

### v1.0.0 — The Product

- API contract document (designed and reviewed before implementation)
- Provider interface (`internal/providers/`) — AWS Secrets Manager + K8s Secrets
- Orchestrator (`internal/orchestrator/`) — workflow engine with partial success handling
- Write API endpoints — register/deregister clusters, manage addons, init repo
- Dual auth — session cookies for UI, Bearer tokens for CLI
- CLI thin client — `sharko login`, `init`, `add-cluster`, `add-addon`, `status`, `version`
- Templates cleanup — `templates/starter/` embedded in binary
- AI assistant rebranded (OpenAI, Ollama, generic OpenAI-compatible providers)

### v1.x — Enhancements

- API keys for non-interactive consumers (`sharko token create`)
- Webhook/event support (cluster.registered, addon.drift, etc.)
- Async operations for batch workflows (202 + job polling)
- UI write features (register cluster, manage addons from dashboard)

### V2 — Kubernetes Operator (if adoption justifies)

- `SharkoConfig` CRD — global configuration
- `ManagedCluster` CRD — per-cluster lifecycle with status reporting
- Continuous credential rotation via reconcile loop
- ValidatingAdmissionWebhook for GitOps-only enforcement

### Tech Stack

```
Go                   → backend language
cobra                → CLI framework
net/http ServeMux    → HTTP router (Go 1.22+ pattern matching, no third-party router)
ArgoCD REST API      → cluster registration, status queries (account token auth)
AWS SDK Go v2        → AWSSecretsManagerProvider
k8s.io/client-go     → KubernetesSecretProvider, config stores
kubebuilder          → operator scaffolding (v2 only)
```

---

## 12. Product Positioning

> **Sharko** is an addon management server for Kubernetes fleets, built on ArgoCD.
> API, UI, and CLI — with pluggable secrets backends and complete GitOps workflow orchestration.

For the portfolio:

> Designed a GitOps blueprint for managing 50+ EKS clusters from a single config file.
> Built Sharko — a server-first addon management platform with REST API, fleet dashboard,
> thin CLI client, pluggable provider interface, and workflow orchestration engine
> for fleet-scale addon management on ArgoCD.

---

## Summary

| Question | Answer |
|---|---|
| What is this today? | An opinionated GitOps blueprint for AWS EKS + ArgoCD + ESO |
| What's the differentiator? | The UI (drift detection + version matrix) + the API (complete workflow orchestration) |
| What gap exists? | No pluggable secrets backend for addon fleet management |
| What's v0.1.0? | Rebranded product. Same functionality, new identity |
| What's v1.0.0? | Server with write API, provider interface, orchestrator, CLI thin client |
| What's v2? | Kubernetes operator with CRDs. If adoption demands it |
| Architecture? | Server-first. CLI is a thin HTTP client. Like ArgoCD's own architecture |
| How many repos? | One: `sharko` (server + UI + CLI + bundled templates) |
| What stays in Git? | AppSet templates — the evolved production logic. Sharko never touches them |
| What becomes pluggable? | Secrets/credentials backend via provider interface |
| Who's the audience? | ArgoCD teams managing multiple clusters who want structure and observability |
