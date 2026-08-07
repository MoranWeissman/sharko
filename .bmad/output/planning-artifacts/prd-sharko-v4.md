---
stepsCompleted: ['step-01-init', 'step-02-discovery', 'step-02b-vision', 'step-02c-executive-summary', 'step-03-success', 'step-04-journeys', 'step-05-domain', 'step-06-innovation', 'step-07-project-type', 'step-08-scoping', 'step-09-functional', 'step-10-nonfunctional', 'step-11-polish', 'step-12-complete']
status: COMPLETE (2026-07-29) · AMENDED 2026-08-08 (catalog model — see the Amendment note under the title)
inputDocuments:
  - .bmad/output/architecture/2026-07-25-sharko-oss-professional-design.md
  - .bmad/output/brainstorming/2026-07-24-blind-vs-biased-diff.md
  - .bmad/output/brainstorming/2026-07-24-neutral-factsheet.md
  - .bmad/output/brainstorming/2026-07-25-operator-ab-factsheet.md
workflowType: 'prd'
classification:
  projectType: self-hosted infrastructure product with a first-class API (web UI + CLI as clients of the same API; runs on the user's hub cluster next to ArgoCD; NOT SaaS, NOT a library)
  domain: DevOps / Kubernetes infrastructure (GitOps tooling)
  complexity:
    operationalRisk: high (cluster credentials, secrets, destructive fleet operations like ArgoCD prune)
    compliance: low (unregulated; security honesty carries the weight compliance would elsewhere)
  projectContext: brownfield — shipped public product (v3.0.0); this PRD is a FULL-product redefinition for the v4 release train (promises, not endpoint inventory) + a short delta-from-v3 appendix
  extraDimensions:
    ecosystemDependency: built on ArgoCD's stable public contracts only (cluster Secret format, ApplicationSets, Application status); PRD must state supported ArgoCD versions
    selfHostedLifecycle: install / upgrade / migrate / uninstall (kill-Sharko exit) are product features
    audiences: human platform engineers (UI) + machines (IDP/CI via API); every journey covers both
documentCounts:
  briefs: 0
  research: 0
  brainstorming: 4
  projectDocs: 0
---

# Product Requirements Document - Sharko

**Author:** Moran
**Date:** 2026-07-29 · **Amended:** 2026-08-08

> **Amendment (2026-08-08) — the catalog model changed after this PRD was first completed.**
> On 2026-07-31 the "delta" catalog model was replaced by the **approved-list model**, and the catalog sections below were updated to match. What the approved-list model means: the org's catalog file holds the **full, self-contained list of addons the org approved** — chart, chart repo, version, namespace, settings, needed secrets — and nothing is inherited from a list shipped inside Sharko. The curated list still exists, but only as a **read-only discovery window (the "Marketplace")**: it decorates approved entries with display knowledge — description, docs link, known quirks, required values — and it never adds an addon to the org's catalog and never supplies a deployment field. Why the change: the delta model showed every curated addon to everyone with no way to opt out, so 45 addons nobody chose looked approved. Design doc: `2026-07-31-catalog-approved-model`.

## Executive Summary

**Sharko is the GitOps addon manager for Argo CD.** It gives a platform team one product for managing addons — Datadog, cert-manager, Kyverno, and so on — across a fleet of Kubernetes clusters: see everything, change anything through a reviewed pull request, upgrade with guidance.

**The problem it solves.** Teams that run addons across many ArgoCD clusters manage them through a git repo full of templates only experts can touch. There is no self-service for the rest of the team, no fleet view without grep, and no API — so a team building an internal platform (IDP) ends up writing scripts that edit YAML files, badly, from scratch. The knowledge of "what runs where, what's outdated, what needs which values" lives in one expert's head.

**How Sharko works.** Three doors, one truth. Humans use a web UI; machines (an IDP, a pipeline) call a REST API; a CLI wraps that same API — every door does the same thing: validate the request, show a preview, and open a pull request to the user's git repo. Git stays the only source of truth. ArgoCD deploys what git says. Sharko's engine — the ApplicationSet logic that turns small data files into deployments — ships as a versioned, signed, public OCI Helm chart that Sharko maintains, so the user's repo holds only readable data files and one version pin. Behind the scenes, three reconcile loops keep reality matching git: cluster connection secrets, addon assignment labels, and addon secrets synced from a vault. Sharko is an operator whose desired state lives in git instead of in custom resources.

**Who it's for.** Platform and DevOps engineers running roughly 10–50+ ArgoCD-managed clusters, where the fleet is bigger than one expert — and platform teams wiring addon management into a portal. It is honestly not for: shops with deeply custom template logic (the raw ApplicationSet pattern serves them better, and the docs say so), or single-cluster hobbyists.

### What Makes This Special

- **It knows what an addon is.** ArgoCD knows Applications. Helm knows charts. Git knows files. Sharko is the only layer where "addon" is a real thing: a catalog entry that carries the addon's knowledge — where its chart lives, what versions exist, what values it needs, which secrets it expects — plus fleet-wide assignment, versions, and upgrade advice. That's what makes the signature moment possible: *cert-manager 1.12 on 14 clusters, 1.14.5 available — one reviewed PR upgrades them all.*
- **A clean exit, provable.** Removing Sharko breaks nothing that is running and strands no data. Everything it creates is standard Kubernetes and git resources — plain secrets, plain labels, readable YAML — manageable by hand or with standard tools. You lose the automation, not your fleet.
- **An ecosystem tool, honestly.** Sharko does not replace ArgoCD or hide it. It integrates only through ArgoCD's stable public contracts — the cluster Secret format, ApplicationSets, Application status — the same pattern as Terragrunt on Terraform or Kargo on Argo CD. "Sharko proposes, ArgoCD enforces."
- **Declarative without CRDs.** Same reconcile loops, drift detection, and self-heal an operator gives — but the "resources" are git files anyone can read, and the review gate is a pull request.

## Project Classification

**Type:** self-hosted infrastructure product with a first-class API — a Go server + web UI + CLI, installed by Helm on the user's hub cluster next to ArgoCD. Not SaaS, not a library. UI and CLI are clients of the same API.
**Domain:** DevOps / Kubernetes infrastructure (GitOps tooling). Unregulated.
**Risk profile:** operational risk **high** (holds cluster credentials, syncs secrets, can trigger fleet-wide changes including pruning); compliance burden **low** (none).
**Context:** brownfield — a shipped public product (v3.0.0, ~170K lines Go, ~60K lines TypeScript). This PRD is the full-product definition for the v4 release train, whose anchor is one breaking change: the engine moves from templates copied into the user's repo to a versioned OCI chart. A delta-from-v3 appendix closes the document.
**Standing constraints:** built on ArgoCD's stable public contracts only (supported ArgoCD versions stated in docs); install, upgrade, migrate, and uninstall are product features; every user journey covers both audiences — humans and machines.

### Planning notes — details the requirements compress (for the epics & stories phase)

- **Verify at planning:** whether current catalog default-seeding copies curated entries into the user's file (superseded by the 2026-08-08 amendment — the migration now writes a full approved list, not a delta) · that the catalog write path (POST/PATCH/DELETE /api/v1/addons — verified to exist) is fully git-native · what RBAC widening the takeover preflight needs to read Applications/ApplicationSets · whether the preflight joins the `sharko doctor` family · that the multi-cluster single-PR upgrade (FR27) matches the shipped code.
- **Catalog entry fields:** name, description, chart location (repo/OCI), version source, required-values list with plain descriptions, optional defaults, docs link, needed secrets. Deliberately NO per-addon JSON schemas.
- **Deployment-settings schema** is versioned with the engine. Escape ladder when a setting is missing: (1) Helm values, (2) declared settings, (3) request the field — a small engine release lands it for everyone, (4) truly bespoke logic → the raw ApplicationSet pattern (the docs say so).
- **Naming** *(amended 2026-08-08)*: "Catalog" = the org's approved list. "Marketplace" = only the read-only curated discovery window. (The original note here said "Catalog everywhere, Marketplace dropped" — that predates the approved-list model, which gave the discovery window its own surface and name.)

## Success Criteria

### User Success

- A platform engineer goes from `helm install` to their first addon deployed — cluster registered, addon enabled, PR merged, ArgoCD synced — in under 30 minutes, without ever reading about ApplicationSets or templates.
- A teammate who is not the fleet expert enables or upgrades an addon through the UI, safely, without help — validation and preview catch mistakes before the PR opens.
- An IDP team integrates through the API without writing a single YAML-editing script.
- The signature moment works: see outdated addons across the fleet → one action → one reviewed PR upgrades N clusters. Rollback is `git revert`.
- When something breaks, the user sees what, where, and why in plain words (file, reason, line where possible), in Sharko or one click into ArgoCD.

### Business Success (the maintainer's actual goals — not vanity metrics)

- Deliberately NOT measured: GitHub stars, adopter counts, download numbers.
- Coherence: every visible feature serves the core (catalog, assignment, matrix) or a door (UI/API/CLI). Zero half-built surfaces ship.
- Honesty: every claim in the docs is true in the code. Documented = works; doesn't work = not documented.
- Showcase quality: the repo reads as professional work end to end, regardless of who adopts it.
- Contribution is easy: adding a catalog entry is a template-guided PR anyone can make.

### Technical Success

- The full loop is provable on a laptop: kind playground → register → enable → PR → merge → deployed → visible in the UI. Zero cloud cost.
- Kill-Sharko holds: delete Sharko → nothing running breaks, no data stranded, only automation lost. Verified each release.
- The engine law holds: a v4 user repo contains zero template files — only data files and one version pin. Engine upgrades are pin-bump PRs. Migration from v3 is exactly one Sharko-opened PR.
- Honest timing promises: label drift detected ≤30s; addon-secret rotation ≤5min default (configurable); version freshness always shows "last checked."
- Bad input never wins: malformed files never crash Sharko, never half-apply, always surface a named error.
- Quality gates stay green every release: build, CI-honest tests, helm lint, strict docs build, perf gate (no >20% p99 regression).

### Measurable Outcomes

- Quickstart ≤30 min; recorded full-loop demo ≤3 min.
- 100% of documented features pass the claims-vs-code audit.
- 0 template files in a v4 user repo; migration = 1 PR.
- v4 ships with operator and scanner parked — 0 half-built visible surfaces.

## Product Scope

### MVP - Minimum Viable Product (the v4.0.0 release)

Mostly a redefinition release — much is already built; the anchor is the engine change:

- Engine as versioned OCI chart + the one-PR migration from v3 + prune-safety knobs in the engine's ApplicationSets.
- Data-only user repo (per-cluster assignment files, values files, engine pin).
- Deployment-settings-as-data (catalog defaults + user overrides + generic engine pass-through).
- Catalog: approved-list model *(amended 2026-08-08)* — the org's file holds full, self-contained entries; the curated list is a read-only discovery window supplying display knowledge only — plus entry extensions (required-values list, needed secrets), internal addons first-class, existing write-back kept and verified git-native, community contribution path (PR template + guide).
- `sharko validate` with line numbers; doors validation/preview (exists).
- Drift detection + opt-in self-heal (exists); version matrix + upgrade intelligence (exists); freshness "last checked" surfaced.
- The honesty pass: SBOM real or claim dropped; API-key expiry; threat model surfaced; arm64 fixed; docs lead with K8s-Secrets; multi-tenancy documented as an honest limit (not built in v4).
- Docs rewritten to the new identity, including an honest comparison page (vs gitops-bridge, Sveltos, raw ApplicationSets). Kill-Sharko doc re-verified.
- AI: both features stay (RESOLVED 2026-07-29, code-checked): upgrade intelligence is deterministic — the AI only adds an optional plain-language summary on top and degrades gracefully without a key; the chat assistant enforces the same permissions and PR gate as the REST API (a client of the same API and the same gate — not a new door and not a bypass; the product has three doors, full stop). Both opt-in behind the user's API key, off the headline, honest AI-transparency note in docs.

### Growth Features (Post-MVP)

ArgoCD UI extension (the "native feel" card) · argocd-notifications reuse · AppProject awareness · brownfield import helper · multi-ArgoCD / multi-tenancy · operator revisited only with full commitment · scanner revisited · community catalog growth.

### Vision (Future)

The addon layer that ArgoCD fleets simply have — possibly living in argoproj-labs one day — with a community-fed catalog. Quality earns that, or it doesn't happen. Both outcomes are fine.

## User Journeys

### Journey 1 — Dana, platform engineer: day zero to first addon

**Where we meet her:** Dana runs 14 EKS clusters with ArgoCD. Addon management is a repo full of templates she inherited from someone who left. She's the only one who dares touch it. She finds Sharko and is skeptical — another tool promising magic.

**Rising action:** Her security instinct kicks in first: she pulls the engine chart from the registry, reads the templates, renders them locally, and reads the "removing Sharko" doc. Nothing hidden, standard resources, clean exit — okay, it earns a trial. She helm-installs Sharko on the hub, connects her git repo and ArgoCD in the setup screen, and merges Sharko's bootstrap PR — empty data folders and one engine pin, nothing else in her repo.

**Climax:** She starts with her NEWEST cluster — just provisioned, no addons wired yet, nothing to fight. She registers it with credentials she already has, picks Kyverno from the catalog, sees the exact preview of the files Sharko wants to create, and merges the PR. ArgoCD deploys. The dashboard goes green. Twenty-five minutes, and she never wrote a line of YAML.

**Resolution:** The new cluster proves the loop. Her 13 existing clusters come over one at a time, following the migration playbook (Journey 6) — no big-bang switch, never two systems fighting over one thing.

### Journey 2 — Sam, the teammate who isn't the expert: safe self-service

**Where we meet him:** Sam is a capable DevOps engineer, but the addon repo was always "ask Dana first." He needs external-dns on staging-us, today, and Dana is on vacation.

**Rising action:** He opens Sharko, finds external-dns in the catalog. The entry tells him what the addon needs — including two required values and a secret it expects from the vault. He fills in the values but makes a mistake — points at the wrong vault path. Validation catches it before anything happens, in plain words.

**Climax:** Fixed, previewed, submitted. The PR shows exactly what will change; his team lead reviews and merges. ArgoCD deploys. Sam did the whole thing without touching a template or pinging Dana once.

**Resolution:** The team's knowledge stopped being one person's head. The review gate meant Sam couldn't break production — the worst case was a rejected PR.

### Journey 3 — Dana, three months later: the fleet upgrade (the signature moment)

**Where we meet her:** A CVE lands in cert-manager. Old world: check fourteen clusters by hand, read release notes, edit values files one by one, hope nothing's forgotten.

**Rising action:** The version matrix already shows it: cert-manager 1.12 on twelve clusters, 1.14.5 available, two clusters already current. "Last checked: 2 hours ago." She selects the twelve, reviews the upgrade preview — including the known webhook ignore-diff the catalog handles for her.

**Climax:** One action → one PR touching twelve small files. Reviewed, merged. The fleet converges. Total time: eleven minutes.

**Resolution:** When her manager asks "are we patched?", the matrix IS the answer. And if 1.14.5 had misbehaved — git revert, done.

### Journey 4 — Priya, IDP developer: the machine door

**Where we meet her:** Priya builds her company's internal developer portal. Product teams request "a cluster with monitoring and ingress" through it. Behind the portal today: a pipeline of scripts that clone the GitOps repo, run yq, template YAML strings, and open PRs — glue her team wrote, hates, and fears.

**Rising action:** She gets a Sharko API token (scoped, expiring). Registering a cluster is one POST; enabling addons is another; each returns the PR link her portal can track to merged-and-deployed.

**Climax:** She deletes the YAML-munging pipeline. Four hundred lines of glue replaced by API calls with validation, preview, and audit built in. Then her team goes further: they add their own in-house chart to the catalog through the API — and suddenly their internal service is a first-class addon in the portal, next to Datadog, with the same versions and upgrade flow.

**Resolution:** The portal's addon feature became deterministic: same request, same PR, same result. Every change traceable in git.

### Journey 5 — Omer, on-call: drift and a broken file

**Where we meet him:** Saturday. Someone (never found out who) hand-edited a cluster secret's labels directly in the cluster during a debugging session — and separately, a hotfix PR hand-edited a data file and broke the YAML.

**Rising action:** Sharko's drift detection flags prod-eu OutOfSync within 30 seconds, with a read-only diff: git says Datadog on, cluster says off. A warning event fires. The broken YAML never got that far — `sharko validate` failed the PR in CI: "clusters/prod-eu.yaml, line 14: bad indentation." For the drift, Omer reviews the diff, confirms git is right, and clicks re-sync; Monday, the team enables self-heal so next time it reverts on its own.

**Resolution:** Neither incident touched a running workload. The tamper was visible in seconds, the bad file never merged, and both fixes were one decision each — with git as the referee.

### Journey 6 — Dana again: migrating a live cluster with zero downtime

**Where we meet her:** The new cluster proved the loop. Now the real test: prod-eu — her busiest cluster, seven addons live, all managed by her old ApplicationSets, its connection secret created by Terraform two years ago.

**Rising action:** She opens Sharko's takeover flow for prod-eu. The preflight runs four checks and reports in plain language: the existing secret is standalone (nothing will recreate it); three of her old ApplicationSets are NOT deletion-safe — "set preserveResourcesOnDeletion first"; here are the seven Applications currently targeting this cluster (her cutover checklist); no name collision if she keeps the name "prod-eu". She fixes the three ApplicationSets (one small edit each — Step 0), and re-runs preflight: all green.

**Climax:** The swap: Sharko deletes nothing until she confirms. Old secret retired, Sharko's secret created seconds later — same name, same address, and every legacy label her old appsets select on, preserved automatically. Nothing prunes; the workloads never notice. Then addon by addon over two afternoons: parity-check values (copy-paste), enable in Sharko, merge, non-cascading delete of the old Application, watch Sharko's Application adopt the running workloads in place. Seven addons, zero restarts.

**Resolution:** When the last addon moves, she drops the legacy labels with one action — transition scaffolding down. prod-eu is fully Sharko-managed and nothing blinked. The remaining twelve clusters take her two weeks of calm, boring afternoons. Boring is the achievement.

### Journey Requirements Summary

These journeys reveal the capability areas the requirements must cover: install & bootstrap (J1) · trust & exit — inspectable engine, kill-Sharko doc (J1) · cluster registration + credentials (J1, J4) · catalog with per-addon knowledge: values, secrets, quirks (J2, J3) · validation + preview on every door (J2, J4) · version matrix + batch upgrade + freshness "last checked" (J3) · API with scoped expiring tokens + PR tracking (J4) · internal addons + catalog write-back (J4) · drift detection, self-heal, diagnostics, `sharko validate` with line numbers (J5) · RBAC & review gates (J2) · audit trail (J4, J5) · **takeover preflight — 4 checks: secret ownership ("who recreates this?"), ApplicationSet deletion-safety, cluster app inventory, name collision** (J6) · **takeover registration — same name/address, legacy labels preserved by default, dropped on completion** (J6) · **migration playbook docs — Step 0 appset safety, parity check, per-addon adoption cutover, honest downtime statement** (J6) · engine ships deletion-safe by default — preserveResourcesOnDeletion (J6).

### Migration decisions (locked with the panel, 2026-07-29)

- The secret swap is NOT "visibility only" by default: cluster generators delete generated Applications when the cluster secret disappears, and the finalizer cascades to workloads (verified vs ArgoCD docs). Step 0 (preserveResourcesOnDeletion on old appsets) is what makes the swap safe — the preflight enforces awareness of it.
- v4 FEATURES: takeover preflight (4 checks; reads only well-defined metadata/spec fields — tracking labels, ownerReferences, syncPolicy; never parses user templates) + takeover registration (name/address preservation, legacy-label preservation by default). RBAC widening for reading Applications/ApplicationSets = verify at planning. Preflight likely merges with the `sharko doctor` family at planning.
- v4 DOCS: the migration playbook (numbered recipe 0-5, parity-check chapter, Helm-CLI takeover chapter, honest downtime statement, "values are copy-paste" said early).
- Growth: automated import helper; automated takeover-diff vs live.
- Honest downtime promise: zero workload downtime — IF Step 0 done; seconds-long control-plane visibility pause at swap; minutes-long unmanaged window per addon during adoption; nothing restarts if parity held.
- POSITIONING (Moran, 2026-07-29): the migration path is not just onboarding mechanics — it is a selling point. Nearly every serious ArgoCD shop runs a home-made version of Sharko (own secret bootstrap, own label conventions, own appset patterns, own portal glue). The takeover flow says: "you already built a custom version of this; here is a safe, boring ramp from yours to the maintained one." Migration = the product meeting users where they already are.

## Domain-Specific Requirements

**Compliance:** None. The domain (DevOps / GitOps tooling) is unregulated. What carries the weight instead is **honesty**: every claim in the docs must be true in the code, and the security story must be inspectable (threat model published, engine chart readable, SBOM real or the claim dropped).

**Security constraints (Sharko holds dangerous things):**
- Sharko holds cluster credentials and syncs secrets from a vault. Secret values must never appear in logs, git files, Kubernetes events, or API responses.
- API tokens are scoped and expiring.
- Anything touching credentials gets the strictest review tier.

**Operational safety (the real risk in this domain is deleting workloads):**
- The engine ships deletion-safe by default (preserveResourcesOnDeletion on its ApplicationSets) — a disappearing cluster secret must never cascade into workload deletion.
- Single-owner rule: Sharko only touches resources it created and stamped `managed-by: sharko`. It never fights another controller over a resource.
- Self-heal is opt-in, off by default. Detection is always on; enforcement is the user's choice.
- Bad input never wins: a broken file never crashes Sharko, never half-applies, and always produces an error naming file, reason, and line.

**Ecosystem constraint:**
- Sharko integrates with ArgoCD only through its stable public contracts (cluster Secret format, ApplicationSets, Application status). Docs state supported ArgoCD versions. No private API usage, ever.

**Domain patterns followed / anti-patterns avoided:**
- Followed: git is the only truth; every change passes a PR gate; one file fanning out to many resources is normal GitOps (ApplicationSet precedent).
- Avoided: two front doors to the same state (the shelved CRD); half-alive features shipping (operator, scanner — parked); adopting resources Sharko didn't create.

## Innovation & Novel Patterns

### Detected innovation areas

1. **The addon as a first-class thing.** ArgoCD knows Applications, Helm knows charts, git knows files — and Renovate-style bots can bump a version in a file, but none of them knows what an addon *is*, where it runs, or what upgrading it means across a fleet. Sharko is the only layer where "addon" exists as an entity: catalog knowledge + fleet assignment + version matrix in one place. That's what makes fleet-wide upgrade advice possible at all. *The felt moment: the manager asks "are we patched?" — and the matrix is the answer.*

2. **Operator-style loops over the management layer, no CRDs.** ArgoCD already reconciles workloads from git. What nobody reconciles is the layer *above* it — cluster registration, addon assignment, addon secrets from a vault. That layer is normally hand scripts, Terraform, and one expert's memory. Sharko runs operator mechanics over it — reconcile loops, drift detection, opt-in self-heal — with readable git files as the desired state and a pull request as the admission gate. Even the templating engine itself becomes a versioned dependency, upgraded by a reviewable pin-bump PR. Not a new invention — a new combination, applied to a layer that never had it. *The felt moment: you can read your whole fleet's configuration like a text file, and there's nothing new to learn to read it.*

3. **Takeover with a safety preflight.** Migration paths are normal product hygiene — the novel part is the preflight that answers *"will anything get deleted?"* before you touch a live fleet: it scans your existing ApplicationSets for the deletion-cascade trap, checks who owns your cluster secrets, and inventories what's running. Sharko treats "you already built a home-made version of this" as the expected starting point, not an edge case. *The felt moment: preflight says "three of your ApplicationSets are not deletion-safe — fix these first," and Dana fixes them before anything could go wrong instead of finding out at 2am.*

### Market context (honest)

The niche is contested, not empty — verified in this project's blind reviews. gitops-bridge covers a similar pattern but is a pattern, not a product. Sveltos is the opposite architecture (own engine, CRDs, no PRs). Renovate/Dependabot bump versions in files but have no concept of an addon or a fleet. Raw ApplicationSets serve deeply custom shops better, and Sharko's docs say so. Sharko's edge is productization and the addon-as-entity layer — not an unoccupied gap.

### Validation approach — each claim gets its own proof

- **Addon-as-entity** → the recorded fleet-upgrade demo (≤3 min): outdated addons visible → one PR → fleet converges. The signature moment is the innovation demo.
- **Management-layer loops** → the kind playground full loop on a laptop, plus the kill-Sharko verification: the loops are real, and removable without breakage.
- **Takeover preflight** → a real brownfield takeover run (live cluster, live addons, old ApplicationSets) before release.
- Underneath all three: the claims-vs-code audit — 100% of documented behavior true in code.

### Risk & fallback

- If the engine abstraction doesn't fit a shop: the documented escape ladder ends with "use the raw ApplicationSet pattern" — an honest exit, not a trap.
- If ArgoCD changes underneath: Sharko touches only stable public contracts, and supported versions are stated. The coupling is real but priced.

## Self-Hosted API Product — Specific Requirements

### Project-type overview

Sharko is one installed product with three doors on one API: humans use the web UI, scripts and portals call the REST API, and the CLI wraps that same API. It runs on the user's hub cluster next to ArgoCD, installed by Helm. There is no SaaS, no hosted version — the user owns everything.

### The API contract

- **One API, three clients.** Anything the UI can do, the API can do — the UI is never a special door. This is a standing requirement, not a feature.
- **Surface areas** (promises, not an endpoint inventory): clusters (register, takeover with preflight), catalog (browse, add/edit entries including internal addons), assignments (enable/disable addons per cluster), versions (the matrix, upgrade actions, freshness), PR tracking (every write returns a PR the caller can follow to merged-and-deployed), validation & preview (dry-run before any PR), diagnostics (drift, health, errors).
- **Every write goes through the same pipeline:** validate → preview → PR. No API endpoint bypasses the review gate.
- **Data formats:** the API speaks JSON; the files it writes to git are readable YAML under Sharko's versioned format (`sharko.dev/v1` envelope). Format changes ship with an automated migration PR — never a hand-migration.
- **Documentation:** the API is documented via generated OpenAPI (swagger) docs — kept in sync by CI, a stale spec fails the build.

### Authentication & access

- Admin login for the UI; **scoped, expiring API tokens** for machines (expiry is part of the v4 honesty pass).
- Fine-grained per-user RBAC inside Sharko is **not** in v4 — team-level control lives where it already works: the PR review gate in git. The docs say this plainly.
- Secret values never travel through the API in responses.

### Versioning — three things version separately, each with a rule

1. **The product** (server + UI + CLI): semver, breaking changes only in majors.
2. **The engine chart**: its own version, pinned in the user's repo, upgraded only by a Sharko-opened pin-bump PR.
3. **The file format** (`sharko.dev/v1`): version bumps come with an automated migration PR.

### Deliberately skipped (with reasons)

- **Rate limiting:** not needed — self-hosted, single team, no public endpoint. Documented as a non-goal.
- **Client SDKs:** none in v4. The OpenAPI spec is the machine contract; users generate clients if they want them. The CLI is the reference client.
- **Multi-tenancy:** documented as an honest limit (one connection config), not built in v4.

### Self-hosted lifecycle (product features, not afterthoughts)

- **Install:** Helm chart, quickstart ≤30 minutes to first addon deployed.
- **Upgrade:** product upgrades via Helm; engine upgrades via pin-bump PR; both documented with rollback (`helm rollback` / `git revert`).
- **Migrate in:** the v3→v4 migration is one Sharko-opened PR; brownfield takeover per the migration playbook.
- **Uninstall:** the kill-Sharko guarantee, verified each release.

## Project Scoping & Phased Development

### MVP strategy

v4 is a **redefinition MVP**: most capabilities exist and are shipped; the release makes them coherent around one breaking change (the engine chart) and one standard (every claim true, nothing half-alive). The fastest path to "this is useful" is not new features — it's the day-zero experience: install → first addon in under 30 minutes, and a safe ramp in for people who already built their own version.

**Resources:** solo maintainer orchestrating an AI agent team; ceremony scales down, quality gates don't.

### Must-have (Phase 1 = v4.0.0) — each tied to the journey that proves it

- Engine as versioned OCI chart + one-PR migration from v3 + deletion-safe defaults *(J1, J6)*
- Data-only user repo: seed bootstrap, per-cluster files, engine pin *(J1)*
- Deployment-settings-as-data: catalog quirk defaults → user overrides → generic engine *(J3)*
- Catalog: approved-list model *(amended 2026-08-08)*, extended entries (required values, needed secrets), internal addons first-class, write-back verified git-native, community contribution path *(J2, J4)*
- Validation + preview on every door; `sharko validate` with line numbers *(J2, J5)*
- Version matrix + upgrade intelligence + freshness "last checked" *(J3 — the signature moment)*
- Takeover: preflight (4 checks) + registration (name + legacy-label preservation) + migration playbook docs *(J6)*
- Scoped, expiring API tokens; PR tracking on every write *(J4)*
- Drift detection + opt-in self-heal (shipped; re-verified under the new engine) *(J5)*
- **AI: both features stay** — upgrade summary + chat assistant, opt-in behind the user's API key, absent-but-not-broken without one, off the headline, honest AI-transparency note in docs *(locked this session)*
- The honesty pass: SBOM real or dropped, threat model surfaced, arm64 fixed, docs lead with K8s-Secrets, multi-tenancy stated as a limit
- Docs rewritten to the new identity; kill-Sharko re-verified

### Phase 2 (Growth)

Automated brownfield import helper · automated takeover-diff vs live · ArgoCD UI extension · argocd-notifications reuse · AppProject awareness · multi-tenancy / multi-ArgoCD · operator revisited only with full commitment · scanner revisited · community catalog growth.

### Phase 3 (Vision)

The addon layer ArgoCD fleets simply have — possibly under argoproj-labs one day. Quality earns it or it doesn't happen; both outcomes are acceptable.

### Risk mitigation

- **Technical — the engine migration is the riskiest piece** (a breaking change to every v3 repo). Mitigations: migration is exactly one Sharko-opened PR, reviewable and revertable; the full loop is provable on the kind playground before release; v3 users upgrade when ready — nothing forces the jump.
- **Market — the niche is contested.** Mitigation: honesty is the strategy — no overclaims to get mocked for, and the takeover flow meets users inside their existing home-made solutions instead of asking them to start over. No adoption metrics means no pressure to inflate.
- **Resource — one maintainer.** Mitigations: scope discipline is enforced by the "nothing half-alive" rule (operator and scanner parked, not dragging); forever-contracts are priced before acceptance (that's why there's no CRD); quality gates are automated so they don't depend on attention.

## Functional Requirements

*The capability contract: anything not listed here will not exist in v4. Panel-reviewed 2026-07-29 (journey coverage sweep, altitude check, over-promise check against the code). Two journey beats deliberately live in DOCS, not FRs: the non-cascading delete of an old Application during addon cutover (playbook step; automation = Growth) and the pre-takeover parity check (playbook chapter; automated takeover-diff = Growth).*

### Install, Lifecycle & Exit

- FR1: A platform engineer can install Sharko with Helm on the hub cluster and complete a guided setup.
- FR2: Sharko can bootstrap the user's repo with one PR containing only empty data folders, an engine version pin, and a README — nothing else.
- FR3: The user can upgrade the engine by merging a Sharko-opened pin-bump PR; the engine never updates silently.
- FR4: A v3 user can migrate to the v4 format by merging exactly one Sharko-opened PR.
- FR5: The user can remove Sharko completely; nothing running breaks, no data is stranded, and the docs prove it.
- FR6: Anyone can pull, read, and locally render the engine chart before trusting it — it's public and signed.
- FR7: A user can explore the UI in demo mode without connecting anything.

### Connections & Access

- FR8: A user can set up, edit, and test Sharko's connections — git provider, ArgoCD, vault — and see their health.
- FR9: Humans authenticate with a login; sessions expire.
- FR10: Every actor — human or token — carries a role from Sharko's coarse role model, and every write action is gated by it. Fine-grained (per-cluster / per-addon) permissions are explicitly not in v4; the docs state this limit.
- FR11: Machine clients authenticate with scoped, expiring API tokens that can be created, renewed, and revoked.

### Cluster Management & Takeover

- FR12: An engineer or machine client can register a cluster with credentials; Sharko creates the ArgoCD cluster secret, stamped as Sharko-managed.
- FR13: The user can run a takeover preflight on a cluster ArgoCD already manages: a plain-language report of four checks — who owns the existing secret, which ApplicationSets are not deletion-safe, what Applications target the cluster, and name collisions.
- FR14: Takeover registration can keep the cluster's existing name and server address, and preserves the old secret's legacy labels by default.
- FR15: After migration, the user can drop the preserved legacy labels with one action.
- FR16: The user can unregister a cluster; Sharko warns about the consequences first (including legacy labels still preserved) and requires explicit confirmation.

### Catalog

- FR17: A user can browse the catalog — the org's approved addons — and, separately, the read-only curated discovery window; entries show the addon's knowledge: description, chart location, required values with plain descriptions, needed secrets, known quirks, docs link. *(amended 2026-08-08)*
- FR18: A user can add their in-house chart as a first-class catalog entry through any door — same versions, upgrades, and flows as public addons.
- FR19: The org's catalog file holds the full, self-contained list of approved addons — chart, chart repo, version, namespace, settings, needed secrets. The curated list never adds an addon to it and never supplies a deployment field; it only decorates approved entries with display knowledge. The repo alone tells the whole deployment story. *(amended 2026-08-08 — replaced the delta-model wording)*
- FR20: Version freshness is maintained on a schedule (default daily) and on demand; the UI always shows "last checked."
- FR21: An outside contributor can propose a curated catalog entry through a template-guided PR.

### Addon Assignment & Deployment

- FR22: A user can enable or disable an addon on a cluster; the change is a PR touching small readable data files.
- FR23: A user can set Helm values per addon — globally and per cluster — as free-form values files.
- FR24: A user can override deployment settings (namespace, ignore-differences, sync options, prune behavior) as declared data per addon and per cluster; catalog entries supply known-quirk defaults.
- FR25: The engine turns data files into ArgoCD ApplicationSets with zero user-visible template logic, and ships deletion-safe by default.

### Versions & Upgrades

- FR26: A user can see the version matrix: which addon version runs on which cluster, and what newer versions exist.
- FR27: A user can upgrade an addon on many clusters in one action, producing one reviewable PR.
- FR28: A user can run an upgrade check before committing: version comparison, impact, and security advisories — deterministic, no AI required.
- FR29: With an AI key configured, a user can additionally get a plain-language upgrade summary.

### Doors: API, UI, CLI

- FR30: Anything the UI can do, the API can do; the CLI wraps the same API.
- FR31: Every write, through any door, follows the same pipeline: validate → preview → PR. Validation catches missing or invalid required values and unresolvable references where Sharko can check them; the preview shows the exact files and content the PR will contain. No door bypasses the gate.
- FR32: Any client — human or machine — can follow a write from PR-opened through merged to deployed.
- FR33: A user can run `sharko validate` in their own CI; failures name file, reason, and line.

### Watching: Drift, Diagnostics, Events

- FR34: Sharko detects drift between git and live cluster state within its stated window and shows a read-only diff.
- FR35: From the drift view, a user can trigger a one-time re-sync — make the cluster match git now — without enabling self-heal.
- FR36: A user can opt in to self-heal; when on, Sharko re-applies only its own addon-label keys — never anything else.
- FR37: When something breaks, diagnostics say what, where, and why in plain words — including parser errors with file and line.
- FR38: Sharko emits Kubernetes events for failures and important transitions, never including secret values.
- FR39: A user can see the whole fleet's state on one dashboard.
- FR40: Every change is traceable: who asked, through which door, which PR, what merged.

### Bad Input & Robustness

- FR41: Malformed or broken input never crashes Sharko and never half-applies — every change lands all-or-nothing.

### Addon Secrets

- FR42: Sharko syncs addon secrets from the user's vault to the remote clusters that need them, and repairs/rotates them automatically on its stated schedule.
- FR43: Secret values never appear in logs, git files, API responses, previews, or events.

### AI Assistant (opt-in)

- FR44: With an AI key, a user can work through a chat assistant that uses the same permissions and the same PR gate as the three doors; without a key, the feature is absent, not broken. *(amended 2026-08-08 — the assistant is a client of the doors' shared pipeline, not a door)*

## Non-Functional Requirements

*Only categories that matter for Sharko. Accessibility and regulatory compliance are skipped on purpose (unregulated product, small technical audience). Every number comes from locked decisions or gates already enforced in CI.*

### Performance (honest, stated timings — no vague "fast")

- NFR1: Label/assignment drift is detected within 30 seconds; merges trigger reconcile immediately rather than waiting for the next cycle.
- NFR2: Addon secrets are checked and repaired on a 5-minute default cycle (configurable); a manual trigger is available.
- NFR3: Catalog version freshness: scheduled refresh (default daily) + on-demand; the UI never hides how stale its data is ("last checked").
- NFR4: The quickstart holds its promise: install → first addon deployed in under 30 minutes on a fresh cluster.
- NFR5: Release-to-release performance is gated: no API path may regress its p99 latency by more than 20% (enforced in CI).

### Security

- NFR6: Stored credentials (git tokens, ArgoCD tokens, kubeconfigs, AI keys) are encrypted at rest; secret values never appear in logs, git files, previews, events, or API responses (the FR43 rule, enforced by tests).
- NFR7: Release artifacts — server images and the engine chart — are signed; an SBOM is published with each release (or the claim is dropped — no paper promises).
- NFR8: The threat model is a public doc, linked from the README, covering what Sharko holds and what an attacker could do with it.
- NFR9: API tokens expire by default and are renewable before expiry; expiry defaults are documented.
- NFR10: The AI assistant's access is bounded by the caller's role — it can never do more than the human or token driving it, and its transparency note states what data leaves the cluster when a key is configured.

### Reliability

- NFR11: Sharko going down never touches the fleet: ArgoCD keeps deploying, workloads keep running — only Sharko's automation pauses. (The kill-Sharko property, held continuously, not just at uninstall.)
- NFR12: All reconcilers recover cleanly from a restart — no manual intervention, no partial state (backed by FR41's all-or-nothing rule).
- NFR13: High availability is NOT promised in v4: single replica, documented honestly as a limit. Acceptable because of NFR11 — Sharko's downtime is an inconvenience, not an outage.

### Scalability

- NFR14: The product is designed and tested for fleets of roughly 10–50+ clusters; the dashboard and version matrix stay usable at 50 clusters × dozens of addons.
- NFR15: Git-at-scale behavior (many small files, PR volume) is documented with honest guidance rather than hidden.

### Integration & Compatibility

- NFR16: Supported ArgoCD versions are stated per release; integration uses only ArgoCD's stable public contracts.
- NFR17: Supported git providers: GitHub, Azure DevOps, Gitea (self-hosted). Supported secret backends: AWS Secrets Manager, Kubernetes Secrets.
- NFR18: Images ship for amd64 and arm64 (Apple-silicon laptops for the playground, Graviton nodes in production).
- NFR19: Supported Kubernetes version range is stated per release.

### Quality Gates (the maintainer's contract with himself)

- NFR20: Every release passes: build, CI-honest tests, helm lint, strict docs build, the perf gate, and the claims-vs-code audit. A claim that fails the audit is fixed or removed — never shipped as words.

## Appendix — Delta from v3

What v4 actually changes, on one page.

**Breaking (the anchor):**
- The engine. v3 copied template files into the user's repo at bootstrap; v4 ships the engine as a versioned, signed, public OCI chart. The user's repo becomes data files plus one version pin. Migration: one Sharko-opened PR (FR4).

**New in v4:**
- Takeover: the preflight (4 checks) and takeover registration (same name/address, legacy labels preserved by default) — the brownfield migration features (FR13–FR16).
- Catalog approved-list model *(amended 2026-08-08 — replaced the delta model)*: the org's file is the full approved list; the curated list is a display-only discovery window. Plus extended entry fields + internal addons as first-class citizens (FR17–FR19).
- Deployment-settings-as-data with catalog quirk defaults (FR24).
- API token lifecycle: expiry by default, renew, revoke (FR11). One-time re-sync from the drift view (FR35). Unregister with warn-and-confirm (FR16).
- The honesty pass: SBOM real or claim dropped, threat model surfaced, arm64 images, docs lead with Kubernetes Secrets, stated limits (multi-tenancy, single replica, coarse roles).
- The migration playbook with the honest downtime statement (docs).

**Removed or parked from the visible surface:**
- The ClusterAddons operator (CRD + controller): shelved — out of the chart and the build, code preserved on a shelf branch.
- The catalog auto-discovery scanner bot: parked — replaced by the community contribution path (FR21).
- Naming *(amended 2026-08-08)*: "Catalog" = the org's approved list. "Marketplace" survives only as the name of the read-only curated discovery window.

**Unchanged and re-affirmed:**
- The three reconcile loops (cluster secrets, assignment labels, addon secrets), drift detection with opt-in self-heal, the validate → preview → PR pipeline on every door, the version matrix with upgrade intelligence, git as the only source of truth, and the kill-Sharko guarantee.
