# Sharko v4 — the data-file format and the settings schema

**Status:** design, agreed contract for v4 Wave 1
**Date:** 2026-07-30
**Story:** v4 Epic 2, Story 2.1
**Replaces:** the v3 scaffold in `templates/bootstrap/`

---

## Needs maintainer decision

**None.** Every choice in this document is locked with a reason in the decisions log
(section 7). Nothing here contradicts the v4 PRD.

Two things cost real work in other stories, and the build lanes should see them early.
They are decided, not open:

- The label that switches an addon on becomes `addons.sharko.dev/<addon>: enabled`
  instead of the bare `<addon>: enabled` used in v3 (decision D9).
- The per-cluster version pin stops being a label on the cluster secret and becomes a
  field in a data file (decision D8). This one is not a preference — the old way stops
  working in v4, and section 4.4 shows exactly why.

---

## 1. One page: what a user sees

This is a real repo after the bootstrap PR, one cluster registered, and one addon
switched on.

```text
my-gitops-repo/
├── README.md                          written once by the bootstrap PR
├── engine/
│   └── application.yaml               the engine pin — the ONLY moving part Sharko ships
├── clusters/
│   └── prod-eu.yaml                   which addons run here, at which version, tuned how
├── fleet/
│   └── connections.yaml               how Sharko reaches each cluster
├── catalog/
│   └── addons.yaml                    your own addons + your changes to the shipped ones
└── values/
    ├── global/
    │   └── cert-manager.yaml          Helm values for cert-manager, everywhere
    └── clusters/
        └── prod-eu/
            └── cert-manager.yaml      Helm values for cert-manager, only on prod-eu
```

Seven files. No templates. Nothing generated. Every file is something a person wrote
on purpose, and every file can be read top to bottom without knowing Helm.

**What "no templates" means.** In v3, Sharko copied a folder of Helm template files into
your repo, and those templates were what actually built the deployments. If Sharko fixed
a bug in them, you had to copy the new ones in. In v4 that logic lives in a chart Sharko
publishes and signs, and your repo points at one version of it. The pointer is
`engine/application.yaml`. Upgrading is a pull request that changes one line.

**What the bootstrap PR actually contains.** Empty folders (`clusters/`, `fleet/`,
`values/global/`, `values/clusters/`, `catalog/`), the engine pin, and the README.
Nothing else. Git cannot track an empty folder, so each empty folder carries a `.gitkeep`
file — that is a git limitation, not Sharko data.

There is no empty `fleet/connections.yaml` or `catalog/addons.yaml` in the seed. **A file
that is not there means "empty".** It is never an error. Sharko creates each file the
first time it has something to put in it.

---

## 2. Every file, fully specified

### 2.0 Which files carry the Sharko envelope, and which do not

Every Sharko-owned file starts with the same four-line header — the *envelope* — so any
reader can tell what a file is before parsing it:

```yaml
apiVersion: sharko.dev/v1
kind: <one of the kinds below>
metadata:
  name: <a short name>
spec:
  # everything else lives here
```

This is the shape already in the code at `internal/schema/envelope.go`. v4 does not
change it.

Two kinds of file in the repo deliberately do **not** carry the envelope:

| File | Why it has no envelope |
|---|---|
| `values/**/*.yaml` | These are Helm values, handed straight to somebody else's chart. If we wrapped them, Helm would pass `apiVersion`, `kind` and `metadata` to cert-manager's chart as if they were settings. They must stay plain. |
| `engine/application.yaml` | This is a real Argo CD `Application` object. Argo CD has to be able to apply it as-is. It carries Argo CD's own `apiVersion`, not Sharko's. |

That is the whole exception list. It is short on purpose.

### 2.1 The per-cluster assignment file

**Path:** `clusters/<cluster-name>.yaml`
**Kind:** `ClusterAssignment`
**Who writes it:** Sharko (through a PR), or a person editing by hand
**Who reads it:** the engine, and `sharko validate`

One file per cluster. It answers one question: *what runs on this cluster, and how.*

The file name is the cluster name. `clusters/prod-eu.yaml` is the cluster called
`prod-eu`. This is not a convention Sharko is free to break — the engine finds the file
by name (section 4.2), so a mismatch means the cluster silently gets nothing.
`sharko validate` fails when the file name and `spec.cluster` disagree.

#### Fields

| Field | Type | Required | What it means |
|---|---|---|---|
| `spec.cluster` | string | yes | The cluster name. Must equal the file name without `.yaml`. |
| `spec.addons` | map | yes | One entry per addon, keyed by the addon name. May be empty (`{}`). |
| `spec.addons.<name>.enabled` | bool | yes | `true` deploys the addon here. `false` keeps the entry — and its settings — but stops deploying. |
| `spec.addons.<name>.version` | string | no | The chart version to run **on this cluster**. Leave it out to follow the catalog (see below). |
| `spec.addons.<name>.settings` | map | no | Deployment settings for this addon on this cluster. Full field list in section 3. |

Keyed by addon name, not a list, for three reasons: an addon can only appear once, a
person editing by hand cannot create a duplicate by accident, and a pull request that
changes one addon shows as a change to one block.

#### Where per-cluster version pins live, exactly

`spec.addons.<name>.version` in this file. Nowhere else. That field is the only
per-cluster pin in the whole format.

**"No pin = follow the catalog default"** means: when `version` is absent, the engine uses
the `version` from the merged catalog entry for that addon (section 2.3). In practice
that value sits in your own `catalog/addons.yaml`, because Sharko writes the version
there the first time you switch an addon on. So the version you run is always a number
you can find in your own git.

If neither place has a version, the engine does not guess. The render fails with a
message naming the addon and the cluster, `sharko validate` fails first and names the
file, and — because a failed render produces no manifests — Argo CD keeps deploying
whatever it deployed before. Nothing half-applies.

#### Worked example

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      version: "1.12.0"
      settings:
        ignoreDifferences:
          - group: admissionregistration.k8s.io
            kind: ValidatingWebhookConfiguration
            jsonPointers:
              - /webhooks/0/clientConfig/caBundle
    metrics-server:
      enabled: true
    external-dns:
      enabled: false
