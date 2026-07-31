# sharko-engine

The Sharko v4 "engine" — a standalone Helm chart that turns a user's GitOps
repo (`clusters/`, `catalog.yaml`, `values/`) into ArgoCD ApplicationSets.
This chart **is** the whole of Sharko's deployment logic — see
`docs/design/2026-07-30-v4-data-file-format.md` for the render mechanics and
`.bmad/output/architecture/2026-07-31-catalog-approved-model.md` (decision
6) for the "catalog = the approved list" model this chart implements: it
reads `catalog.yaml` + `clusters/` + `values/` from the user's own repo,
nothing else. There is no shipped/curated addon data baked into this chart
— that set now lives only on the Sharko server, feeding the Marketplace.

It replaces `templates/bootstrap/` from v3. The mechanics (matrix generator:
cluster-label arm × git-files arm) are the same proven shape; what changed
is the data-file format it reads and the settings pass-through it supports.

## What it is

One ApplicationSet per addon in the org's own `catalog.yaml` — every entry
there is a FULL entry (repoURL, chart, namespace, version, settings), not a
delta against a shipped default. An empty or missing `catalog.yaml` renders
zero ApplicationSets: nothing runs in a fresh org's fleet until an addon is
approved into the catalog. The templates carry **zero per-addon
conditionals** — there is no `if addon == "cert-manager"` anywhere in this
chart. Swap any addon name for any other and the shape produced is
identical.

Each generated ApplicationSet:

- Selects clusters labelled `addons.sharko.dev/<addon>: enabled` (that
  label is derived from `clusters/<cluster>.yaml` and pushed onto the
  ArgoCD cluster secret by Sharko's existing reconciler — the engine never
  reads a cluster secret's labels directly, see the hard rule below).
- Reads that cluster's `clusters/<cluster>.yaml` for its version pin and
  per-cluster settings.
- Layers Helm values: chart defaults → `values/global/<addon>.yaml` →
  `values/clusters/<cluster>/<addon>.yaml`, via Helm's own `valueFiles`
  ordering — this chart writes no merge logic of its own.
- Defaults `syncPolicy.preserveResourcesOnDeletion: true` — deleting the
  ApplicationSet never cascades into deleted workloads.

**Hard rule (design doc section 4.4):** inside a generated ApplicationSet,
the round-two metadata field is a merge artifact of both matrix arms — the
clusters arm (listed first) wins any key the two arms share, and the
matrix generator's merge deep-merges nested maps, so that field ends up a
hybrid of the assignment file's envelope name and the real cluster
secret's labels/annotations, never a clean handoff to either side. The
only cluster-identity fields safe to use are `.name` and `.server`. This
is why the v3 per-cluster version-override label (`<addon>-version` read
via `index .metadata.labels ...`) could never reliably carry over into v4;
version pins moved into `clusters/<cluster>.yaml` instead (decision D8).

**createNamespace precedence rule.** ArgoCD has no dedicated `createNamespace`
field — it is only ever expressed as `CreateNamespace=true` membership in
`syncOptions`. When a cluster overrides both `createNamespace` and
`syncOptions` in the same addon's settings, `createNamespace` always wins:
it decides whether `CreateNamespace=true` ends up in the rebuilt list,
regardless of whether the cluster's own `syncOptions` value already
contained (or omitted) that entry.

## Rendering locally

Against this repo's own render-test fixture (the design doc's worked
example — two clusters, cert-manager pinned older on one with a webhook
quirk, metrics-server everywhere on the catalog default):

```bash
helm template testengine charts/sharko-engine \
  --values tests/enginerender/testdata/engine-values.yaml \
  --values tests/enginerender/testdata/catalog.yaml
```

Against your own repo, in production shape, the two values sources are
`engine.yaml`'s `helm.parameters` (repo URL/revision, host cluster name,
project name) and your own `catalog.yaml` (passed as the chart's one
enveloped Helm values file — see decision D6 for why it is exactly one
file, never more).

`helm lint charts/sharko-engine` and `go test ./tests/enginerender/...` are
the two automated gates; both run in CI (`helm-validate` and
`go-build-test`).

## Publishing (v4 Wave 1 Story 2.4, versioning corrected in R2 review)

