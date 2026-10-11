# Why use Sharko?

If you already run ArgoCD for deploying workloads, you might wonder what Sharko adds. This page explains what Sharko does and why you'd choose it over building the same thing yourself.

For a comparison with specific tool categories (GitOps promoters, secret-delivery tools, cluster-fleet managers), see [Sharko vs. the Alternatives](comparison.md).

## What Sharko does

Sharko manages which addons run on which clusters. It keeps that information in Git and makes sure ArgoCD deploys those addons.

When you register a cluster or enable an addon through Sharko (via UI, API, or CLI), Sharko:

1. Validates the change
2. Shows you exactly what files will change
3. Opens a pull request
4. After you merge, writes labels on the ArgoCD cluster Secret that tell ArgoCD which addons to deploy there

**Sharko never deploys workloads.** ArgoCD deploys the actual addon workloads by reading those labels. Sharko just writes the labels.

## The two loops

Sharko and ArgoCD each own a different job. They never touch the same Kubernetes object.

**Sharko's job:** read Git, write labels on the ArgoCD cluster Secret (in the `argocd` namespace). The labels say which addons belong on which cluster.

**ArgoCD's job:** read those labels, deploy the addon workloads on the clusters.

Same Git as truth. Different jobs. No overlap.

## What Sharko is replacing

Many teams build a custom repository to manage fleet-wide addons. That repo typically has:

- A YAML file listing which clusters exist and which addons go where
- An ArgoCD ApplicationSet with a matrix generator (clusters × addons)
- External Secrets Operator setup to fetch credentials
- Custom naming conventions for values files

**Sharko is that repo as a product.** You get the same result — Git as truth, ArgoCD deploys — without building the bootstrap ApplicationSet, the matrix generator, the ESO wiring, or the file layout yourself.

And you get:

- **UI + API + CLI** instead of hand-editing YAML
- **Catalog** to browse addons instead of knowing them by heart
- **Preview before every change** — see exactly what this PR will do
- **Pull requests for every change** with a readable summary and recorded actor
- **Works without ESO** — teams without a secret store can still use it
- **Lower barrier** — someone who doesn't know GitOps deeply can run a fleet

## If you remove Sharko

ArgoCD keeps running. Your workloads stay deployed. The ArgoCD ApplicationSets stop seeing new addon labels (because Sharko was writing them), but existing Applications keep syncing.

For a full teardown guide, see [Removing Sharko (no lock-in)](../operator/removing-sharko.md).

## What Sharko is not

- **Not a general-purpose GitOps rival to ArgoCD.** Sharko is a narrow controller for cluster registration and addon labels. It never touches workloads.
- **Not a replacement for ArgoCD.** Sharko requires ArgoCD. It runs on top of ArgoCD.
- **Not SaaS.** Sharko is a local install, like ArgoCD. You run it in your own cluster.

## Who should use Sharko

Sharko is for ArgoCD users who **don't already have the hand-built setup.** If you're about to build an addon-management repository with ESO + ApplicationSets + custom conventions, Sharko gives you that result out of the box.

If you already run a mature setup with 50+ clusters, you probably won't rip it out. Sharko is for the team about to hand-build it.

## What's next

- [Managing Clusters](clusters.md) — registration, connection types, credentials
- [Managing Addons](addons.md) — enabling, upgrading, per-cluster values
- [Drift Detection and Sync](drift-and-sync.md) — how Sharko detects and fixes out-of-band edits
