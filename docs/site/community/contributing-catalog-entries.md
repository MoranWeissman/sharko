# Contributing a Catalog Entry

Sharko ships a curated list of addons — cert-manager, ingress-nginx,
Prometheus, and a few dozen others — so a new user gets a working
Marketplace on day one instead of an empty screen. That list lives in
one file: [`catalog/addons.yaml`](https://github.com/MoranWeissman/sharko/blob/main/catalog/addons.yaml)
in this repository. This page explains how to add or update an entry.

For everything else about opening a pull request (branching, DCO
sign-off, running the test suite), see the main
[`CONTRIBUTING.md`](https://github.com/MoranWeissman/sharko/blob/main/CONTRIBUTING.md).
This page only covers what's specific to a catalog entry.

## Before you open a PR

Open the GitHub compare/PR page and pick the **"catalog-entry"** template
from the template dropdown (or add `?template=catalog-entry.md` to the
compare URL) — it walks you through every field as a checklist. This
page is the prose version of the same checklist, with the reasoning
behind each rule.

## What belongs in the catalog, and what doesn't

Sharko's curated catalog is not a general Helm-chart index — a user can
already point Sharko at any chart they want by pasting a repo URL. The
catalog exists for the addons a new user would expect to find without
having to know they exist first.

**Belongs:** an addon that's already independently curated somewhere
else — the CNCF Landscape, AWS EKS Blueprints, Azure AKS add-ons, GKE
Marketplace, or an ArtifactHub-verified/official publisher. That's the
`curated_by` field, and it's required for exactly this reason: Sharko
doesn't originate a curation decision, it aggregates ones that already
exist. If your addon isn't listed anywhere else yet, get it listed
there first, or open an issue to discuss the exception.

**Doesn't belong:** your team's internal chart. Sharko has a separate,
first-class path for that — add it to your own `catalog/addons.yaml`
delta after Sharko is running (that file lives in *your* GitOps repo,
not this one, and it's exactly as first-class as any curated addon:
same version field, same upgrade flow, same everything).

## The fields, in plain English

Every entry is one item under `addons:` in `catalog/addons.yaml`. Here's
a complete real example, then a walk through what each part means:

```yaml
- name: cert-manager
  description: Automated TLS certificate lifecycle management for Kubernetes.
  chart: cert-manager
  repo: https://charts.jetstack.io
  default_namespace: cert-manager
  docs_url: https://cert-manager.io/docs
  homepage: https://cert-manager.io
  source_url: https://github.com/cert-manager/cert-manager
  maintainers: [jetstack]
  license: Apache-2.0
  category: security
  curated_by: [cncf-graduated, aws-eks-blueprints, artifacthub-verified]
  min_kubernetes_version: "1.23"
  required_values:
    - key: installCRDs
      description: Set to true so the chart installs cert-manager's own CRDs; otherwise apply them separately before installing.
  secrets:
    - name: DNS provider API credentials
      description: Only needed if you configure a DNS-01 ClusterIssuer (e.g. Route53, Cloudflare) after install — the chart itself needs no secret to run.
  quirks:
    - "The webhook's CA bundle is rewritten on every reconcile — ignore admissionregistration.k8s.io/ValidatingWebhookConfiguration's caBundle field in Argo CD or every sync looks dirty."
```

### Required on every entry

| Field | What it is |
|---|---|
| `name` | Lowercase, DNS-safe (letters, digits, hyphens). Must be unique in the file. Should match the chart's own common name — this is what a user searches for. |
| `description` | One sentence. What the addon does, in words a non-expert would use — not an architecture summary. |
| `chart` | The Helm chart's name inside its repo. |
| `repo` | The Helm repository URL. `https://` for a classic repo (must serve `index.yaml`), or `oci://` for a Helm 3.8+ OCI registry. |
| `default_namespace` | The namespace the chart normally installs into. |
| `license` | The chart's SPDX license identifier (e.g. `Apache-2.0`, `MIT`). Outside the allow-list (`Apache-2.0`, `BSD-3-Clause`, `MIT`, `MPL-2.0`), it still merges — CI just flags it for a maintainer to read the actual license text before merging. |
| `maintainers` | At least one name — the chart's own maintainer list, or the publishing org. |
| `category` | Exactly one of: `security`, `observability`, `networking`, `autoscaling`, `gitops`, `storage`, `database`, `backup`, `chaos`, `developer-tools`. |
| `curated_by` | At least one of: `cncf-graduated`, `cncf-incubating`, `cncf-sandbox`, `aws-eks-blueprints`, `azure-aks-addon`, `gke-marketplace`, `artifacthub-verified`, `artifacthub-official`. See ["What belongs"](#what-belongs-in-the-catalog-and-what-doesnt) above. |

### Optional, but worth adding when you know them

- **`docs_url`, `homepage`, `source_url`** — links a user landing on the
  addon's detail page would actually want.
- **`min_kubernetes_version`** — only set this if the chart's own docs
  state a floor. Don't guess.
- **`required_values`** — the operational knowledge that saves the next
  person a failed install. One entry per Helm value that MUST be set
  before the addon actually works (not every value the chart accepts —
  just the ones with no sane default). Each entry is a `key` (the
  dotted Helm values path) and a plain-English `description` of what it
  does and, where useful, what to set it to. Most addons need none of
  these — skip the section entirely if the chart works out of the box.
- **`secrets`** — one entry per credential the addon needs to actually
  *work* (as opposed to what it needs to *install*, which is usually
  nothing). Each entry is a short `name` and a `description` of what
  it's for and how it's normally supplied (an env var, a mounted file,
  an IRSA/workload-identity role, ...).
- **`quirks`** — short, plain-English sentences about known operational
  gotchas: things that surprise people the first time, or cause a
  support question if left undocumented. Free text on purpose — a
  quirk is knowledge for a human reading the addon's detail page, not a
  machine-actionable setting. (If a quirk needs an actual Argo CD
  setting — an `ignoreDifferences` rule, a namespace override — that's
  a separate mechanism; open an issue to discuss it rather than trying
  to express it here.)

### Fields you should never set by hand

- **`version`** is not a field on a curated entry, and never will be —
  a version baked into the shipped catalog goes stale the moment a new
  chart release ships. Sharko writes the version a user is actually
  running into *their own* `catalog/addons.yaml` delta, not into this
  file.
- **`security_score`**, **`github_stars`**, and
  **`security_score_updated`** are refreshed automatically by a
  scheduled job (OpenSSF Scorecard + GitHub stars). Leave
  `security_score: unknown` on a new entry — it fills in on its own
  after the addon ships.

## Validating your entry before you open the PR

Run the same check CI runs:

```bash
go run ./cmd/sharko validate-catalog catalog/addons.yaml
```

This is not a separate tool from what the running server enforces — it's
a thin command-line front end over the exact loader Sharko itself uses
to read `catalog/addons.yaml` at startup. If your entry passes this
locally, it will pass in CI (the **Catalog Format Validation** job,
which only runs on PRs that touch `catalog/addons.yaml`).

A failure names the specific entry and the specific problem, for
example:

```text
FAIL catalog/addons.yaml: catalog: entry #12 (name="my-addon"): missing required field: description
```

## What happens after you open the PR

A maintainer reviews the entry against the checklist above, spot-checks
that the chart repo is actually reachable, and — for anything outside
the license allow-list — reads the license text before merging. Once
merged, the addon appears in the Marketplace on the next Sharko release
that bundles the updated catalog.
