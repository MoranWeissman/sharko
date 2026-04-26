---
story_key: V123-2-5-release-pipeline-sign-embedded-catalog-entries
epic: V123-2 (Per-entry cosign signing)
status: review
effort: L
dispatched: 2026-04-26
merged: TBD
---

# Story V123-2.5 — Release pipeline signs embedded catalog entries

> **Effort revised from M → L** in this brief. The literal AC ("a v1.23 Sharko
> loading the released binary / Then embedded entries load `verified: true`")
> requires the signed YAML to be embedded BEFORE binary build, which means
> orchestrating the workflow so signing runs first and downstream build jobs
> consume the signed YAML. That's not a one-job add — it restructures the
> release flow.

## Brief (from epics-v1.23.md §V123-2.5)

As the **Sharko maintainer**, I want the release workflow to sign every
embedded catalog entry using keyless OIDC, so that downstream consumers of
our catalog can verify provenance.

## Acceptance criteria

**Given** `.github/workflows/release.yml` is updated with a new
`sign-catalog-entries` job running BEFORE the binary builds
**When** a tag is pushed
**Then** the job iterates every entry in `catalog/addons.yaml`, signs each
using cosign keyless (GitHub OIDC), and:
- Mutates the on-disk `catalog/addons.yaml` to add
  `signature: {bundle: <release-asset-URL>}` to every entry.
- Emits `<entry-name>.bundle` files for each entry as workflow artifacts.
- Downstream `build-and-push` + `goreleaser` jobs download the artifact and
  use the signed YAML when embedding the catalog into binaries.

**Given** the release is published
**Then** the `.bundle` files are attached as release assets at
`https://github.com/MoranWeissman/sharko/releases/download/<tag>/<entry-name>.bundle`
(the URL the signed YAML's `signature.bundle` points to).

**Given** a v1.23 Sharko loading the released binary
**Then** embedded entries load `verified: true` against the default trust
policy (which whitelists Sharko's own release workflow per V123-2.3).

**Given** a v1.21 / v1.22 Sharko reading the v1.23 release binary's embedded
catalog
**Then** the `signature` field is tolerated as unknown; no runtime error
(forward-compat guarantee from V123-2.1).

**Given** PR builds (non-tag pushes)
**Then** the signing job does NOT run; the unsigned `catalog/addons.yaml` is
embedded as today.

## Design constraints

### Canonical bytes contract is load-bearing

Signing and verification MUST compute the same canonical bytes per entry. If
they drift, every signature is a mismatch.

The verifier (V123-2.2) uses
`internal/catalog/signing/canonical.go::CanonicalEntryBytes`. The signing
tool MUST call the same Go function — not reimplement canonicalization in
Bash, jq, or yq.

→ **New Go binary** `cmd/catalog-sign/main.go` that imports
`internal/catalog/signing` and produces canonical bytes via the same code path.

### NFR-V123-6 applies to the runtime verifier, not the release pipeline

The "no `cosign` CLI shell-out" rule is for the runtime verifier inside the
Sharko binary (V123-2.2). The release pipeline can absolutely use the
`cosign` CLI for `sign-blob` — that's standard sigstore practice and what
the existing V121-8.1 image-signing step already does.

### Release-asset URL is deterministic

Even though the release isn't created until after the build completes, the
URL pattern is known:

```
https://github.com/MoranWeissman/sharko/releases/download/<TAG>/<entry-name>.bundle
```

The signing tool stamps these URLs into the YAML before they exist. By the
time anyone fetches them (verifier reads the embedded YAML's
`signature.bundle` URL), the release is published and the assets are live.

### What lands where

| Artifact                        | Location                                                         | Audience                          |
|---------------------------------|------------------------------------------------------------------|-----------------------------------|
| Signed `addons.yaml` (in-tree)  | Embedded into binaries by `//go:embed addons.yaml`               | Sharko at runtime                 |
| Per-entry `.bundle` files       | Release assets (`.../releases/download/<tag>/<name>.bundle`)     | Verifier fetches at load time     |
| Original (unsigned) git tree    | `catalog/addons.yaml` in main, never mutated by the release      | Repo / CI dev builds              |

The on-disk mutation in CI is **transient** — confined to the GitHub Actions
runner workspace; never committed back to git.

## Implementation plan

### 1. New Go tool `cmd/catalog-sign/main.go`

```go
// Command catalog-sign signs each entry in catalog/addons.yaml using cosign
// keyless (sign-blob) and emits:
//   - <out>/<entry-name>.bundle      — Sigstore bundle per entry
//   - <out>/addons.yaml.signed       — original YAML with `signature:` added
//
// Used only by the release workflow (V123-2.5). Calls the same
// signing.CanonicalEntryBytes function the runtime verifier uses, so signing
// and verification agree on the message bytes.
package main

import (
    "flag"
    "fmt"
    "os"
    "os/exec"
    "path/filepath"

    catalogembed "github.com/MoranWeissman/sharko/catalog"
    "github.com/MoranWeissman/sharko/internal/catalog"
    "github.com/MoranWeissman/sharko/internal/catalog/signing"
    "gopkg.in/yaml.v3"
)

func main() {
    var (
        outDir         = flag.String("out", "_dist/catalog", "output directory")
        releaseBaseURL = flag.String("release-base-url", "", "base URL for release assets (e.g., https://github.com/.../releases/download/v1.2.3)")
    )
    flag.Parse()

    if *releaseBaseURL == "" {
        die("--release-base-url is required")
    }
    if err := os.MkdirAll(*outDir, 0o755); err != nil { die("mkdir: %v", err) }

    // Load via the canonical loader so we get the same []CatalogEntry the
    // runtime sees (same validation, same field ordering).
    cat, err := catalog.LoadBytes(catalogembed.AddonsYAML())
    if err != nil { die("load catalog: %v", err) }

    // For each entry: canonical bytes → cosign sign-blob → bundle.
    // Mutate a fresh copy (don't touch the loaded *Catalog).
    var raw struct {
        Addons []catalog.CatalogEntry `yaml:"addons"`
    }
    if err := yaml.Unmarshal(catalogembed.AddonsYAML(), &raw); err != nil {
        die("re-unmarshal: %v", err)
    }
    for i := range raw.Addons {
        e := raw.Addons[i]
        canonical, err := signing.CanonicalEntryBytes(e)
        if err != nil { die("canonical %s: %v", e.Name, err) }

        payloadPath := filepath.Join(*outDir, e.Name+".payload")
        bundlePath := filepath.Join(*outDir, e.Name+".bundle")
        if err := os.WriteFile(payloadPath, canonical, 0o644); err != nil {
            die("write payload %s: %v", e.Name, err)
        }
        // cosign sign-blob keyless. CLI use here is expected per V123-2.5
        // — only the runtime verifier is forbidden from shelling out.
        cmd := exec.Command("cosign", "sign-blob",
            "--yes",
            "--bundle", bundlePath,
            "--output-signature", filepath.Join(*outDir, e.Name+".sig"),
            "--output-certificate", filepath.Join(*outDir, e.Name+".pem"),
            payloadPath,
        )
        cmd.Stdout = os.Stdout
        cmd.Stderr = os.Stderr
        if err := cmd.Run(); err != nil { die("cosign sign-blob %s: %v", e.Name, err) }

        raw.Addons[i].Signature = &catalog.Signature{
            Bundle: fmt.Sprintf("%s/%s.bundle", *releaseBaseURL, e.Name),
        }
    }

    // Emit the signed YAML.
    out, err := yaml.Marshal(&raw)
    if err != nil { die("marshal signed yaml: %v", err) }
    if err := os.WriteFile(filepath.Join(*outDir, "addons.yaml.signed"), out, 0o644); err != nil {
        die("write signed yaml: %v", err)
    }
    fmt.Printf("signed %d entries → %s\n", len(raw.Addons), *outDir)
}

func die(format string, args ...any) {
    fmt.Fprintf(os.Stderr, "catalog-sign: "+format+"\n", args...)
    os.Exit(1)
}
```

**Key invariants** in the tool:
- Reuses `internal/catalog.LoadBytes` for validation parity with runtime.
- Reuses `signing.CanonicalEntryBytes` for byte-for-byte parity with verifier.
- Stamps deterministic release-asset URLs (the release doesn't exist yet, but
  the URL is predictable).
- Emits `addons.yaml.signed` as a separate file — the on-disk
  `catalog/addons.yaml` is replaced LATER (in the workflow step that consumes
  this artifact).

### 2. New release workflow job `sign-catalog-entries`

Add as the FIRST job in `.github/workflows/release.yml` (before
`build-and-push`):

```yaml
sign-catalog-entries:
  name: Sign Catalog Entries
  if: >
    github.event.workflow_run.conclusion == 'success' &&
    startsWith(github.event.workflow_run.head_branch, 'v')
  runs-on: ubuntu-latest
  permissions:
    contents: read
    id-token: write   # OIDC for cosign keyless

  steps:
    - uses: actions/checkout@v4
      with:
        ref: ${{ github.event.workflow_run.head_branch }}
        fetch-depth: 0

    - name: Set up Go
      uses: actions/setup-go@v5
      with:
        go-version-file: go.mod

    - name: Install cosign
      uses: sigstore/cosign-installer@v3
      with:
        cosign-release: 'v2.4.1'

    - name: Extract version from tag
      id: version
      run: |
        TAG="${{ github.event.workflow_run.head_branch }}"
        echo "TAG=$TAG" >> "$GITHUB_OUTPUT"

    - name: Sign catalog entries (cosign keyless)
      env:
        COSIGN_YES: "true"
      run: |
        TAG="${{ steps.version.outputs.TAG }}"
        BASE="https://github.com/MoranWeissman/sharko/releases/download/${TAG}"
        go run ./cmd/catalog-sign --out _dist/catalog --release-base-url "${BASE}"

    - name: Upload signed catalog + bundles as workflow artifact
      uses: actions/upload-artifact@v4
      with:
        name: signed-catalog
        path: _dist/catalog/
        if-no-files-found: error
        retention-days: 7
```

### 3. Modify `build-and-push` job

Add `needs: sign-catalog-entries` and a download step before `docker build`:

```yaml
build-and-push:
  needs: sign-catalog-entries
  # ...
  steps:
    - uses: actions/checkout@v4
      # ...
    - name: Download signed catalog artifact
      uses: actions/download-artifact@v4
      with:
        name: signed-catalog
        path: _dist/catalog/

    - name: Use signed catalog for build
      run: cp _dist/catalog/addons.yaml.signed catalog/addons.yaml

    # ... existing docker build steps unchanged ...
```

### 4. Modify `goreleaser` job similarly

`needs: [sign-catalog-entries, build-and-push, helm-package]`.
Same download + cp steps before `Run GoReleaser`.

### 5. Modify `goreleaser` to upload bundles as release assets

In `.goreleaser.yml` (find existing config), add to `release.extra_files`:

```yaml
release:
  extra_files:
    - glob: ./_dist/catalog/*.bundle
```

OR add a final workflow step after goreleaser that uploads via `gh release
upload`. Pick the simpler one based on what `.goreleaser.yml` looks like.

### 6. Helm-package job

The Helm chart doesn't embed the catalog; this job doesn't need changes.
Confirm by inspection.

### 7. Local dev story

`go build` from main still works (uses unsigned `catalog/addons.yaml`).
The signing tool only runs in the release workflow.

Document in the README or `CONTRIBUTING.md`:
- Local dev: catalog stays unsigned; UI shows "Unsigned" pill.
- Release builds: catalog is signed by the release pipeline.

## Test plan

### Unit — `cmd/catalog-sign/main_test.go`

Tests are tricky because the tool calls the `cosign` CLI. Two paths:

1. **Skip the `cosign` invocation in tests** — extract a `signBlob(payload)
   ([]byte, error)` interface, stub it in tests with a fake bundle. Test
   the orchestration logic (canonical bytes generation, YAML mutation, file
   layout, deterministic URLs).
2. **Skip integration entirely** — unit-test only `signing.CanonicalEntryBytes`
   (already covered by V123-2.2) and let the workflow itself be the
   integration test.

Recommend (1) for the orchestration test surface; (2) for the cosign call.
Three test cases:

- `TestCatalogSign_StampsURLOnEveryEntry` — fake signer returns a constant
  bundle; assert every entry in `addons.yaml.signed` has
  `signature.bundle` set to the expected URL.
- `TestCatalogSign_PreservesEntryFields` — fake signer used; assert every
  field in the original entry is preserved in the output (only `signature`
  is added).
- `TestCatalogSign_DeterministicOrdering` — two consecutive runs produce
  byte-identical `addons.yaml.signed` (as long as fake signer is deterministic).

### Workflow validation

The workflow itself can be linted but not executed without a tag push.
Smoke-test the YAML with `act` (if available) or by visual review against
the existing `build-and-push` step structure.

### Manual regression

After this PR merges to main, the next tag push will be the integration
test. **DO NOT cut a tag for this story** — that's V123-4.5's call. We need
operator confirmation before cutting v1.23.0.

If you want to validate without cutting a real tag: cut a throwaway tag like
`v1.23.0-rc1` in a fork, observe the workflow, and revert if anything
breaks. Optional; the user can sequence this however they prefer.

## Quality gates

- `go build ./cmd/catalog-sign/...` — clean.
- `go vet ./cmd/catalog-sign/...` — clean.
- `go test ./cmd/catalog-sign/... -race -count=1` (3 orchestration tests).
- No regression in existing tests.
- `gh workflow list` confirms release.yml still parses (or `actionlint`
  if available).
- No swagger regen (no API surface change).

## Explicit non-goals

- Signing third-party catalog files. That's the operator's responsibility;
  Sharko's release pipeline only signs its own embedded catalog.
- Tag cut. **No `git tag v1.23.0` from this story.** That's V123-4.5 after
  the user explicitly approves.
- Running cosign sign-blob in tests. Live Fulcio/Rekor calls are flaky for
  CI; mock the signer at the test boundary.
- Per-entry rebuild on partial signing failure. If cosign fails on one entry,
  the whole job fails. Acceptable — release is bounded by the OIDC token's
  short lifetime; partial signatures can't be reasoned about safely.

## Dependencies

- V123-2.1 — `Signature` field on `CatalogEntry` — done ✅.
- V123-2.2 — `signing.CanonicalEntryBytes` + verifier interpretation — done ✅.
- V123-2.3 — default trust policy whitelists Sharko's own release workflow — done ✅.

## Gotchas

1. **Canonical bytes parity** is the make-or-break invariant. The tool MUST
   call `signing.CanonicalEntryBytes`. Don't reimplement canonicalization.
2. **Workflow `if:` conditions on tag push.** All new jobs gate on
   `startsWith(github.event.workflow_run.head_branch, 'v')` to skip non-tag
   builds. The tag pattern matches the existing image-signing job.
3. **Workflow artifact retention.** 7 days is enough — the artifact is
   ephemeral; release assets are the durable copy.
4. **OIDC `id-token: write` permission** required on the new job. Match the
   existing image-signing job's permissions block.
5. **Release-asset URL convention** must EXACTLY match what GitHub generates
   (`https://github.com/<owner>/<repo>/releases/download/<tag>/<filename>`).
   No trailing slash. No extra path components.
6. **The release commit doesn't exist as a git artifact** — the "signed YAML"
   only lives inside the released binary's embedded bytes and as a release
   asset. The on-tree `catalog/addons.yaml` stays unsigned forever.
7. **Cert-identity match.** The default trust policy from V123-2.3 expects
   `^https://github\.com/MoranWeissman/sharko/\.github/workflows/release\.yml@refs/tags/v.*$`.
   Confirm the workflow file is named `release.yml` and the trigger ref is
   `refs/tags/vX.Y.Z` so the cert SAN matches. If goreleaser uses a different
   workflow file or ref pattern, the trust policy default needs adjustment.
8. **First-tag-after-merge will be the integration test.** Don't cut a tag
   from this story. Document in the PR body that v1.23.0 cut is V123-4.5.
9. **Concurrent-tag risk** — if two tags fire concurrently, the
   `concurrency: release` block at the top of release.yml serializes them
   already. No new logic needed.

## Role files (MUST embed in dispatch)

- `.claude/team/devops-agent.md` — primary (release workflow, cosign).
- `.claude/team/go-expert.md` — for the new `cmd/catalog-sign` tool.
- `.claude/team/security-auditor.md` — signing posture, cert identity match,
  release-asset URL discipline.

## Use context7 MCP

Before writing the cosign sign-blob invocation, query context7 for the
current `cosign sign-blob` flag syntax. The flags shifted between cosign 2.0
and 2.4 (the existing pipeline uses `cosign-release: v2.4.1`). Confirm
`--bundle`, `--output-signature`, `--output-certificate`, `--yes` are still
the right flags.

## PR plan

- Branch: `dev/v1.23-release-signing` off main.
- Commits (suggested):
  1. `feat(cmd/catalog-sign): tool to sign catalog entries with cosign keyless (V123-2.5)`
  2. `test(cmd/catalog-sign): orchestration tests with fake signer (V123-2.5)`
  3. `feat(release): add sign-catalog-entries job; build/goreleaser consume signed YAML (V123-2.5)`
  4. `chore(bmad): mark V123-2.5 for review`
- **No tag.** Tag cut is V123-4.5.

## Next story

V123-2.6 — Tests: verification happy / mismatch / unsigned / untrusted paths.
Closes Epic V123-2. Also the natural place to expand the verification-state
distinction (warning chip variants) that V123-2.4 punted.

## Tasks completed

1. **New Go tool `cmd/catalog-sign/main.go`.** Imports `catalog`,
   `catalogembed`, and `signing`. Validates the embedded YAML through
   `catalog.LoadBytes` for runtime parity, then re-unmarshals into a fresh
   raw struct so mutation never touches computed-only fields (Source,
   SecurityTier, Verified, SignatureIdentity). Per-entry canonical bytes
   come from `signing.CanonicalEntryBytes` — never reimplemented. Stamps
   deterministic release-asset URLs (`<base>/<name>.bundle`).
2. **Restructured `main.go` for testability.** Extracted `signer` interface
   with `SignBlob(payloadPath string, out signOutputs) error`; production
   uses `cosignCLI{}` (shell-out); the testable orchestration lives in
   `run(opts options, s signer) error`. The fake signer in tests writes a
   constant byte string to the bundle path so the orchestration is
   exercised hermetically.
3. **Tests `cmd/catalog-sign/main_test.go`.** 4 cases:
   - `TestCatalogSign_StampsURLOnEveryEntry` — every entry's
     `signature.bundle` matches `<base>/<name>.bundle` and the corresponding
     `.bundle` file exists on disk (goreleaser glob requirement).
   - `TestCatalogSign_PreservesEntryFields` — round-trips every YAML field
     unchanged; only `signature` is added.
   - `TestCatalogSign_DeterministicOrdering` — two runs produce
     byte-identical `addons.yaml.signed`.
   - `TestCatalogSign_RejectsMissingReleaseBaseURL` — guards the CLI
     contract so a misconfigured workflow can't emit empty-URL stamps.
4. **Workflow restructure `.github/workflows/release.yml`.** New
   `sign-catalog-entries` job runs FIRST (gated by tag prefix + CI success).
   Permissions: `contents: read`, `id-token: write` (matches existing
   image-sign job posture). Steps: checkout → setup-go → install cosign
   v2.4.1 → extract tag → `go run ./cmd/catalog-sign --out _dist/catalog
   --release-base-url "https://github.com/MoranWeissman/sharko/releases/
   download/${TAG}"` → upload-artifact `signed-catalog`. `build-and-push`
   gained `needs: sign-catalog-entries` and a download/cp pair before the
   docker build. `goreleaser` gained `sign-catalog-entries` to its needs
   list and the same download/cp pair. `helm-package` untouched (the chart
   doesn't embed the catalog).
5. **`.goreleaser.yaml` extension.** Added `release.extra_files: [glob:
   ./_dist/catalog/*.bundle]` so the per-entry bundles land as release
   assets at the deterministic URLs the signed YAML stamps.
6. **BMAD tracking.** sprint-status.yaml flipped V123-2.5 to `review`,
   `last_updated` refreshed; story frontmatter updated; retrospective
   sections appended.

## Files touched

- `cmd/catalog-sign/main.go` (NEW — orchestration tool)
- `cmd/catalog-sign/main_test.go` (NEW — 4 hermetic tests)
- `.github/workflows/release.yml` (new sign-catalog-entries job; download
  + cp steps in build-and-push and goreleaser; goreleaser `needs` extended)
- `.goreleaser.yaml` (added `release.extra_files` glob for `*.bundle`)
- `.bmad/output/implementation-artifacts/sprint-status.yaml`
- `.bmad/output/implementation-artifacts/V123-2-5-release-pipeline-sign-embedded-catalog-entries.md`

## Tests

Quality gates run on the dev/v1.23-release-signing branch:

- `go build ./cmd/catalog-sign/...` — passed.
- `go vet ./cmd/catalog-sign/...` — passed.
- `go test ./cmd/catalog-sign/... -race -count=1` — 4 PASS in ~6.9s.
  - TestCatalogSign_StampsURLOnEveryEntry — 45 entries, every URL stamped,
    every `.bundle` file present.
  - TestCatalogSign_PreservesEntryFields — every CatalogEntry field
    round-trips; only `signature` is added.
  - TestCatalogSign_DeterministicOrdering — two runs → byte-identical
    `addons.yaml.signed`.
  - TestCatalogSign_RejectsMissingReleaseBaseURL — guard fires.
- `go vet ./...` — passed (no regression elsewhere).
- `go build -o /dev/null ./cmd/sharko` — passed (no regression).
- Workflow YAML parsed cleanly via gopkg.in/yaml.v3 (actionlint not
  installed locally; visual review confirmed the new job + `needs` chain +
  download/cp steps match the existing job patterns).

The cosign sign-blob CLI invocation cannot be validated without a live
Fulcio/Rekor call — the brief explicitly defers that to the first real tag
push after merge.

## Decisions

1. **Cosign 2.4.x flag set confirmed via context7.** `--bundle`,
   `--output-signature`, `--output-certificate`, `--yes` are all current
   in cosign 2.4.x and emit a Sigstore protobuf bundle. No flag breakage
   between v2.0 and v2.4.1 (the version pinned in `cosign-installer@v3`).
   The tool emits both the `.bundle` (authoritative) and `.sig`/`.pem`
   (companions for any consumer that prefers the split-file format) so
   downstream choice is preserved.
2. **`.goreleaser.yaml` `extra_files` extension chosen.** Existing
   `release:` block had only `github: {owner, name}` — adding `extra_files:
   [glob: ./_dist/catalog/*.bundle]` is a clean two-line append. The
   alternative (`gh release upload "$TAG" _dist/catalog/*.bundle` as a
   final workflow step) is unnecessary because goreleaser already runs in
   the `goreleaser` job after the catalog has been downloaded into
   `_dist/catalog/`.
3. **Three-output emission per entry (`.bundle` + `.sig` + `.pem`)** instead
   of bundle-only. Costs 2 extra files per entry per release (~45 entries
   → 90 small files) but preserves backwards-compat with any consumer that
   uses `cosign verify-blob --certificate ... --signature ...` instead of
   `--bundle`. The runtime verifier (V123-2.2) consumes only the bundle;
   companions are insurance.
4. **Validation via `catalog.LoadBytes` happens BEFORE signing.** A schema
   error in the embedded YAML should fail the release workflow loudly at
   the sign step, not propagate silently into a signed-but-unloadable
   YAML. The loaded `*Catalog` is then discarded — mutation works on a
   fresh re-unmarshal so computed-only fields (Source, SecurityTier,
   Verified, SignatureIdentity) can't leak into the signed output.
5. **Re-unmarshal mutation pattern, not in-place edit on `*Catalog`.** The
   loader sets `Source = "embedded"` and `SecurityTier = ...` on every
   entry; if we marshalled those back out, the signed YAML would carry
   computed fields that the runtime would refuse to round-trip
   (`yaml:"-"`). The re-unmarshal gives us a clean struct that only
   carries on-disk fields plus the new Signature stamp.
6. **Tests use a fakeSigner; live cosign deferred to first tag push.**
   Live Fulcio/Rekor calls are flaky in CI and the brief's gotcha #8
   explicitly accepts the first-tag integration risk. The fakeSigner
   interface is the test-quality compromise — orchestration is hermetic,
   signing semantics are an integration concern.
7. **No tag cut from this story.** Per the brief's dependencies and the
   `feedback_release_cadence.md` rule. v1.23.0 cut belongs to V123-4.5
   after operator approval. The PR body flags the first-tag-after-merge
   as the integration test.
