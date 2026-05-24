# YAML Schema Migration (V1.25)

Sharko v1.25 introduces a self-describing schema envelope for the two YAML
files Sharko writes into your bootstrap repo:

- `managed-clusters.yaml` (registry of clusters Sharko manages)
- `addons-catalog.yaml`   (catalog of addons Sharko can deploy)

This page is a runbook: what changed, why, what it means for you, and how
to get inline schema validation in your editor.

If you only operate Sharko (don't edit the YAML by hand), the headline is:

> **You don't have to do anything.** First write after upgrading to v1.25
> emits the new shape. The reader still understands the old shape. No
> manual migration. No outage.

The rest of this page is for operators who edit the YAML by hand, want to
adopt the new shape immediately, or want editor-side validation while
authoring config.

---

## What changed

### 1. Envelope shape

Both `managed-clusters.yaml` and `addons-catalog.yaml` now wrap their
content in a CRD-shaped envelope:

```yaml
# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
apiVersion: sharko.io/v1
kind: ManagedClusters
metadata:
  name: managed-clusters
spec:
  clusters: []
```

```yaml
# yaml-language-server: $schema=https://sharko.io/schemas/addons-catalog.v1.json
apiVersion: sharko.io/v1
kind: AddonCatalog
metadata:
  name: addon-catalog
spec:
  applicationsets: []
```

The envelope mirrors Kubernetes resource conventions
(`apiVersion` / `kind` / `metadata` / `spec`) so future graduation to
operator mode (V3+) is mechanical, not architectural. Sharko remains a
plain Go service — there is no CRD installation, no webhook, no operator
required in v1.25. The shape is a contract, not a deployment dependency.

### 2. Schema header

The first non-comment line of every Sharko-written YAML file is a
`yaml-language-server` directive pointing at a stable schema URL:

```yaml
# yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
```

Editors that understand this comment (VS Code with the Red Hat YAML
extension, IntelliJ / GoLand built-in YAML, Neovim with `coc-yaml` or
`nvim-lspconfig` + `yaml-language-server`) pick up the schema
automatically and give you:

- inline error markers on schema violations,
- field-level auto-complete,
- hover docs sourced from the schema's `description` fields.

No setup beyond installing the extension. See [Editor setup](#editor-setup)
below for the per-editor walk-through.

---

## Why

The motivation is a single bullet:

> **Operational safety prep for V125-1-8.**

V125-1-8 introduces a goroutine-based cluster reconciler that
continuously converges ArgoCD cluster Secret state to whatever
`managed-clusters.yaml` says. A reconciler reading a malformed YAML file
is the operational equivalent of pointing a loaded gun at production:
either it silently skips entries (clusters quietly disappear from ArgoCD)
or it crashloops on every reconcile tick.

Locking the file shape behind a JSON Schema means the reconciler can
fail fast — and loudly — on bad input rather than silently
under-reconciling. The validation also runs on the CI side of every
PR that edits these files (see [CI validation](#ci-validation) below) so
the bad YAML never reaches the reconciler in the first place.

The schema-envelope work is intentionally landing *before* the
reconciler — V125-1-9 is the operational-safety prerequisite for
V125-1-8.

---

## User impact

Five guarantees, in priority order:

1. **No manual migration required.** The reader accepts both old (no
   envelope) and new (enveloped) shapes. Existing bootstrap repos
   continue to work after the upgrade with zero edits.
2. **First write after upgrade rewrites the file in the new shape.** As
   soon as Sharko makes a change to your config (a new cluster added
   via UI, a new addon installed via Marketplace, etc.) the file is
   rewritten with the envelope + schema header. There is no
   intermediate state where the file is half-converted.
3. **No downtime, no restart loop.** Both shapes work; both are
   validated by the same reader; both result in the same in-memory
   state.
4. **Reads in the server hot path are schema-validated.** Sharko's
   reader code paths (loaders for `managed-clusters.yaml`,
   `addons-catalog.yaml`, and per-cluster addons overrides) all run the
   embedded JSON Schema validator on every load and reject malformed
   files with a structured error rather than logging-and-continuing.
   This is invisible to you when the YAML is valid; it surfaces a clear
   error in the API response when it isn't.
5. **The reconciler integration is not in this sprint.** V125-1-9 ships
   the schema, validators, and CLI/CI hooks. The reconciler that
   *consumes* the validated YAML lands in the next sprint (V125-1-8).

If you're authoring YAML by hand and want the new shape immediately,
see [Manual migration](#manual-migration) below.

---

## Deprecation timeline

| Version | What happens |
|---------|--------------|
| **v1.25** (current) | Both shapes work. New writes emit the envelope shape. |
| **v1.26** (planned) | Legacy reader removed — files MUST have the envelope. Sharko reads of legacy-shape files will fail at startup with a "migrate to v1.25 envelope" error pointing back at this page. |
| **v2.x+** | Operator-mode graduation (CRD-backed) reuses the same `sharko.io/v1` envelope. Bootstrap repos that adopted the envelope in v1.25 are forward-compatible to operator mode without further edits. |

Cutover plan: before upgrading to v1.26, run `sharko validate-config` (or
a manual `sharko serve` smoke test) against your bootstrap repo to
confirm every file already has the envelope. If a file is still in the
legacy shape, edit it in-place via UI or CLI (any write triggers the
rewrite) before the upgrade.

---

## Editor setup

The `yaml-language-server` directive in the file header tells your
editor where to fetch the schema. Configuration on your side is
one-time-per-machine.

### VS Code

1. Install the Red Hat YAML extension
   (publisher: `redhat.vscode-yaml`).
2. Open `managed-clusters.yaml` or `addons-catalog.yaml`.
3. The extension reads the `# yaml-language-server: $schema=...` line on
   open and fetches the schema. Subsequent opens hit a local cache.

If validation isn't lighting up, check `Settings > YAML > Schemas` —
the schema URL should appear in the active-schemas list when the file
is focused. The Red Hat extension's "YAML: Get JSON Schema" command
also dumps which schema (if any) is currently associated with the
active file.

### JetBrains (IntelliJ / GoLand / WebStorm / PyCharm)

The bundled YAML support reads `yaml-language-server` directives
out-of-the-box (since 2021.2). No plugin install required.

If you're on an older JetBrains version, install the **JSON Schema**
plugin and add a manual mapping under
`Preferences > Languages & Frameworks > Schemas and DTDs > JSON Schema
Mappings`:

- Schema URL: `https://sharko.io/schemas/managed-clusters.v1.json`
- File pattern: `managed-clusters.yaml`

(Repeat with `addons-catalog.v1.json` and `addons-catalog.yaml`.)

### Neovim

Use `nvim-lspconfig` with `yamlls`. The schema-from-modeline behaviour
is on by default in recent `yaml-language-server` releases; no extra
config is required if the file has the header.

For explicit mapping (older `yamlls`):

```lua
require('lspconfig').yamlls.setup {
  settings = {
    yaml = {
      schemas = {
        ["https://sharko.io/schemas/managed-clusters.v1.json"] = "**/managed-clusters.yaml",
        ["https://sharko.io/schemas/addons-catalog.v1.json"]   = "**/addons-catalog.yaml",
      },
    },
  },
}
```

### Other editors

Any editor whose YAML LSP integration is built on top of
`yaml-language-server` (Sublime, Helix, etc.) honours the modeline
directive. If your editor uses a different YAML LSP, point it at the
schema URLs above explicitly.

---

## Manual migration

If you'd rather not wait for the first Sharko-side write to flip the
shape, you can edit the files by hand. The procedure for each file:

1. **Add the schema header** as the first line (no blank line above it):

    ```yaml
    # yaml-language-server: $schema=https://sharko.io/schemas/managed-clusters.v1.json
    ```

2. **Wrap the existing content** in the envelope:

    ```yaml
    apiVersion: sharko.io/v1
    kind: ManagedClusters       # or AddonCatalog
    metadata:
      name: managed-clusters    # or addon-catalog
    spec:
      <previous top-level keys go under spec>
    ```

   For `managed-clusters.yaml`: the old top-level `clusters:` list moves
   verbatim under `spec.clusters:`.

   For `addons-catalog.yaml`: the old top-level `applicationsets:` list
   moves verbatim under `spec.applicationsets:`.

3. **Validate locally** with the CLI:

    ```bash
    sharko validate-config <path-to-your-bootstrap-repo>
    ```

   `validate-config` walks the directory recursively, picks up every
   `*.yaml` / `*.yml` file, validates the Sharko-enveloped ones against
   the embedded schemas, and skips the rest. Exit code is `0` on
   success, `1` on any schema failure. See [Quick reference](#quick-reference)
   below.

4. **Commit + push.** The new shape is identical in semantics to the
   old one, so Sharko picks it up on the next reconcile / refresh
   cycle without any restart.

If you have a worked example: the bootstrap templates that ship with
v1.25 are the canonical reference shape — see
`templates/bootstrap/configuration/managed-clusters.yaml` and
`templates/bootstrap/configuration/addons-catalog.yaml` in the Sharko
repo.

---

## CI validation

`.github/workflows/ci.yml` runs a `validate-sharko-config` job on every
PR that touches `*.yaml` / `*.yml` files. The job builds the Sharko
CLI, runs `sharko validate-config` against changed files, and fails the
PR if any Sharko-enveloped file violates its schema. PRs that change
only non-Sharko YAML (workflows, Helm chart templates, kind configs,
fixture YAML for other tools) pass through untouched because
`validate-config` skips files whose `apiVersion` is not
`sharko.io/v1`.

If your CI job fails on a YAML you didn't expect to be validated,
double-check the top-level `apiVersion` field — the schema picker keys
off it, not off the filename.

---

## Quick reference

`sharko validate-config` is the single tool you need for any YAML schema
question. Full documentation lives in
[CLI Reference → Commands → `sharko validate-config`](../cli/commands.md#sharko-validate-config).

The one-paragraph synopsis: `sharko validate-config <path>` accepts
either a single file or a directory. With a directory it walks
recursively, validates every `*.yaml`/`*.yml` file whose `apiVersion`
is `sharko.io/v1` against the matching embedded schema, and skips the
rest. Exit code `0` = clean (or only non-Sharko files), `1` = at least
one Sharko-enveloped file failed. `--quiet` suppresses per-file pass
lines and shows only failures + the summary count.

---

## Related reading

- [CLI Reference → `sharko validate-config`](../cli/commands.md#sharko-validate-config) —
  the full CLI flag + output reference.
- Architecture Decision: schema envelope —
  the design doc that motivated this work (`docs/design/2026-05-12-v125-architectural-todos.md`
  §4 in the Sharko repo covers the V125-1-9 scope and shipped state).
  Not part of the published docs site.
- Source of truth schemas:
    - [`managed-clusters.v1.json`](https://sharko.io/schemas/managed-clusters.v1.json)
    - [`addons-catalog.v1.json`](https://sharko.io/schemas/addons-catalog.v1.json)
