# Sharko

<p align="center">
  <img src="assets/sharko-mascot.png" alt="Sharko mascot" width="240">
</p>

<p align="center"><em>Declarative addon management for Kubernetes clusters, built on ArgoCD.</em></p>

Sharko runs in-cluster and manages the full lifecycle of addons (cert-manager, monitoring, logging, and more) across your entire fleet — from a single dashboard, CLI, or REST API. Every change goes through a Git PR, so fleet state is always auditable and version-controlled.

<div class="grid cards" markdown>

-   :material-rocket-launch:{ .lg .middle } **Getting Started**

    ---

    Install Sharko and register your first cluster in 5 minutes.

    [:octicons-arrow-right-24: Quickstart](getting-started/quickstart.md)

-   :material-book-open:{ .lg .middle } **User Guide**

    ---

    Day-to-day operations: clusters, addons, values, upgrades.

    [:octicons-arrow-right-24: Read the guide](user-guide/connections.md)

-   :material-tools:{ .lg .middle } **Operator Manual**

    ---

    Install, configure, secure, and troubleshoot a Sharko deployment.

    [:octicons-arrow-right-24: Operator docs](operator/installation.md)

-   :material-console:{ .lg .middle } **API Reference**

    ---

    Swagger-generated endpoint docs for every tier.

    [:octicons-arrow-right-24: API docs](api/overview.md)

</div>

## Key Features

- **Wizard-based setup** — guided first-run configures Git, ArgoCD, secrets provider, and initializes your repo
- **Fleet dashboard** — cluster health cards, addon version matrix, drift detection
- **Managed vs discovered clusters** — adopt existing ArgoCD clusters into Sharko management in one click
- **GitOps-native** — all write operations create PRs; auto-merge optional
- **Unified API** — CLI, UI, Backstage, Terraform, and CI/CD all use the same REST API
- **Secrets management** — deliver credentials to remote clusters (AWS SM or Kubernetes Secrets, no ESO)
- **AI assistant** — context-aware troubleshooting with OpenAI, Claude, Gemini, or Ollama
- **API keys** — long-lived tokens for non-interactive consumers

## Try the Demo

No cluster required — mock backends simulate ArgoCD, Git, and secrets providers:

```bash
git clone https://github.com/MoranWeissman/sharko.git
cd sharko
make demo
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin`.
