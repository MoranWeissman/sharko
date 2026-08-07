# Sharko vs. the Alternatives

Sharko is not the only way to run addons across a fleet of clusters on top of ArgoCD. This page is an honest look at the other paths — when hand-building it yourself is the right call, where the neighboring tool categories fit, and who should skip Sharko entirely.

None of the tools named below are compared feature-by-feature. They solve different problems, or the same problem at a different layer. The goal here is to help you place Sharko correctly, not to score points against anyone.

## The DIY path: hand-built ApplicationSets and app-of-apps

If your platform team already knows ArgoCD well, you can build fleet-wide addon management yourself: an `ApplicationSet` with a cluster generator, labels on ArgoCD cluster secrets to say which addons go where, per-cluster values files, and (if you need secrets) External Secrets Operator wired up to your secrets store. This is the [gitops-bridge](https://github.com/gitops-bridge-dev/gitops-bridge) pattern, and plenty of teams run it in production.

**When DIY is genuinely the right call:**

- You have one or a handful of clusters, and the addon set barely changes. The overhead of learning a new tool costs more than the problem it solves.
- Your platform team already has deep ArgoCD expertise and a working setup. Ripping out something that works to adopt Sharko is rarely worth it.
- You need templating flexibility that goes beyond "which chart, which version, which values" — see "Deeply custom templating needs" below.

**Where it starts to hurt:** as the fleet and the addon list grow, the hand-built `ApplicationSet` matrix, the label conventions, and the ESO wiring become something only the person who built them fully understands. Every new addon is a small PR into infrastructure YAML. Every new teammate has to learn your specific repo's conventions before they can safely touch it. Sharko exists for the team that is about to build this and would rather not maintain it by hand — see [Why Sharko, not just an ArgoCD Application?](why-sharko.md) for the full case.

## Neighboring tool categories

These categories solve real, adjacent problems. Some of them can be used *alongside* Sharko; none of them replace what Sharko does.

### GitOps promoters (for example, Kargo)

Tools like [Kargo](https://kargo.io/) manage **promotion** — moving a validated change through environments (dev → staging → prod) with checks and gates along the way. That's a different question from Sharko's: Sharko answers "which addons, at which versions, run on which clusters," not "should this change be allowed to move from staging to prod yet." A team using Kargo to gate promotions and Sharko to manage the addon fleet on each of those environments is a reasonable combination, not a conflict.

### Secret-delivery tools (for example, External Secrets Operator)

[External Secrets Operator](https://external-secrets.io/) and similar tools run an in-cluster controller that continuously pulls secret values from a secrets store (AWS Secrets Manager, Vault, and others) and keeps a Kubernetes `Secret` in sync with it. This is a general-purpose job — any workload's secrets, not just addon credentials.

Sharko's own secret sync overlaps with this category, but narrower: it delivers **addon credentials specifically** — the values an addon in Sharko's catalog needs to run — by reading from your secrets store and pushing the value into the cluster itself, on a schedule. It does not manage arbitrary application secrets, and it was built as an option for teams that don't already run ESO, not as a replacement for it. If you already run ESO, you can leave Sharko's addon-secret delivery switched off and keep using ESO for that job — see [Secret Sync](secret-sync.md) for the on/off switch and exactly what Sharko's version does and doesn't do.

### Cluster-fleet managers (for example, Sveltos, Rancher Fleet)

Tools like [Sveltos](https://github.com/projectsveltos/sveltos) and [Rancher Fleet](https://fleet.rancher.io/) manage the fleet layer itself — registering clusters, distributing arbitrary manifests or Helm charts to groups of them, sometimes without requiring ArgoCD at all. They solve a broader problem than Sharko does: "get any workload onto any cluster," not "manage a curated addon catalog on top of ArgoCD." Sharko assumes ArgoCD is already your deployment engine and adds an addon-specific layer on top of it — a catalog, an upgrade advisor, a UI and API safe for people who didn't write the GitOps repo, and an audit trail. If you need general-purpose multi-cluster manifest distribution without ArgoCD in the picture, that's a different category of tool.

### GitOps-bridge-style glue repos

The [gitops-bridge](https://github.com/gitops-bridge-dev/gitops-bridge) project (and similar reference repos) is not a tool you install — it's a pattern for wiring cluster metadata into ArgoCD `ApplicationSet`s by hand. Sharko implements the same underlying pattern (cluster labels driving `ApplicationSet` generators) but packages it as a product: a versioned, signed engine chart instead of a repo you copy and maintain yourself, plus the UI, API, catalog, and audit trail on top. If you've already built something in this style and it works for you, there's no urgency to switch — Sharko is for the team that's about to build it.

## Who should NOT use Sharko

Sharko is a good fit for teams that want a productized version of the "curated addon fleet on top of ArgoCD" pattern. It is not a good fit for everyone:

- **Teams with deeply custom templating needs.** Sharko's engine chart deliberately has zero per-addon conditionals — every catalog entry renders through the same generic path. If your addons need conditional logic, custom Helm post-processing, or anything beyond "chart + values + version," you'll be fighting Sharko's model instead of using it.
- **Single-cluster hobbyists.** If you run one cluster, the entire premise of "manage addons consistently across a fleet" doesn't apply to you. A plain `helm install` or a single ArgoCD `Application` per addon is simpler and has nothing to learn.
- **Shops happy with their hand-rolled setup.** If your `ApplicationSet` matrix and ESO wiring already work, are understood by your team, and aren't causing pain, switching to Sharko is a migration for its own sake. Sharko's target user is the team about to build that setup, not the team that already has and likes one.

## Where to go next

- [Why Sharko, not just an ArgoCD Application?](why-sharko.md) — the full case for Sharko over the DIY path
- [If You Remove Sharko](../operator/removing-sharko.md) — what happens if you decide Sharko isn't for you after all
