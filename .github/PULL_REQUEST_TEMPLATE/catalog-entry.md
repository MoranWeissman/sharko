<!--
Thanks for proposing an addon for Sharko's curated catalog! This template
walks through the fields catalog/addons.yaml expects. See the full guide:
docs/site/community/contributing-catalog-entries.md

Delete this comment block once you've filled in the sections below.
-->

## Summary

<!-- Which addon(s) does this PR add or update? One line per addon. -->

## Checklist

- [ ] The entry lives in `catalog/addons.yaml`, under `addons:`, in
      alphabetical order by `name`.
- [ ] `name` is unique in the file, lowercase, DNS-safe (letters, digits,
      hyphens only), and matches the Helm chart's own common name.
- [ ] `description` is one plain-English sentence — what the addon does,
      not how it works internally.
- [ ] `chart` + `repo` point at a real, publicly reachable Helm repo
      (`https://` or `oci://`) — a maintainer will run `helm repo add` +
      `helm show chart` against them before merging.
- [ ] `default_namespace` is the namespace the chart normally installs
      into.
- [ ] `license` is the chart's SPDX identifier (e.g. `Apache-2.0`,
      `MIT`). Licenses outside the allow-list (Apache-2.0, BSD-3-Clause,
      MIT, MPL-2.0) still merge, but get flagged for a maintainer to
      read the license text before merge.
- [ ] `category` is exactly one of: `security`, `observability`,
      `networking`, `autoscaling`, `gitops`, `storage`, `database`,
      `backup`, `chaos`, `developer-tools`.
- [ ] `curated_by` lists at least one of: `cncf-graduated`,
      `cncf-incubating`, `cncf-sandbox`, `aws-eks-blueprints`,
      `azure-aks-addon`, `gke-marketplace`, `artifacthub-verified`,
      `artifacthub-official` — i.e. this addon is independently listed
      somewhere else first. Sharko's catalog doesn't originate curation
      decisions, it aggregates them.
- [ ] `maintainers` names at least one real maintainer or org (e.g. the
      chart's own maintainers field, or the org that publishes it).
- [ ] I ran `go run ./cmd/sharko validate-catalog catalog/addons.yaml`
      locally and it printed `OK`.

## Optional fields (fill in what you know; delete what doesn't apply)

- [ ] `docs_url` / `homepage` / `source_url` — links a user would
      actually want.
- [ ] `min_kubernetes_version` — only if the chart itself documents a
      floor.
- [ ] `required_values` — one entry per Helm value a user MUST set
      before the addon actually works, each with a plain-English
      `description`. Skip this section entirely if the chart works with
      zero required configuration.
- [ ] `secrets` — one entry per credential/secret the addon needs to
      function (not to install — to actually work), each with a
      plain-English `description` of what it's for and how it's
      normally supplied.
- [ ] `quirks` — short, plain-English sentences about known operational
      gotchas. Free text, not structured settings — e.g. "the webhook's
      CA bundle is rewritten on every reconcile, ignore it in Argo CD or
      every sync looks dirty." Skip if you don't know of any.

## Why this addon

<!--
A sentence or two on why this belongs in the curated set — e.g. "widely
used for X, already listed in the AWS EKS Blueprints add-ons catalog."
-->

## Test plan

- [ ] `go run ./cmd/sharko validate-catalog catalog/addons.yaml` passes
- [ ] `make test` passes
