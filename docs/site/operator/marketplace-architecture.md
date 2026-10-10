# Marketplace and Catalog — the two words, and what each one means

Sharko uses exactly two words for addons, everywhere: in the UI, in the
API, and in these docs. There is no third word. This page explains what
each one means, why there are two, and who is responsible for what.

> The Marketplace is what you could run. The Catalog is what your org
> allows. Your clusters run only what's enabled from the Catalog.

## Marketplace — what you could run

The Marketplace is a screen for looking, not for changing anything. It
shows every addon Sharko knows about (name, description, chart location,
license, maintainers, known problems) so you can look before you decide.
Nothing in the Marketplace is deployed, and nothing in it is running
anywhere. It's the menu, not the order.

The Marketplace always shows Sharko's built-in list: a few dozen common
addons, such as cert-manager, ingress-nginx and Prometheus. It is kept in
one file in Sharko's own repository,
[`catalog/addons.yaml`](https://github.com/MoranWeissman/sharko/blob/main/catalog/addons.yaml).
Anyone can suggest an addition or a fix to that file. See
[Contributing a Catalog Entry](../community/contributing-catalog-entries.md).

You can also add your own lists to the Marketplace, for example your
company's internal charts. Each one is an `https://` address of a file in
the same format as the built-in list. See
[Extra Marketplace sources](catalog-sources.md).

The Marketplace is read through `GET /api/v1/marketplace/addons` and the
other Marketplace routes. None of them write anything to your GitOps
repository.

## Catalog — what your org allows

The Catalog is your org's own approved list, kept as one file in your own
GitOps repository: `catalog.yaml`. An addon is only in your Catalog
because someone on your team put it there: by clicking "Add to catalog"
on a Marketplace entry, by filling in a form for your own in-house chart,
or by editing `catalog.yaml` by hand. All three give the same result: a
pull request that adds a full entry (chart, repository, version,
namespace and settings) to `catalog.yaml`. Whoever reviews that pull
request sees exactly what is coming into the org, because the whole entry
is right there in the change.

A brand new Sharko install has an **empty** Catalog. Nothing runs in your
org that nobody in your org chose. That is true from the very first
install, not just after you've tidied things up.

## Enabling — what's actually running

Being in the Catalog does not mean an addon is running anywhere. A cluster
only runs an addon once someone enables it there, which writes an entry to
that cluster's own `cluster-addons/<name>.yaml` file. **You can only
enable an addon that is already in `catalog.yaml`.** From the Marketplace,
"add to catalog and enable on this cluster" is offered as one pull
request that changes both files at once, so the reviewer sees both
changes together and one merge makes both true. You don't have to combine
them. Do it as two separate reviewed changes if that's how your org
prefers to work.

## Curated means the details are right, not that it is safe

The built-in Marketplace list goes through Sharko's own review. That
means one thing: the Sharko project checked that the chart address is
right, the defaults make sense, and the entry matches what the chart
really does. It does **not** mean Sharko has read the addon's source
code, tested it for security problems, or promises it is safe to run in
your environment. Every entry's page shows where it comes from (the chart
repository and the project's own homepage) so you can go and look
yourself.

The same goes for any extra source you add: Sharko shows what is in it,
and does not promise it is safe. A **Verified** badge only means Sharko
checked who signed the entry. See
[Marketplace signature settings](catalog-trust-settings.md).

The decision about safety belongs to your org. It is made when someone
reviews and merges the pull request that adds the entry to
`catalog.yaml`. That is the whole point of having two words: the
Marketplace can show you things without promising anything about them,
because nothing in it can run until your own reviewer says yes.

## Approval is the only way in, for every source

There is no automatic path from "in the Marketplace" to "running in your
fleet". Not for the built-in list, and not for any extra source you add.
Every addon, from anywhere, comes into your org the same way: a pull
request to `catalog.yaml` that a person on your team reviews and merges.
No source is trusted enough to skip that, and there is no option to copy
addons in automatically. That will not change as more sources are added.
It is what makes the Marketplace safe to browse in the first place.

## What is not built

- **A bot that suggests addons for your org's Catalog.** Nothing scans
  outside sources for you and opens pull requests on *your* `catalog.yaml`.
  The Sharko project has its own tool for suggesting additions to
  Sharko's built-in list (see the
  [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md)).
  That tool only runs when a Sharko maintainer starts it by hand, it
  only proposes changes to Sharko's own file, and it never touches your
  repository.

If something like this is ever built, the rule above still holds: it
could only open a pull request for a person to review.

## Related pages

- [Contributing a Catalog Entry](../community/contributing-catalog-entries.md): how to suggest an addition to the built-in Marketplace list
- [Extra Marketplace sources](catalog-sources.md): adding your own lists to the Marketplace
- [The marketplace-sources.yaml file](marketplace-sources-config.md): listing those sources in Git
- [Catalog Scan Runbook](../developer-guide/catalog-scan-runbook.md): the tool that helps keep Sharko's own built-in list up to date
- [Managing Addons](../user-guide/addons.md): day-to-day adding, enabling and upgrading
- [Marketplace](../user-guide/marketplace.md): browsing addons in the UI
