# CLI Overview

The Sharko CLI (`sharko`) is a thin HTTP client for the Sharko API — similar to how `kubectl` talks to the Kubernetes API server or `argocd` CLI talks to the ArgoCD server.

## Design Philosophy

- **No credentials on developer laptops** — the CLI authenticates to the Sharko server, which holds all platform credentials (ArgoCD token, Git token, secrets provider access)
- **One login** — `sharko login` replaces configuring ArgoCD + Git + AWS locally
- **Same API as the UI** — every CLI command calls the same REST endpoint the UI uses

## Installation

=== "macOS (Homebrew)"

    ```bash
    brew install moranweissman/tap/sharko
    ```

=== "Linux / Manual"

    Download the binary from [GitHub Releases](https://github.com/MoranWeissman/sharko/releases) and place it on your `PATH`:

    ```bash
    curl -L https://github.com/MoranWeissman/sharko/releases/latest/download/sharko_linux_amd64.tar.gz | tar xz
    sudo mv sharko /usr/local/bin/
    ```

=== "Go Install"

    ```bash
    go install github.com/MoranWeissman/sharko/cmd/sharko@latest
    ```

Verify installation:

```bash
sharko version
```

## Authentication

Log in once per server:

```bash
sharko login --server https://sharko.your-domain.com
```

You will be prompted for username and password. The CLI stores a session token locally (in `~/.sharko/config.yaml`). Session tokens expire; re-run `sharko login` when prompted.

For non-interactive use (CI/CD), use an API key:

```bash
export SHARKO_TOKEN=sharko_a1b2c3d4...
sharko status  # token read from env var
```

## Global Flags

| Flag | Description |
|------|-------------|
| `--server <url>` | Override the server URL |
| `--token <token>` | Override the auth token |
| `--output json` | Output as JSON (useful for scripting) |
| `--help` | Show help for any command |

## Usage Pattern

All commands follow the same pattern:

```
sharko <verb>-<noun> [name] [flags]
```

Examples:

```bash
sharko add-cluster my-cluster --addons cert-manager
sharko remove-addon cert-manager --confirm
sharko upgrade-addon ingress-nginx --version 4.9.0
sharko token create --name ci --role viewer
```

See [Commands](commands.md) for the full reference.