```

Read out loud: on `prod-eu`, cert-manager runs at 1.12.0 and Argo CD is told to stop
reporting a difference on the webhook's certificate field. metrics-server runs at
whatever the catalog says. external-dns is switched off, but the entry is kept so
switching it back on is a one-word change.

### 2.2 The values files

**Paths:** `values/global/<addon>.yaml` and `values/clusters/<cluster>/<addon>.yaml`
**Kind:** none — plain Helm values
**Who reads them:** the addon's own Helm chart, through Argo CD

These are free-form. Sharko does not define, validate, or interpret their contents.
Whatever the addon's chart accepts, you can put here. That is the point: it is the first
rung of the escape ladder (section 3.4), and it is deliberately unbounded.

Two levels, and only two:

- `values/global/cert-manager.yaml` — cert-manager on every cluster that runs it.
- `values/clusters/prod-eu/cert-manager.yaml` — cert-manager on `prod-eu` only.

Missing files are fine and normal. Most addons on most clusters have neither file.

**Worked example** — `values/global/cert-manager.yaml`:

```yaml
installCRDs: true
replicaCount: 2
resources:
  requests:
    cpu: 10m
    memory: 32Mi
```

And `values/clusters/prod-eu/cert-manager.yaml`:

```yaml
replicaCount: 3
```

On `prod-eu`, cert-manager gets `installCRDs: true`, `replicaCount: 3`, and the resource
requests. The per-cluster file only says what is different. Helm does the merging —
Sharko writes no merging logic at all (section 4.3).

**What changed from v3, and why it matters.** v3 put every addon for a cluster into one
file, `configuration/addons-clusters-values/<cluster>.yaml`, with the addon name as a
top-level key inside it. The engine then had to dig that block out and re-inject it as a
Helm values string. v4 gives each addon its own file. That removes the digging, removes
the re-injection, and means two people changing two different addons on the same cluster
no longer touch the same file.

### 2.3 The catalog delta file

**Path:** `catalog/addons.yaml`
**Kind:** `AddonCatalogDelta`
**Who writes it:** Sharko (through a PR), or a person editing by hand
**Who reads it:** the engine, the Sharko server, `sharko validate`

*Delta* means: **only your changes.** This file never holds a copy of the addons Sharko
ships. It holds your own in-house charts, plus the specific fields you wanted different
on a shipped addon. If Sharko ships 45 addons and you use 6 and changed 1 setting, this
file has 6 short entries.

The addons Sharko ships — their chart location, their namespace, their known quirks —
live inside the engine chart, which is versioned and signed. Where your file and the
shipped set disagree, **your file wins**, field by field.

#### Fields

| Field | Type | Required | What it means |
|---|---|---|---|
| `spec.addons` | map | yes | One entry per addon, keyed by addon name. |
| `spec.addons.<name>.repoURL` | string | see note | Where the Helm chart lives — a Helm repo URL or an `oci://` reference. |
| `spec.addons.<name>.chart` | string | see note | The chart name inside that repo. |
| `spec.addons.<name>.version` | string | see note | The fleet-wide default version for this addon. |
| `spec.addons.<name>.namespace` | string | no | Default namespace for this addon. Falls back to the addon name. |
| `spec.addons.<name>.settings` | map | no | Fleet-wide deployment settings for this addon (section 3). |
| `spec.addons.<name>.additionalSources` | list | no | Extra Helm sources to deploy alongside the main chart. Carried over from v3 unchanged. |
| `spec.addons.<name>.extraHelmValues` | map | no | Extra Helm parameters as name/value pairs. Carried over from v3 unchanged. |

**The note on "required":** for an addon Sharko already ships, every field is optional —
you only write what you are changing. For your own in-house chart, `repoURL`, `chart` and
`version` are all required, because nothing else knows them. `sharko validate` applies
exactly this rule after merging: if the merged entry for an enabled addon is missing any
of those three, it fails and names the addon.

#### Worked example

```yaml
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
    metrics-server:
      version: "3.12.1"
    billing-api:
      repoURL: oci://registry.example.com/charts
      chart: billing-api
      version: "2.4.0"
      namespace: billing
```

Three entries. The first two say only "this is the version we run" — everything else
about cert-manager and metrics-server comes from the shipped catalog. The third is an
in-house service, and it is a first-class addon: same version field, same upgrade flow,
same everything as cert-manager.

### 2.4 The connections file

**Path:** `fleet/connections.yaml`
**Kind:** `ManagedClusters` — unchanged from v3, only the path moved
**Who reads it:** the Sharko server's cluster reconciler. **The engine never reads it.**

This is how Sharko reaches each cluster: name, where its credentials are, which region,
how it was registered. It is the same shape the code already reads through
`models.LoadManagedClusters`, so v4 changes nothing about it except where it sits.

One thing does change in meaning. In v3 this file's `labels` block held the addon
on/off keys (`datadog: enabled`) and the version overrides (`datadog-version: 3.70.7`).
In v4, **Sharko no longer authors addon keys here.** They are derived from the assignment
files in `clusters/` and pushed onto the Argo CD cluster secret by the reconciler that
already exists. The `labels` field stays in the format because brownfield takeover needs
it — Journey 6 preserves a cluster's old labels so the user's own ApplicationSets keep
selecting on them.

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: connections
spec:
  clusters:
    - name: prod-eu
      secretPath: k8s-prod-eu
      region: eu-central-1
      credsSource: secret-kubeconfig
    - name: staging-us
      secretPath: k8s-staging-us
      region: us-east-1
      credsSource: secret-kubeconfig