On every tagged release, `.github/workflows/release.yml`'s
`helm-package-engine` job packages this chart, pushes it to
`oci://ghcr.io/moranweissman/sharko/sharko-engine`, and cosign-signs the
pushed digest — the exact package → push → sign shape the `helm-package`
job already uses for the `sharko` chart itself, same registry namespace,
same keyless (OIDC) signing identity.

**Chart-version rule.** The engine chart versions independently — bumped
"whenever Sharko releases new deploy logic or a new shipped catalog"
(design doc section 5), not tied to the product release tag by name. The
release pipeline publishes this chart at **exactly its own committed
`version:`/`appVersion:` from this file's `Chart.yaml`** — never an
override, and never the product's release tag. The pin a user merges into
their `engine.yaml` references that same chart version, and
`internal/engineversion/generated.go` (regenerated from this file's `name`
+ `version` fields, CI-gated to match) is what the pin-bump check compares
a repo's current pin against — so the published OCI tag and the number
CI enforces are always the same number by construction. (An earlier draft
of this rule published the chart under the product's own release tag
instead; that would have meant the very first release's chart lived at a
version nothing on disk ever pointed to — corrected before it shipped.)

Because most product releases don't touch `charts/sharko-engine/`, most
releases have nothing new to publish here. The release job checks whether
the committed version already exists in GHCR before packaging, and skips
the package/push/sign steps with a loud `::notice::` (not a silent no-op,
not an error) when it does — republishing an unchanged chart under its
own version would be redundant, not idempotent-safe by accident. Bump
this file's `version:` whenever `charts/sharko-engine/` actually changes,
same discipline as any other chart.

## Pulling the released chart

Verify the signature first (same identity policy as the server image and
the `sharko` chart — see `docs/site/operator/supply-chain.md`):

```bash
ENGINE_VERSION=X.Y.Z   # this chart's own Chart.yaml version, per the rule above
cosign verify ghcr.io/moranweissman/sharko/sharko-engine:${ENGINE_VERSION} \
  --certificate-identity-regexp 'https://github.com/MoranWeissman/sharko/.github/workflows/release.yml@.*' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

Then pull it:

```bash
helm pull oci://ghcr.io/moranweissman/sharko/sharko-engine --version ${ENGINE_VERSION} --untar
```

And render it locally, exactly like the "Rendering locally" section
above but against the untarred chart directory instead of
`charts/sharko-engine`:

```bash
helm template testengine sharko-engine \
  --values tests/enginerender/testdata/engine-values.yaml \
  --values tests/enginerender/testdata/catalog.yaml
```

This is also how to inspect a new engine version before merging a
pin-bump PR (`sharko validate` and the pin-bump check compare version
numbers only — actually rendering the new chart against your own repo
before merging is on you, same as reviewing any other dependency bump).

## The kill-Sharko story

Delete the `sharko-engine` Application (`engine.yaml`) from
ArgoCD, or stop running Sharko entirely. Every ApplicationSet this chart
generated has `preserveResourcesOnDeletion: true`, so ArgoCD leaves the
running workloads exactly as they are — nothing gets torn down. What you
lose is only the automation: nobody is watching `clusters/*.yaml` for
changes anymore. `kubectl apply -f engine.yaml` at any point
brings that back, unchanged, because the Application it recreates is the
same one Sharko applied in the first place — Sharko does not manage a
resource that manages itself (decision D15).

If you want to stop using Sharko's format too, not just Sharko the
program: every object this chart generates is a standard ArgoCD
ApplicationSet with no Sharko-specific machinery inside it apart from
labels. Write your own ApplicationSets by hand from here — there is
nothing to unwind (design doc section 3.4, "rung 4" of the escape ladder).

## What this chart does not do

- It does not read `managed-clusters.yaml` — that is the Sharko server's
  cluster reconciler's job, never the engine's (design doc section 2.4).
- It does not read `sharko/default-addons.yaml` or
  `sharko/marketplace-sources.yaml` — those configure the Sharko server
  itself, not the engine (design doc section 2.6).
- It does not carry any shipped/curated addon data of its own — the
  Marketplace's curated list lives only on the Sharko server
  (`.bmad/output/architecture/2026-07-31-catalog-approved-model.md`
  decision 6). Approving an addon copies it, as a full entry, into the
  org's own `catalog.yaml` — that is the only addon data this chart ever
  reads.
- It does not validate the data files it reads — `sharko validate` is the
  gate that runs before a PR merges; the engine's job is to render what is
  already there.
