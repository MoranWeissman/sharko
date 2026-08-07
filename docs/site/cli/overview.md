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

You will be prompted for username and password. The CLI stores the server URL and session token locally in `~/.sharko/config` (a plain YAML file, no extension — not `config.yaml`). Session tokens expire; re-run `sharko login` when prompted.

`sharko login` always exchanges a username and password for a session token — there is no CLI flow that trades an API key for one. For non-interactive use (CI/CD), create an API key with `sharko token create` (see [Commands](commands.md#sharko-token-create)) and write it straight into the config file the CLI reads, instead of running `login`:

```bash
mkdir -p ~/.sharko
cat > ~/.sharko/config <<EOF
server: https://sharko.your-domain.com
token: sharko_a1b2c3d4...
EOF
```

Every command reads the token from that file — there is no `SHARKO_TOKEN` environment variable read at request time. `SHARKO_CONFIG_DIR` overrides where the CLI looks for it (`~/.sharko` is the default), which is useful for pointing a CI job at a config file it wrote to a different path.

## Global Flags

Every command inherits these two persistent flags from the root command:

| Flag | Description |
|------|-------------|
| `--server <url>` | Override the server URL from the saved config for this call |
| `--insecure` | Skip TLS certificate verification (for self-signed certs) |
| `--help` | Show help for any command |

There is no `--token` flag and no `--output json` flag. The token always comes from the saved config (`sharko login` writes it), and every command prints plain text — there is no JSON output mode.

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
