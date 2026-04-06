# Sharko

Sharko is an addon management server for Kubernetes clusters, built on ArgoCD. It runs in-cluster and manages the full lifecycle of addons (cert-manager, monitoring, logging, and more) across your entire fleet — from a single dashboard, CLI, or REST API.

Every change Sharko makes goes through a Git PR, so your fleet state is always auditable and version-controlled.

## Key Features

- **Fleet dashboard** — cluster health cards, addon version matrix, drift detection highlights
- **GitOps-native** — all write operations create PRs; auto-merge optional
- **Unified API** — CLI, UI, Backstage, Terraform, and CI/CD all use the same REST API
- **Addon catalog** — add, remove, and upgrade addons globally or per-cluster
- **Secrets management** — deliver API keys and credentials to remote clusters
- **AI assistant** — context-aware troubleshooting with OpenAI, Claude, Gemini, or Ollama
- **API keys** — long-lived tokens for non-interactive consumers
- **Multi-provider** — AWS Secrets Manager or Kubernetes Secrets as the credentials backend

## Quick Links

<div class="grid cards" markdown>

- :material-rocket-launch: **[Quick Start](getting-started/quickstart.md)** — up and running in 5 minutes
- :material-book-open: **[User Guide](user-guide/connections.md)** — day-to-day operations
- :material-server: **[Operator Manual](operator/installation.md)** — install, configure, secure
- :material-console: **[CLI Reference](cli/commands.md)** — all commands and flags
- :material-api: **[API Reference](api/overview.md)** — endpoints, auth, schemas

</div>

## Try the Demo

No cluster required — mock backends simulate ArgoCD, Git, and secrets providers:

```bash
git clone https://github.com/MoranWeissman/sharko.git
cd sharko
make demo
```

Open [http://localhost:8080](http://localhost:8080) and log in with `admin` / `admin`.