```

**Why this stayed one file while assignments became one file per cluster.** The
reconciler re-reads this file on a timer to catch drift. One file is one git API call.
One file per cluster would be one call per cluster per tick — at 50 clusters and a 30
second tick that is 6,000 git API calls an hour, which will hit a provider rate limit.
The assignment files have no such cost, because Argo CD reads them through its own repo
cache, not through the git provider's API.

### 2.5 The engine pin

**Path:** `engine/application.yaml`
**Kind:** an Argo CD `Application` — not a Sharko envelope
**Who applies it:** Sharko, at bootstrap and after a pin-bump PR merges

This is the whole of Sharko's deployment logic, reduced to one pointer. It names the
engine chart, the exact version, your repo, and your branch. Nothing else.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: sharko-engine
  namespace: argocd
  labels:
    app.kubernetes.io/managed-by: sharko
  finalizers:
    - resources-finalizer.argocd.argoproj.io
spec:
  project: default
  sources:
    - repoURL: ghcr.io/example-org/charts
      chart: sharko-engine
      targetRevision: 4.0.0
      helm:
        ignoreMissingValueFiles: true
        valueFiles:
          - $values/catalog/addons.yaml
        parameters:
          - name: repo.url
            value: https://github.com/example-org/fleet-gitops.git
          - name: repo.revision
            value: main
          - name: hostCluster.name
            value: hub
    - repoURL: https://github.com/example-org/fleet-gitops.git
      targetRevision: main
      ref: values
  destination:
    server: https://kubernetes.default.svc
    namespace: argocd
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

An engine upgrade is a pull request that changes `targetRevision: 4.0.0` to
`targetRevision: 4.1.0`. One line. Reviewable, and revertable with `git revert`.

Notice there is exactly **one** Sharko file passed as Helm values —
`catalog/addons.yaml`. That is on purpose. Every enveloped file puts its payload under
the key `spec`, so passing two of them as values files would make Helm merge two
unrelated payloads into one `spec` block. v3 did this with three files and got away with
it because their sub-keys happened not to overlap. v4 does not rely on luck.

Everything else the chart needs — your repo URL, your branch, your hub cluster name —
comes through `helm.parameters` in this same file, so there is no second file to keep in
step with this one.

**Who applies this file.** Sharko creates the Application through the Argo CD API during
bootstrap, and re-applies it when a pin-bump PR merges — reusing the PR-merge fan-out
that already exists (`prTracker.SetOnMergeFn`). The file in git is the record of what
Sharko applied and the thing the pin-bump PR edits.

Sharko being down does not affect anything here: the Application already exists in Argo
CD and keeps working. And if you remove Sharko entirely, `kubectl apply -f
engine/application.yaml` is a complete replacement for what Sharko was doing. That is the
kill-Sharko promise, holding at the engine layer.

### 2.6 Files that are Sharko's own, not the engine's

Two v3 files are configuration for the Sharko server. The engine never reads them, and
the bootstrap seed does not create them — Sharko writes each one when you first change
the setting it holds.

| Path | Kind | What it holds |
|---|---|---|
| `sharko/default-addons.yaml` | `DefaultAddons` | Which addons get switched on automatically when a cluster is registered without an explicit choice. |
| `sharko/marketplace-sources.yaml` | `MarketplaceSources` | Extra catalog source URLs to pull addon entries from. |

Both keep their v3 shape and their v3 kind exactly. Only the folder changed.

---

## 3. The settings schema

### 3.1 What a "setting" is, and what it is not

A **value** is something the addon's own chart understands — replica counts, resource
limits, feature flags. Sharko has no opinion about these and does not look at them.

A **setting** is something *Argo CD* understands about how to deploy the addon — which
namespace, what to ignore when comparing, whether to delete things that disappear.
These are Sharko's business, because Sharko generates the Argo CD objects.

The rule that follows from this: **settings are declared data, never template
conditionals.** The engine has no `if addon == "cert-manager"` anywhere in it. It reads
seven named fields and passes them through. That is what makes the engine generic, and
it is what makes an unfamiliar addon behave the same as a familiar one.

### 3.2 The v1 settings — seven fields

Kept deliberately small. Each one earns its place because a real addon needs it and no
Helm value can provide it.

| Field | Type | Default | Plain meaning |
|---|---|---|---|
| `namespace` | string | the addon's name | Which namespace to install into. |
| `createNamespace` | bool | `true` | Let Argo CD create that namespace if it is missing. |
| `syncOptions` | list of strings | `["ServerSideApply=true"]` | Argo CD sync options, passed straight through. |
| `ignoreDifferences` | list of objects | none | Fields Argo CD should stop comparing. This is the escape valve for addons that rewrite their own resources. |
| `prune` | bool | `true` | When something disappears from git, delete it from the cluster too. |
| `selfHeal` | bool | `true` | When something is changed by hand in the cluster, put it back. |
| `preserveResourcesOnDeletion` | bool | `true` | If the Application is ever removed, leave the running workloads alone. |

`ServerSideApply=true` is a default rather than a choice because without it, large addons
(Kyverno, Keda) permanently fail to apply once their CRDs exceed Kubernetes' 262144-byte
annotation limit. v3 learned this the hard way; v4 keeps the lesson.

`preserveResourcesOnDeletion: true` is the deletion-safe default the PRD requires. It
means a cluster secret disappearing can never cascade into deleted workloads.

#### Two tiers — and one field that cannot vary per cluster

The engine builds **one ApplicationSet per addon**, and that one ApplicationSet covers
every cluster running the addon. So a setting can only vary per cluster if it lives on
the generated *Application*, not on the *ApplicationSet*.

| Tier | Fields | Can it differ per cluster? |
|---|---|---|
| Per-Application | `namespace`, `createNamespace`, `syncOptions`, `ignoreDifferences`, `prune`, `selfHeal` | **Yes** |
| Per-ApplicationSet | `preserveResourcesOnDeletion` | **No** — addon-wide |

Setting `preserveResourcesOnDeletion` inside a `clusters/*.yaml` file is a validation
error, not a silent no-op. Sharko names the file and says where the field belongs.

#### Two words that both mean "self-heal"

`selfHeal` in this table is **Argo CD's** self-heal on the addon's Application: the
cluster drifts from the chart, Argo CD puts it back. It is on by default, as in v3.

Sharko's own self-heal — re-applying addon labels onto a cluster secret someone edited
by hand — is a **different thing, off by default**, configured on the Sharko server, and
covered by FR36. Detection is always on; only enforcement is a choice.

They are not related and neither one turns the other on. The docs must say this plainly,
because the same word doing two jobs is how people end up surprised.

### 3.3 Precedence — who wins

Four places can set the same setting. Later wins:

```text
1. engine built-in defaults        (the table in 3.2)
2. shipped catalog entry settings  (the known-quirk defaults, inside the engine chart)
3. your catalog/addons.yaml        (fleet-wide, your choice)
4. clusters/<cluster>.yaml         (this cluster only, your choice)
```

**In one sentence:** engine defaults, then the shipped catalog's known-quirk defaults,
then your fleet-wide choice, then your per-cluster choice — later always wins, one field
at a time, except that list fields replace whole.

That exception matters. `syncOptions` and `ignoreDifferences` are lists, and there is no
honest way to merge two lists — Sharko cannot know whether your entry was meant to add
to the shipped one or replace it. So it replaces. When you write `ignoreDifferences` on a
cluster, you are stating the complete list for that cluster. `sharko validate` warns when
your list replaces a shipped one, so the loss is visible rather than discovered later.

Versions follow the same shape, one level shorter:

```text
1. shipped catalog version (optional — often absent)
2. your catalog/addons.yaml version
3. clusters/<cluster>.yaml version
```

### 3.4 The escape ladder

When Sharko cannot express what you need, there is a defined way up. Each rung is
slower than the one before, and the last rung is an honest exit rather than a trap.

**Rung 1 — a Helm value.** Most needs are chart settings, not deploy settings. Put it in
`values/`. This is unbounded and needs nobody's permission. Try this first, always.

**Rung 2 — a declared setting.** If it is about how Argo CD deploys rather than what the
chart does, one of the seven fields in 3.2 probably covers it. Also self-service.

**Rung 3 — ask for the field.** If neither fits, open an issue. If the need is general,
it becomes a new declared field in a small engine release, and then everybody has it.
This is deliberately the path — a field that one shop needs is usually a field several
shops need, and adding it to the engine keeps every repo template-free.

**Rung 4 — go back to raw ApplicationSets.** If your logic is genuinely bespoke — real
branching that only your team's rules explain — Sharko is the wrong tool, and the docs
say so in those words. Write the ApplicationSet yourself. Sharko generates standard Argo
CD objects, so there is nothing to unwind: the workloads keep running, and you take over
the object that manages them.

Rung 4 existing is what makes rungs 1 to 3 honest. Sharko does not have to be able to
express everything, so it does not have to grow a template language to pretend it can.

---

## 4. How the engine consumes all this

### 4.1 Two template layers — read this first

There are two separate rounds of `{{ }}` in play, and confusing them is the main way to
misread everything below.

**Round one — Helm, when the engine chart renders.** The chart loops over the merged
catalog and writes out one ApplicationSet per addon. At this point the addon's name,
chart, repo URL and the catalog-level settings are all known, so they are written into
the output as plain text.

**Round two — the ApplicationSet controller, later and repeatedly.** The ApplicationSet
that came out of round one still has `{{ }}` in it. Argo CD fills those in per cluster,
from the generators.

Every YAML block in this section shows the output of round one — what Argo CD actually
holds. Inside the engine chart's source, round-two braces are escaped so Helm leaves them
alone.

### 4.2 Which generator arm reads which file

The engine keeps v3's matrix generator shape. A matrix generator combines two lists and
produces every valid pairing.

```yaml
generators:
  - matrix:
      generators:
        # Arm 1 — which clusters run this addon
        - clusters:
            selector:
              matchLabels:
                argocd.argoproj.io/secret-type: cluster
                addons.sharko.dev/cert-manager: enabled
        # Arm 2 — that cluster's assignment file
        - git:
            repoURL: https://github.com/example-org/fleet-gitops.git
            revision: main
            files:
              - path: "clusters/{{ .name }}.yaml"
```

**Arm 1 — the cluster generator — decides *whether*.** It lists Argo CD's registered
clusters and keeps the ones labelled `addons.sharko.dev/cert-manager: enabled`. Sharko's
existing reconciler puts that label there, derived from `spec.addons.cert-manager.enabled`
in the cluster's assignment file. Git decides; the label is how the decision reaches Argo
CD.

**Arm 2 — the git files generator — decides *how*.** It reads
`clusters/<that cluster>.yaml` and makes the whole file available to the template. That
is where the version pin and the settings come from.

The order is forced. Arm 2's file path contains `{{ .name }}`, and `.name` only exists
once the cluster generator has run. Clusters must come first.

### 4.3 How values layering is assembled

Sharko writes no merging code for values. Helm already does it, and the engine just
lists the files in order:

```yaml
sources:
  - repoURL: https://charts.jetstack.io
    chart: cert-manager
    targetRevision: '1.12.0'
    helm:
      ignoreMissingValueFiles: true
      valueFiles:
        - $values/values/global/cert-manager.yaml
        - $values/values/clusters/prod-eu/cert-manager.yaml
  - repoURL: https://github.com/example-org/fleet-gitops.git
    targetRevision: main
    ref: values
```

Chart defaults come first because Helm always starts there. Then the global file. Then
the per-cluster file. Last wins, field by field. `ignoreMissingValueFiles: true` is what
makes absent files normal rather than an error.

The second source with `ref: values` is Argo CD's mechanism for letting a chart from one
repo read files from another. `$values/` in the paths above points at it.

This is a real simplification over v3, which pulled the addon's block out of a combined
per-cluster file and re-injected it as a Helm values string. That code disappears.

### 4.4 How per-cluster pins reach the chart version

The version is a string field, so it is filled in directly:

```yaml
targetRevision: '{{ dig "addons" "cert-manager" "version" "1.14.5" .spec }}'
```

Read as: look in the assignment file for `spec.addons.cert-manager.version`; if it is not
there, use `1.14.5` — the value Helm baked in from the merged catalog at render time.

**Why the pin had to move out of cluster-secret labels.** In v3 the pin was a label on
the cluster secret, and the template read it with
`index .metadata.labels (print .values.app "-version")`. That does not carry over into
v4 cleanly, and the reason is worth stating precisely — it is easy to get backwards.

ArgoCD's matrix generator (goTemplate mode) merges the two arms' parameter maps with
Mergo's `WithOverride` merge, called so that the FIRST-listed generator's values win on
any key both arms set, and — because `WithOverride` recurses into nested maps instead of
replacing them outright — non-conflicting nested keys from BOTH arms survive into the
merged result. Arm 1 (clusters) is listed first, so it wins conflicts; arm 2 (the git
file) only fills in what arm 1 left empty (confirmed against ArgoCD's
`applicationset/generators/matrix.go`: `g0 := getParams(Generators[0])`, then for each `a`
in `g0`, `g1 := getParams(Generators[1], seed: a)`, then for each `b` in `g1`,
`mergo.Merge(&tmp, b, WithOverride)` followed by `mergo.Merge(&tmp, a, WithOverride)` —
the second merge call is what wins).

Both arms happen to set the top-level `metadata` key, but with almost no overlapping
subkeys: the ArgoCD clusters generator's `metadata` (in goTemplate mode) carries the
cluster secret's real `labels`/`annotations`, with no `name` of its own; the git file's
`metadata` — its Sharko envelope header — carries only `name` (e.g. `prod-eu`), no
`labels`/`annotations`. The deep merge keeps both halves, so `metadata` at round two ends
up a HYBRID: its `name` subkey is the envelope's own name, while its `labels` /
`annotations` subkeys are the real cluster secret's. Nothing about that shape is a
documented ArgoCD contract — it falls out of an implementation detail of how these two
specific generators' parameter maps happen to line up, and it is not something the engine
chart should ever depend on.

So this is a hard rule for the engine, not a style preference:

> Inside a generated ApplicationSet, `metadata` at round two is a merge artifact — part
> envelope name, part real cluster-secret labels/annotations — never a clean handoff to
> either side. Never read it for anything. The only cluster identity fields the engine
> may use are `.name` and `.server`.

Pins as data are better anyway — visible in a pull request, diffable, reviewable. Version
pins live in `clusters/<cluster>.yaml`'s own `spec.addons.<name>.version`, read via `dig`
against `.spec` — never against `metadata` — regardless of which arm's data happens to
survive there.

### 4.5 How structured settings reach the Application

Some settings are not strings. `prune` is a boolean; `ignoreDifferences` is a list of
objects. An ApplicationSet's `template` block is a typed object, so `{{ }}` only works
in string fields — you cannot template a boolean or grow a list there.

Argo CD's documented answer is `templatePatch`: a second block, rendered as free-form
YAML with the same fields available, then merged onto the generated Application. It
supports conditionals and loops, which is exactly what varying lists need.

```yaml
templatePatch: |
  spec:
    {{- $s := dig "addons" "cert-manager" "settings" dict .spec }}
    syncPolicy:
      automated:
        prune: {{ dig "prune" true $s }}
        selfHeal: {{ dig "selfHeal" true $s }}
    {{- if hasKey $s "syncOptions" }}
      syncOptions:
        {{- range $s.syncOptions }}
        - {{ . }}
        {{- end }}
    {{- end }}
    {{- if hasKey $s "ignoreDifferences" }}
    ignoreDifferences:
      {{- toYaml $s.ignoreDifferences | nindent 6 }}
    {{- end }}
```

Every default in that block (`true`, `true`) was written by Helm in round one after
merging engine defaults with the shipped catalog entry. The `dig` calls only look for the
per-cluster override. So the precedence chain from section 3.3 is split across the two
rounds: rungs 1–3 resolve at Helm time, rung 4 at ApplicationSet time.

**One thing Story 2.2 must confirm:** `dig` and `hasKey` come from the sprig function
library. Argo CD's ApplicationSet controller registers sprig, but removes a few
functions. If either is missing, the fallback is `index` plus `default`, which is more
verbose and needs care around missing keys returning nil. Confirm before building on it.

### 4.6 One generated ApplicationSet, end to end

This is what Argo CD holds for cert-manager after the engine renders. Nothing here was
written by the user.

```yaml
apiVersion: argoproj.io/v1alpha1
kind: ApplicationSet
metadata:
  name: sharko-cert-manager
  namespace: argocd
  labels:
    app.kubernetes.io/managed-by: sharko
    sharko.dev/addon: cert-manager
spec:
  goTemplate: true
  goTemplateOptions: ["missingkey=zero"]
  syncPolicy:
    preserveResourcesOnDeletion: true
  generators:
    - matrix:
        generators:
          - clusters:
              selector:
                matchLabels:
                  argocd.argoproj.io/secret-type: cluster
                  addons.sharko.dev/cert-manager: enabled
          - git:
              repoURL: https://github.com/example-org/fleet-gitops.git
              revision: main
              files:
                - path: "clusters/{{ .name }}.yaml"
  template:
    metadata:
      name: 'cert-manager-{{ .name }}'
      labels:
        app.kubernetes.io/managed-by: sharko
        sharko.dev/addon: cert-manager
        sharko.dev/cluster: '{{ .name }}'
      finalizers:
        - resources-finalizer.argocd.argoproj.io
    spec:
      project: sharko-addons
      sources:
        - repoURL: https://charts.jetstack.io
          chart: cert-manager
          targetRevision: '{{ dig "addons" "cert-manager" "version" "1.14.5" .spec }}'
          helm:
            ignoreMissingValueFiles: true
            valueFiles:
              - $values/values/global/cert-manager.yaml
              - $values/values/clusters/{{ .name }}/cert-manager.yaml
        - repoURL: https://github.com/example-org/fleet-gitops.git
          targetRevision: main
          ref: values
      destination:
        server: '{{ .server }}'
        namespace: '{{ dig "addons" "cert-manager" "settings" "namespace" "cert-manager" .spec }}'
      syncPolicy:
        automated:
          prune: true
          selfHeal: true
        syncOptions:
          - CreateNamespace=true
          - ServerSideApply=true
  templatePatch: |
    spec:
      {{- $s := dig "addons" "cert-manager" "settings" dict .spec }}
      syncPolicy:
        automated:
          prune: {{ dig "prune" true $s }}
          selfHeal: {{ dig "selfHeal" true $s }}
      {{- if hasKey $s "ignoreDifferences" }}
      ignoreDifferences:
        {{- toYaml $s.ignoreDifferences | nindent 8 }}
      {{- end }}
```

Zero per-addon conditionals. Swap `cert-manager` for any other name and the shape is
identical — which is the whole claim of the engine.

The full engine implementation is Story 2.2's job. This is the target shape it builds
toward.

### 4.7 How the shipped catalog and your delta are merged

The shipped catalog lives in the engine chart's own `values.yaml`, under
`curated.addons`, keyed by addon name. Your delta arrives as a Helm values file and lands
under `spec.addons`. The chart merges them in one explicit line rather than relying on
Helm's own quiet coalescing:

```text
merged = mergeOverwrite(deepCopy(.Values.curated.addons), .Values.spec.addons)
```

Field by field, your entry wins. List fields replace whole — the same rule as section
3.3, for the same reason.

**One source, two copies, one gate.** The Sharko server also needs the shipped catalog,
to render the browse screens and to validate. So the same data exists in two places: the
binary and the engine chart. Two copies drift. The fix is the pattern the repo already
uses for JSON schemas: `catalog/addons.yaml` in this monorepo is the single source, the
engine chart's copy is generated from it at build time, and a CI job regenerates and
fails on any diff — the same shape as the existing `schemas-up-to-date` job. Story 2.4
owns wiring that up.

Note that today's `catalog/addons.yaml` carries no chart versions at all — it has
`chart`, `repo` and `default_namespace`, but no `version`. That is fine and deliberate:
a version baked into a signed chart goes stale, and staleness in a signed artefact is
worse than an absent field. Sharko writes the version into *your* delta when you switch
an addon on. The result is a genuinely useful safety property:

> **An engine pin-bump can never change a running addon's version**, because every
> deployed version is an explicit number in the user's own git.

---

## 5. Versioning — three numbers, one rule each

Three things version separately. Confusing them is easy, so here is one rule each.

| What | Where you see it | When it changes |
|---|---|---|
| The product | the Helm release of Sharko itself | Normal semver. Breaking changes only in a major. |
| The engine chart | `targetRevision` in `engine/application.yaml` | Whenever Sharko releases new deploy logic or a new shipped catalog. Only ever by a PR you merge. |
| The file format | `apiVersion: sharko.dev/v1` in your data files | Only when an existing field changes meaning. |

### The one rule for the file format

> **The `apiVersion` changes only when an older reader would get an existing file
> wrong.** Adding a new optional field never bumps it.

What follows from that:

- **A new optional setting** → engine minor version bump, format stays `sharko.dev/v1`.
  An older engine ignores the field it does not know. `sharko validate` compares your
  pinned engine version against the fields you used, and warns: "this setting needs
  engine 4.2.0 or newer; you are pinned to 4.1.0."
- **A field removed, renamed, or given a new meaning** → `sharko.dev/v2`, and Sharko
  opens one migration PR that rewrites your files. Never a hand migration.
- **A new shipped catalog entry, or a changed quirk default** → engine version bump only.
  The format is untouched.

Two safety rails already in the code carry straight into v4. The engine and every reader
must **ignore unknown fields** rather than reject them, so a repo edited by a newer
Sharko still parses. And an `apiVersion` in the `sharko.*` family that a reader does not
recognise is a **loud error, never an empty result** — the guard in
`internal/schema/envelope.go` that exists because reading a future file as "zero clusters"
once led to deleting every managed secret.

The engine chart declares which format versions it can read, as an annotation in its
`Chart.yaml`. `sharko validate` reads the pin, reads that annotation, and refuses a
mismatch with a plain sentence naming both numbers.

---

## 6. Worked end-to-end example

Two clusters. cert-manager pinned older on one of them with a known webhook quirk;
metrics-server everywhere on the catalog default.

### The repo

```text
├── engine/application.yaml
├── fleet/connections.yaml
├── catalog/addons.yaml
├── clusters/
│   ├── prod-eu.yaml
│   └── staging-us.yaml
└── values/
    ├── global/cert-manager.yaml
    └── clusters/prod-eu/cert-manager.yaml
```

### `catalog/addons.yaml`

```yaml
apiVersion: sharko.dev/v1
kind: AddonCatalogDelta
metadata:
  name: addon-catalog-delta
spec:
  addons:
    cert-manager:
      version: "1.14.5"
    metrics-server:
      version: "3.12.1"
```

### `clusters/prod-eu.yaml`

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: prod-eu
spec:
  cluster: prod-eu
  addons:
    cert-manager:
      enabled: true
      version: "1.12.0"
      settings:
        ignoreDifferences:
          - group: admissionregistration.k8s.io
            kind: ValidatingWebhookConfiguration
            jsonPointers:
              - /webhooks/0/clientConfig/caBundle
    metrics-server:
      enabled: true
```

### `clusters/staging-us.yaml`

```yaml
apiVersion: sharko.dev/v1
kind: ClusterAssignment
metadata:
  name: staging-us
spec:
  cluster: staging-us
  addons:
    cert-manager:
      enabled: true
    metrics-server:
      enabled: true
```

### `fleet/connections.yaml`

```yaml
apiVersion: sharko.dev/v1
kind: ManagedClusters
metadata:
  name: connections
spec:
  clusters:
    - name: prod-eu
      secretPath: k8s-prod-eu
      region: eu-central-1
      credsSource: secret-kubeconfig
    - name: staging-us
      secretPath: k8s-staging-us
      region: us-east-1
      credsSource: secret-kubeconfig
```

### `values/global/cert-manager.yaml` and the per-cluster override

```yaml
installCRDs: true
replicaCount: 2
```

```yaml
replicaCount: 3
```

### What comes out

| | `prod-eu` | `staging-us` |
|---|---|---|
| cert-manager version | **1.12.0** — the per-cluster pin | **1.14.5** — the catalog default |
| cert-manager namespace | `cert-manager` | `cert-manager` |
| cert-manager `replicaCount` | **3** — global 2, overridden per cluster | **2** — global only |
| cert-manager `installCRDs` | `true` — from global | `true` — from global |
| cert-manager ignore-diff | **the webhook caBundle rule** | none |
| metrics-server version | **3.12.1** — the catalog default | **3.12.1** — the catalog default |
| Argo CD Applications | `cert-manager-prod-eu`, `metrics-server-prod-eu` | `cert-manager-staging-us`, `metrics-server-staging-us` |

Two ApplicationSets exist — one for cert-manager, one for metrics-server. Each generates
two Applications. Both ApplicationSets carry
`syncPolicy.preserveResourcesOnDeletion: true`.

### The upgrade, as a pull request

Moving cert-manager on `prod-eu` up to the fleet version is one deletion:

```diff
   addons:
     cert-manager:
       enabled: true
-      version: "1.12.0"
       settings:
```

Removing the pin makes `prod-eu` follow the catalog again. Reviewable in five seconds,
revertable with `git revert`. That is the shape every fleet upgrade takes.

---

## 7. Decisions log

Every judgement call, with its reason.

**D1 — One file per cluster for assignments, at `clusters/<name>.yaml`.**
It is the file a person opens to answer "what runs here", and per-cluster files mean two
people changing two clusters never touch the same file. The path also matches the error
message the PRD already promises in Journey 5.

**D2 — Assignments are keyed by addon name, not a list.**
An addon can appear only once. A map makes that structurally true, keeps pull request
diffs to one block, and lets the engine look up an addon directly.

**D3 — The connections registry stays one file, at `fleet/connections.yaml`.**
Splitting it per cluster would cost one git API call per cluster per reconcile tick — at
50 clusters and 30 seconds that is thousands of calls an hour and a rate limit. The
assignment files are free by comparison because Argo CD reads them through its own repo
cache. Same kind (`ManagedClusters`), same shape, new path.

**D4 — Values get one file per addon per cluster, not one combined file per cluster.**
It lets Helm's own `valueFiles` ordering do the layering, which deletes v3's
extract-and-re-inject code path, and it keeps concurrent edits apart.

**D5 — The catalog delta is a new kind, `AddonCatalogDelta`, not the v3 `AddonCatalog`.**
The payload changes from a list (`spec.applicationsets`) to a map (`spec.addons`), and
the meaning changes from "the whole catalog" to "only my changes". Same `apiVersion` plus
same `kind` plus a different shape is precisely the silent-misread failure this codebase
already got burned by. A new name makes an old reader fail loudly.

**D6 — The engine takes exactly one enveloped Helm values file.**
Every enveloped file puts its payload under `spec`, so two of them merge into one `spec`
block. v3 passed three and survived on non-overlapping sub-keys. Everything else the
chart needs comes through `helm.parameters` in the same pin file, so there is no second
file to keep in step.

**D7 — The shipped catalog carries no versions; Sharko writes the version into the
user's delta.**
A version baked into a signed chart goes stale, and stale data in a signed artefact is
worse than an absent field. It also buys a real guarantee: an engine pin-bump can never
move a running version, because every deployed version is explicit in the user's git.

**D8 — Per-cluster version pins move from cluster-secret labels into the assignment
file.**
Not a preference. In a matrix generator the git arm's envelope `metadata` replaces the
cluster arm's `metadata`, so `index .metadata.labels ...` silently returns empty under
`missingkey=zero` and every pin becomes "no pin". Section 4.4 has the full mechanism.
Pins as data are also more reviewable, which is the consolation prize.

**D9 — The enable label becomes `addons.sharko.dev/<addon>: enabled`.**
The bare `<addon>: enabled` key can collide with a label the user already uses, and
brownfield takeover explicitly preserves the user's own labels alongside Sharko's. A
prefix makes "these keys are Sharko's" structurally true, which is what FR36's "re-applies
only its own addon-label keys" needs in order to be precise. The per-cluster
`<addon>-version` label disappears entirely, so the total label surface still shrinks.

**D10 — Seven settings in v1.**
`namespace`, `createNamespace`, `syncOptions`, `ignoreDifferences`, `prune`, `selfHeal`,
`preserveResourcesOnDeletion`. Each maps to one Argo CD field, each is needed by a real
addon, and none can be expressed as a Helm value. Rung 3 of the escape ladder is how the
list grows, and it grows for everybody at once.

**D11 — Deferred from v1 settings, with reasons.**
`applicationsSync` (create-only / create-update / create-delete): real, but
`preserveResourcesOnDeletion` already covers the workload-safety story, and this one is
narrow enough to wait for someone to ask. `additionalSources` and `extraHelmValues`: kept
as **catalog-level** fields exactly as in v3, so no capability is lost, but deliberately
not added to the per-cluster settings block — per-cluster extra sources is a request
nobody has made.

**D12 — List settings replace, they do not merge.**
There is no honest way to know whether a user's `ignoreDifferences` was meant to add to
the shipped list or replace it. Replacing is predictable. `sharko validate` warns when a
replacement drops a shipped rule, so the loss is visible rather than discovered in an
incident.

**D13 — `selfHeal` stays on by default.**
This is Argo CD's Application-level self-heal, which v3 already defaults to true and
which is ordinary GitOps behaviour. The PRD's "self-heal is opt-in, off by default" is
about Sharko's *own* label self-heal loop — a different mechanism at a different layer.
Section 3.2 documents both so the shared word stops being a trap.

**D14 — Structured settings use `templatePatch`, not the `template` block.**
An ApplicationSet `template` is a typed object, so `{{ }}` only substitutes into string
fields. Booleans and lists that vary per cluster have no other supported route.
`templatePatch` is Argo CD's documented answer and supports the conditionals and loops
this needs.

**D15 — Sharko applies the engine pin; the Application does not manage itself.**
Self-management is a known Argo CD pattern but it can wedge: a bad pin makes the chart
fail to render, and the Application can no longer fix its own pin. Sharko applies it at
bootstrap and re-applies on pin-bump merge through the PR-merge fan-out that already
exists. Kill-Sharko still holds — `kubectl apply -f engine/application.yaml` replaces
Sharko completely for this one object.

**D16 — Missing means empty for the registry and catalog files.**
It lets the bootstrap seed be honestly "folders, pin, README, nothing else", and it
removes a whole class of first-run error. Only empty-folder `.gitkeep` files remain, and
those exist because git cannot track an empty folder.

**D17 — One shared AppProject, `sharko-addons`, instead of one per addon.**
v3 creates an AppProject per addon with every permission set to `'*'`, so the per-addon
split provides no isolation today — just one extra object per addon. A single project is
easier to read. This is engine-internal rather than part of the data contract, so Story
2.2 may revisit it if per-addon restrictions ever become real.

**D18 — `sharko/` holds Sharko's own server config.**
`default-addons.yaml` and `marketplace-sources.yaml` keep their v3 kinds and shapes
unchanged; only the folder moves. They are not engine input and are not part of the
bootstrap seed — each is created when the setting it holds is first changed.

---

## 8. What this changes in the existing code

A candid list for the build lanes. None of it is a blocker; all of it is work someone
will meet.

**Kinds added:** `ClusterAssignment`, `AddonCatalogDelta`.
**Kinds retired:** `AddonCatalog` (the v3 list-shaped deployment catalog).
**Kinds unchanged:** `ManagedClusters`, `DefaultAddons`, `MarketplaceSources`.

1. **`internal/config/parser.go`** — `AddonCatalogSpec.ApplicationSets` is a slice of
   `models.AddonCatalogEntry`. The delta file is a map. This is a new type and a new
   reader, not an edit to the old one; the old one stays until Epic 5's migration ships.

2. **`internal/models/addon_labels.go` and everything reading `<addon>-version`
   labels** — the version-override label is gone. `internal/config/parser_test.go`
   asserts `datadog-version` parsing, and `internal/argosecrets/sync_managed_labels_test.go`
   treats it as a managed key. Both need rewriting, not deleting: the label
   *format* changes to the `addons.sharko.dev/` prefix at the same time (D9).

3. **`internal/orchestrator/types.go` — `RepoPathsConfig`** — all five path fields
   (`ClusterValues`, `GlobalValues`, `Catalog`, `Bootstrap`, `ManagedClusters`) point at
   the v3 layout. Story 4.2 re-points them. `Charts` and `Bootstrap` lose their meaning
   entirely once templates leave the repo.

4. **Hardcoded v3 paths outside `RepoPathsConfig`** — `configuration/addons-global-values`
   and `configuration/addons-clusters-values` appear as string literals in at least
   `internal/api/values_extra.go`, `values_editor.go`, `values_preview_merge.go`,
   `ai_annotate.go`, `values_unwrap_migration.go`, and `internal/ai/tools.go` +
   `agent.go`. These will not be caught by changing the config struct. Grep before
   assuming a lane is done.

5. **`internal/orchestrator/constants.go`** — `BootstrapRootAppName` is
   `cluster-addons-bootstrap` and `BootstrapRootAppPath` is `root-app.yaml`, with a test
   guarding that they match the template. Both change to the engine pin
   (`sharko-engine`, `engine/application.yaml`).

6. **`templates/bootstrap/`** — the whole tree goes away in Story 4.2, including
   `templates/addons-appset.yaml`, which becomes the starting point for the engine chart
   at `charts/sharko-engine/`. Do not delete it before Story 2.2 has lifted what it needs;
   the `ServerSideApply` reasoning and the host-cluster `in-cluster` routing both live
   there and both still matter.

7. **The host-cluster special case** — v3 rewrites the destination server to
   `https://kubernetes.default.svc` when the cluster name matches
   `SHARKO_HOST_CLUSTER_NAME`, using a placeholder substituted at bootstrap time. The
   engine now receives it as `hostCluster.name` through `helm.parameters`. The behaviour
   is unchanged; only the plumbing is.

8. **`internal/demo/mock_git.go` and `setup.go`** — the demo repo is built in the v3
   layout and is inconsistent with itself already (some paths use
   `addons-clusters-values/<cluster>/<addon>.yaml`, others use
   `addons-clusters-values/<cluster>.yaml`). Story 4.5 rebuilds it on the v4 layout,
   which incidentally fixes that.

9. **`tests/bootstraprender/testdata/`** — renders the v3 bootstrap chart. It needs to
   become an engine-chart render test against a v4 fixture repo. That fixture is the
   natural home for Story 2.2's acceptance test.

10. **`sprig` availability in the ApplicationSet controller** — the design uses `dig` and
    `hasKey` in `templatePatch`. Argo CD registers sprig but removes some functions.
    Verify both before building on them; the fallback is `index` plus `default`, which
    needs care because `index` on a missing key returns nil and dereferencing that
    errors.

---

## 9. Documents this builds on

- `.bmad/output/planning-artifacts/prd-sharko-v4.md` — FR2, FR19, FR22–FR25, FR33, and
  the versioning section
- `.bmad/output/planning-artifacts/epics-v4.md` — Epic 2 Stories 2.1–2.6, Story 4.2
- `internal/schema/envelope.go` — the envelope, the kind constants, the unknown-version
  guard
- `templates/bootstrap/templates/addons-appset.yaml` — the v3 matrix pattern this
  replaces
- Argo CD ApplicationSet documentation — matrix generator, git files generator,
  `templatePatch`, `syncPolicy.preserveResourcesOnDeletion`, `syncPolicy.applicationsSync`
  (all verified against current Argo CD docs while writing this)
