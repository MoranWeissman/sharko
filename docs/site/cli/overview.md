# CLI Overview

The Sharko CLI (`sharko`) is a thin HTTP client for the Sharko API — similar to how `kubectl` talks to the Kubernetes API server or `argocd` CLI talks to the ArgoCD server.

## Design Philosophy

- **No credentials on developer laptops** — the CLI authenticates to the Sharko server, which holds all platform credentials (ArgoCD token, Git token, secrets provider access)
- **One login** — `sharko login` replaces configuring ArgoCD + Git + AWS locally
- **Same API as the UI** — every CLI command calls the same REST endpoint the UI uses

## Installation

The CLI is published as a signed archive on each GitHub release, and that is the
only supported way to get it. There is no Homebrew formula, and the Go module
proxy has no v4 of this module, so the Go toolchain cannot fetch a current
version either.

Every archive filename carries its version — `sharko_4.0.1_linux_amd64.tar.gz`
and so on — so the commands below build the name from the tag you set. Four
platforms are published: `darwin` and `linux`, each for `amd64` and `arm64`.

=== "macOS"

    ```bash
    TAG=v4.0.1        # or a later tag from the releases page
    ARCH=arm64        # arm64 for Apple Silicon, amd64 for Intel Macs
    ARCHIVE=sharko_${TAG#v}_darwin_${ARCH}.tar.gz
    BASE=https://github.com/MoranWeissman/sharko/releases/download/${TAG}

    mkdir -p /tmp/sharko-install && cd /tmp/sharko-install
    curl -fLO ${BASE}/${ARCHIVE}
    curl -fLO ${BASE}/checksums.txt
    shasum -a 256 --check --ignore-missing checksums.txt
    tar xzf ${ARCHIVE}
    sudo mv sharko /usr/local/bin/
    ```

=== "Linux"

    ```bash
    TAG=v4.0.1        # or a later tag from the releases page
    ARCH=amd64        # amd64 for Intel and AMD, arm64 for Graviton and other ARM
    ARCHIVE=sharko_${TAG#v}_linux_${ARCH}.tar.gz
    BASE=https://github.com/MoranWeissman/sharko/releases/download/${TAG}

    mkdir -p /tmp/sharko-install && cd /tmp/sharko-install
    curl -fLO ${BASE}/${ARCHIVE}
    curl -fLO ${BASE}/checksums.txt
    sha256sum -c --ignore-missing checksums.txt
    tar xzf ${ARCHIVE}
    sudo mv sharko /usr/local/bin/
    ```

A few things those commands are doing on purpose:

- `TAG` must be `v4.0.1` or later. Earlier release lines are retired and
  unsupported — see
  [SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md#why-v300-is-retired).
  The [releases page](https://github.com/MoranWeissman/sharko/releases) shows the
  current tag.
- `curl -f` makes a wrong tag or filename fail with a non-zero exit instead of
  quietly saving GitHub's 404 page as if it were the archive.
- The archive holds `README.md`, `LICENSE` and `CHANGELOG.md` next to the binary,
  which is why it is unpacked in a scratch directory rather than wherever you
  happened to be standing.
- `checksums.txt` covers every archive in the release, so the check needs
  `--ignore-missing` to pass when you only downloaded one of them.

Each archive also has a detached cosign signature (`.sig`) and certificate
(`.pem`) published beside it. Checking the signature as well as the checksum is
described in [Supply chain](../operator/supply-chain.md).

Verify installation:

```bash
sharko version
```

The first line reports the CLI's own version and must read `4.0.1` or later:

```
Sharko CLI: 4.0.1
```

The second line reports the server from your saved config, and says it is
unreachable until you log in.

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
