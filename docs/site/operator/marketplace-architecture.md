# Marketplace and Catalog — the two words, and what each one means

Sharko uses exactly two words for addons, everywhere — in the UI, in the
API, and in these docs. There is no third word. This page explains what
each one means, why the split exists, and who is responsible for what.

> The Marketplace is what you could run. The Catalog is what your org
> allows. Your clusters run only what's enabled from the Catalog.

## Marketplace — what you could run

The Marketplace is a read-only browse screen. It shows every addon Sharko
knows about — name, description, chart location, license, maintainers,
known quirks — so you can look before you decide. Nothing in the
Marketplace is deployed, and nothing in the Marketplace is running
anywhere. It's the menu, not the order.

The built-in Marketplace list is a curated set of a few dozen common
addons (cert-manager, ingress-nginx, Prometheus, and others), shipped with
Sharko and kept in one file in this project's own repository:
[`catalog/addons.yaml`](https://github.com/MoranWeissman/sharko/blob/main/catalog/addons.yaml).
Anyone can propose an addition or a fix to that file — see
[Contributing a Catalog Entry](../community/contributing-catalog-entries.md).

The Marketplace is read through `GET /api/v1/marketplace/addons` and the
related `/api/v1/marketplace/*` routes. None of those routes write
anything to your GitOps repository.

## Catalog — what your org allows

The Catalog is your org's own approved list, kept as one file in your own
GitOps repository: `catalog.yaml`. An addon only exists in your Catalog
because someone on your team put it there — by clicking "Add to catalog"
on a Marketplace entry, by filling in a form for your own in-house chart,
or by hand-editing `catalog.yaml` directly. All three paths produce the
same thing: a pull request adding a full entry — chart, repo, version,
namespace, and settings — to `catalog.yaml`. Whoever reviews that pull
request sees exactly what is entering the org, because the whole entry is
right there in the diff.

A brand new Sharko install has an **empty** Catalog. Nothing runs in your
org that nobody in your org chose — that holds from the very first
bootstrap, not just after you've cleaned things up.

## Enabling — what's actually running

Being in the Catalog does not mean an addon is running anywhere. A cluster
only runs an addon once someone enables it there, which writes an entry to
that cluster's own `clusters/<name>.yaml` file. **Enabling now requires
catalog membership** — you cannot turn an addon on for a cluster unless
it is already in `catalog.yaml`. From the Marketplace, "add to catalog and
enable on this cluster" is offered as a single pull request that touches
both files at once, so the reviewer sees both changes together and one
merge makes both true. Nothing forces you to combine them — do it as two
separate reviewed changes if that's how your org prefers to work.

## Curated means correct, not audited

The built-in Marketplace list going through Sharko's own review process
means one specific thing: the Sharko project checked that the chart
address is right, the defaults are sane, and the entry matches what the
chart actually does. It does **not** mean Sharko has audited the addon's
source code, tested it for security issues, or vouches for it being safe
to run in your environment. Every entry's page shows its provenance — the
chart repository, the project's own homepage — so you can go look for
yourself.

The safety decision belongs to your org, made at the moment someone
reviews and merges the pull request that adds the entry to `catalog.yaml`.
That is the whole point of the two-word split: the Marketplace can show
you things without vouching for them, because nothing in it can run until
your own reviewer says yes.

## Approval is the only door in — for every source, forever

There is no automatic path from "exists in the Marketplace" to "running
in your fleet." Not for the built-in list, not for any future source.
Every addon, from anywhere, enters your org the same way: a pull request
to `catalog.yaml` that a person on your team reviews and merges. There is
no trusted-source bypass and no auto-sync option, and that will not
change as more sources are added — it's the property that makes the
Marketplace safe to browse in the first place.

## Roadmap — documented, not built

Two things are planned but do not exist yet in this release:

- **Pointing the Marketplace at your own chart index.** Today the
  Marketplace only shows the built-in curated list. Letting an org add
  its own internal chart index as a second Marketplace source is planned,
  not built.
- **An automated discovery bot for your org's Catalog.** A bot that scans
  upstream sources on your behalf and opens pull requests proposing
  additions to *your* `catalog.yaml` is planned, not built. (This is
  different from the internal tool that helps maintain Sharko's own
  built-in list — see the
  [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md) — which
  already exists but only touches this project's own curated file, never
  your org's repository.)

Whenever either ships, the approval-is-the-only-door rule above still
applies without exception.

## Related pages

- [Contributing a Catalog Entry](../community/contributing-catalog-entries.md) — how to propose an addition to the built-in Marketplace list
- [Third-party Catalog Sources](catalog-sources.md) — configuring extra Marketplace sources today
- [Marketplace Sources Configuration](marketplace-sources-config.md) — the `configuration/marketplace-sources.yaml` file schema
- [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md) — the bot that helps maintain Sharko's own built-in list
- [Managing Addons](../user-guide/addons.md) — day-to-day add / enable / upgrade workflow
- [Marketplace](../user-guide/marketplace.md) — browsing and discovering addons in the UI
