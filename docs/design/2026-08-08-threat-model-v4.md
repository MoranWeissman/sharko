---
title: "Sharko v4 Threat Model"
author: "Moran Weissman <moran.weissman@gmail.com>"
date: "2026-08-08"
version: "v4.0.0 (pre-release, task #152 Secret Sync security closure)"
status: "Replaces the v2.0.0 baseline threat model. Written against merged main after task #152 lanes A (refresh reads Git), B (backend boundary), C (destination TLS refusal) and F (least-privilege RBAC). All of these are merged and describe what is actually deployed."
scope: "Sharko server, CLI, web UI, Helm chart, container image, catalog signing pipeline. Excludes downstream addon contents and ArgoCD itself."
related:
  - ".claude/team/security-auditor.md"
  - "SECURITY.md"
  - "docs/site/operator/security.md"
  - "docs/site/operator/configuration.md"
  - "docs/site/operator/supply-chain.md"
  - "docs/site/community/roadmap.md"
---

# Sharko v4 Threat Model

> This document tells you, plainly, what happens to a secret value once
> Sharko has it, what stops that value from going somewhere it
> shouldn't, and what is still your job to lock down. It is not a
> penetration-test report and it is not a promise that nothing can go
> wrong — it is an honest account of what the code actually does today,
> written for someone who is about to hand Sharko real production
> credentials and wants to know what they're trusting it with.
>
> This document replaces the earlier v2.0.0 threat model. It used to
> keep that document's old filename and URL (`2026-06-02-threat-model-v2.md`)
> so existing links kept working — but the content below is a full
> rewrite for v4, not an update of the old STRIDE/OWASP-style document,
> so keeping a filename that said "v2" and carried a June date was its
> own small inaccuracy. The file has been renamed to
> `2026-08-08-threat-model-v4.md` to say what it actually is; a short
> pointer file was left at the old path so any link written against the
> old filename still resolves.

## Contents

1. [The short version](#1-the-short-version)
2. [What Sharko actually does with a secret](#2-what-sharko-actually-does-with-a-secret)
3. [The two delivery triggers, and why they're one path now](#3-the-two-delivery-triggers-and-why-theyre-one-path-now)
4. [The backend boundary](#4-the-backend-boundary)
5. [The destination check — no secret over an unverified connection](#5-the-destination-check--no-secret-over-an-unverified-connection)
6. [The ownership gate — Sharko won't take over your Secret](#6-the-ownership-gate--sharko-wont-take-over-your-secret)
7. [Where a raw value exists, and for how long](#7-where-a-raw-value-exists-and-for-how-long)
8. [What permissions Sharko holds, and how to cut them down](#8-what-permissions-sharko-holds-and-how-to-cut-them-down)
9. [The residual risk, stated plainly](#9-the-residual-risk-stated-plainly)
10. [What is not built yet](#10-what-is-not-built-yet)
11. [The ArgoCD resource-exclusions question](#11-the-argocd-resource-exclusions-question)
12. [Reporting a problem](#12-reporting-a-problem)
13. [Where this document came from](#13-where-this-document-came-from)

---

## 1. The short version

Sharko holds credentials that can reach every cluster it manages — a
Git token, an ArgoCD token, and whatever AWS or Kubernetes identity it
uses to read secret values. If someone fully compromises the Sharko
pod, they get everything that identity can reach. That is true of any
tool built this way, and this document does not pretend otherwise —
see [§9](#9-the-residual-risk-stated-plainly).

What this document is really about is the smaller, sharper question:
*given that Sharko holds those credentials, what stops it from moving
a secret value somewhere it shouldn't go, even by accident or by a
narrower compromise (a stolen admin login, a bug in one handler)?*
Four checks answer that question, and every one of them lives in the
code, not just in a policy document:

| Check | What it stops | Where it lives |
|---|---|---|
| One delivery path | Two different code paths quietly drifting apart, one of them skipping a check the other has | `internal/secrets/sync_cluster.go`, `internal/secrets/reconciler.go` |
| Backend boundary | Reading a secret value outside the area an operator configured | `internal/providers/boundary.go`, `aws_sm.go`, `k8s_secrets.go` |
| Destination check | Sending a secret value over a connection that skips certificate checks | `internal/remoteclient/tlsguard.go` |
| Ownership gate | Overwriting a Kubernetes Secret some other tool owns | `internal/remoteclient/secrets.go` (`EnsureSecret`) |

The rest of this document walks through each of these, with the exact
file and function that enforces it, and then says clearly what is
**not** covered by any of this yet.

---

## 2. What Sharko actually does with a secret

Sharko does not store secret values. It is a courier: it reads a value
from a backend you configured (AWS Secrets Manager, or a Kubernetes
Secret store), and writes that value into a real Kubernetes Secret on
a cluster you manage, so an addon running there can use it. The value
passes through Sharko's memory on its way — it is never written to
Git, never rendered into a manifest, never sent to ArgoCD's
repo-server, and never touches Redis. That last part is a genuine,
deliberate design choice, and it's a real difference from how ArgoCD
itself handles secret-shaped values through plugins like the ArgoCD
Vault Plugin, which do render into manifests that pass through
repo-server. Sharko never does that.

What Sharko decides — the part that carries risk — is *which* value
goes *where*. That decision has to come from something trustworthy,
it has to stay inside a boundary an operator configured, it has to go
to a destination that's actually who it claims to be, and it has to
land on a Secret Sharko is allowed to touch. Sections 3 through 6 cover
each of those in turn.

---

## 3. The two delivery triggers, and why they're one path now

There are two moments where Sharko decides to push a secret value to a
cluster:

- **The scheduled engine.** A timer fires roughly every five minutes
  and pushes whatever the Git catalog says should be pushed.
- **"Refresh now."** An operator clicks Refresh on a cluster, or calls
  `POST /api/v1/clusters/{name}/secrets/refresh`, and wants it to
  happen immediately instead of waiting for the timer.

Until this sprint, those were two different code paths reading from
two different places. The scheduled engine read from Git. The refresh
path read from an in-memory map that an API call
(`POST /api/v1/addon-secrets`) could fill directly — no Git, no pull
request, no review. In production nothing else ever filled that map,
so a stolen admin token could point a real production secret at the
wrong destination and then hit refresh, and it would go through with
no trace in Git. That was a real hole, and it's why this sprint
exists.

It's closed now, and closed the simple way: **both triggers run the
same code.** `POST /api/v1/addon-secrets` and
`DELETE /api/v1/addon-secrets/{addon}` are gone — there is no HTTP door
into that in-memory map any more. The map itself (`internal/api/router.go`'s
`addonSecretDefs`) and the orchestrator's push path that reads it are
still there in the code, but nothing in a production boot ever fills
that map: the only function that writes to it is `SetDemoAddonSecretDefs`,
called by demo mode and by tests, and no production code path calls it.
A real boot's addon-secret definitions live exclusively in the Git
catalog. `POST /clusters/{name}/secrets/refresh` no longer reads a
request body at all; the only inputs it takes are the cluster name in
the URL and an optional addon name in the query string
(`internal/api/cluster_secrets.go`, `handleRefreshClusterSecrets`).
What it delivers comes from `Reconciler.SyncCluster`
(`internal/secrets/sync_cluster.go`), which filters the *exact same
plan* the scheduled engine computes
(`Reconciler.planPushes`, `internal/secrets/reconciler.go`) down to one
cluster, and pushes each item through the *exact same function*
(`Reconciler.reconcileSecret`) the timer calls. There is one function
that decides "what should this cluster have," and both triggers call
it. A cluster the Git-tracked `managed-clusters.yaml` doesn't list is
refused (`ErrClusterNotInGit`); an addon Git doesn't define a secret
for is refused (`ErrAddonNotInGit`). Neither refusal echoes any secret
material — they're fixed sentences naming the cluster or addon the
caller already asked about.

Because both triggers now share `reconcileSecret`, every check in
sections 4 through 6 below applies identically no matter which trigger
fired. There is no second, thinner implementation anywhere that skips
one of them.

---

## 4. The backend boundary

A secret-value read has to stay inside the area an operator configured
for that connection. This check lives in the provider itself
(`internal/providers/boundary.go`), before any call reaches AWS or the
Kubernetes API — so it can't be missed by a caller that forgets to ask,
and a refused read never even reaches the backend.

**AWS Secrets Manager.** The provider is configured with a name
prefix. A read is only allowed if the requested secret name starts
with that prefix (`checkAddonSecretPathAllowed`,
`internal/providers/aws_sm.go`). If no prefix is configured, Sharko
refuses every read rather than falling back to "the whole account" —
an empty prefix is treated as a configuration mistake, not permission
to read anything. The refusal is a fixed sentence naming the path and
the configured prefix; it never carries a raw AWS SDK error.

**Kubernetes Secrets backend.** The provider is configured with a
namespace. A path can name that namespace explicitly
(`namespace/secret-name/key`) or leave it implicit
(`secret-name/key`), but it can never point at a *different*
namespace — that form is refused before the Kubernetes API is ever
called (`GetSecretValue`, `internal/providers/k8s_secrets.go`).

Both refusals are worded the same everywhere — the UI, the CLI and the
API all show the provider's own sentence back to the caller — so an
operator sees the same explanation no matter which door they used.

This only covers *addon-secret value reads* — the calls that fetch a
value Sharko is about to deliver to a managed cluster. It does not
apply to the separate lookup Sharko uses to fetch a *cluster's own*
kubeconfig from a backend, which is a different, narrower surface (an
exact-name lookup, not a value Sharko then redistributes).

---

## 5. The destination check — no secret over an unverified connection

`EnsureSecret` — the one function every secret-carrying write on a
remote cluster goes through — refuses to send a value at all if the
destination's connection is set up to skip certificate checks. This
matters because a secret write is not a read: it puts a live value on
the wire, and if the connection doesn't verify who it's actually
talking to, anyone who can sit in the middle can terminate that
connection themselves and read the value in the clear.

A destination counts as unverified if its kubeconfig says
`insecure-skip-tls-verify: true`, or if its ArgoCD cluster config says
`insecure: true` (which Sharko folds into the same kubeconfig shape
when it builds the connection, so both cases end up caught the same
way). The check happens twice, for defense in depth: once early, in
the reconciler, right after fetching credentials and before ever
touching the secrets backend (`reconcileSecret`,
`internal/secrets/reconciler.go`) — so a refusal shows up as a clean,
deliberate "refused" row rather than a failed write; and again, as the
real hard stop, inside `EnsureSecret` itself
(`internal/remoteclient/secrets.go`), which is the single choke point
every write to a remote cluster's Secrets goes through, from any
caller. Nobody can reach a write path that skips this question.

This refusal only applies to **writes that carry a real secret
value.** Reads (listing what's on a cluster, diagnostics, discovery)
and deletes (cleaning up during unadopt or cluster removal) still work
against an insecurely-registered cluster, because neither of those
sends a secret value out — blocking them would break cleanup on
exactly the clusters that most need it. There's a build-tag-gated
bypass for tests and demos (`allowUnverifiedDestinations`); a normal
release build does not compile with that tag, so the bypass cannot be
switched on in a production binary.

What this check does **not** cover: if the destination's connection
*does* pass certificate verification but the certificate authority it
trusts has itself been compromised, Sharko has no way to know that —
it trusts the same certificate chain the kubeconfig points at, same as
any TLS client would.

---

## 6. The ownership gate — Sharko won't take over your Secret

Before Sharko writes or updates a Secret on a remote cluster, it
checks whether that Secret already exists and, if it does, whether
Sharko itself created it — a Secret is "Sharko's" only if it carries
the label `app.kubernetes.io/managed-by: sharko`. If the Secret exists
and doesn't carry that label, Sharko leaves it alone: no overwrite, no
re-labeling, no error surfaced as a failure — it's recorded as a
deliberate refusal (`ErrForeignSecret`). This is the same rule task
#150 already applied on the harder problem — a rival tool owning the
*cluster-connection* Secret — extended here to every Secret Sharko
ever writes.

The check and the write happen in the same function
(`EnsureSecret`, `internal/remoteclient/secrets.go`) with the ordinary
Kubernetes get-then-write pattern: get, and if it exists and isn't
Sharko's, stop. A Secret that appears *between* the check and the
write (a race with something else creating it that exact moment) is
still caught — the create call itself fails with "already exists," and
the caller treats that the same as finding it labeled foreign.

The same rule holds when Sharko is turned off. Disabling an addon
leaves its Secrets in place — Sharko doesn't chase after them and
delete them. The only Secrets Sharko ever removes are ones its own
ownership label says it created, and only through the specific
cleanup paths that check that label first
(`DeleteSecretIfManaged`, same file). If you remove Sharko entirely,
the Secrets it wrote stay exactly where they are.

---

## 7. Where a raw value exists, and for how long

A raw secret value exists in Sharko's memory only while it's actively
being delivered:

1. Inside the backend provider call, as bytes returned from
   `GetSecretValue` (`aws_sm.go`, `k8s_secrets.go`).
2. Held briefly by the reconciler while it builds the destination
   Secret's data (`reconcileSecret`, `internal/secrets/reconciler.go`).
3. Sent once, as the `Data` field of the Kubernetes Secret object,
   through `EnsureSecret`.

It is never written to Git, never rendered into a manifest, never sent
to ArgoCD's repo-server, and never touches Redis. It does not appear
in any API response — the API only ever surfaces backend *paths*
(Git-approved metadata) and Secret *names*, never contents. It does
not appear in the audit log, which records that a secret was pushed
and to where, not what was in it.

One gap that used to be open is closed now: five log lines that
recorded a fetched value's **length** — not the value itself, just how
many bytes it was — have been removed (`aws_sm.go`, `k8s_secrets.go`,
task #152 lane D). Two of them were worse than first thought: they
logged the length of a whole cluster kubeconfig, at Info level, so they
were on by default in production, not just at debug level. All five
are gone now. Sharko logs still carry secret *shapes* in a couple of
harmless ways (for example, how many keys are in a fetched Kubernetes
Secret) but no line records how big a raw value or a kubeconfig is.

Also worth being honest about: nothing here zeroes memory after use.
The raw bytes sit in Go's normal garbage-collected heap like any other
value, and someone with a memory dump of the Sharko pod (`kubectl
debug` plus process attach, or similar) could in principle recover
them for as long as the garbage collector hasn't reclaimed that
memory. This is a general property of managed-runtime software, not
something specific to Sharko, and it's listed honestly in
[§10](#10-what-is-not-built-yet).

---

## 8. What permissions Sharko holds, and how to cut them down

As of this document, Sharko's Kubernetes ServiceAccount is granted, in
its own Helm chart (`charts/sharko/templates/rbac.yaml`):

- **A cluster-wide, read-only grant on ArgoCD's own resources**
  (Applications, AppProjects, ApplicationSets — `get`/`list`/`watch`
  only, plus Nodes if `config.nodeAccess` is on, which is the
  default). This `ClusterRole` carries **no Secrets rule at all**.
  There used to be a cluster-wide `secrets: get,list` rule here too —
  Kubernetes documents `list` on Secrets as exposing their contents,
  not just their names, so that was a genuinely broad grant. Task #152
  story F removed it. Every Secret read Sharko does anywhere on the
  host cluster is now one of the two namespaced grants below.
- **A namespaced read/write** on Secrets in the ArgoCD namespace only
  (`rbac.argocdNamespace`, default `argocd`), for managing
  cluster-connection Secrets. It carries no `resourceNames`, so it
  covers every Secret in that namespace, not only the ones Sharko
  created.
- **A namespaced, read-only grant** (`get`/`list`) per namespace
  listed in `rbac.k8sSecretsProviderNamespaces` (the release namespace
  is always included, whether or not an operator lists it), for the
  k8s-secrets cluster-credential and addon-secret providers when
  either is configured to use that backend. It is namespace-scoped,
  not name-scoped: within each of those namespaces it reads **every**
  Secret, not only the ones those two providers ask for, and it is
  created even when neither provider is configured. The blast radius
  is therefore "every Secret in the release namespace, plus every
  Secret in each namespace an operator adds" — narrower than the
  cluster-wide rule it replaced, and wider than the providers
  themselves need. Running Sharko in a namespace of its own, and
  keeping unrelated Secrets out of any listed namespace, is what
  closes the rest of the gap.
- **A namespaced, mostly resource-name-scoped** set of permissions in
  Sharko's own release namespace, for its own auth Secret, connection
  Secret, API-token Secret, and a few others — each pinned to a
  specific Secret name where the Kubernetes RBAC model allows it
  (`create` can't be scoped by resource name, so that verb stays
  broader by necessity).

The narrowing described above already shipped (task #152, story F, PR
#793) — this document describes the RBAC that is actually deployed
today, not a proposal.

**AWS side:** the addon-secret backend authenticates through whatever
IAM identity you give it (typically IRSA). Sharko's own boundary check
([§4](#4-the-backend-boundary)) stops it from *asking* for anything
outside the configured prefix, but that check only runs inside
Sharko's code — the IAM policy itself is the real, enforced backstop.
**Scope the IAM policy to the same prefix you configure in Sharko.** A
policy that grants `secretsmanager:GetSecretValue` on `*` means the
IAM identity — not Sharko's code — is the only thing standing between
a compromised Sharko pod and every secret in the account.

**Production example.** A minimal IRSA policy for the addon-secret
identity should look like:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": "secretsmanager:GetSecretValue",
      "Resource": "arn:aws:secretsmanager:REGION:ACCOUNT_ID:secret:PREFIX*"
    }
  ]
}
```

Replace `REGION`, `ACCOUNT_ID`, and `PREFIX` with your own values —
none of those are real values in this document. Keep `PREFIX` matching
exactly the prefix you configure on the Sharko connection, so the IAM
policy and Sharko's own boundary check agree.

**Today the addon-secret identity and the cluster-credential identity
are the same ServiceAccount / IAM identity** unless you deliberately
set up two separate connections. This is a separate, still-open gap —
it was not part of story F's RBAC narrowing above, and it isn't
something the Kubernetes RBAC change could fix on its own. Real
per-provider identity separation would need a code change so Sharko
can assume a different role per call, which doesn't exist today. If
you want that separation now, you have to build it yourself, by
registering two distinct provider connections with two distinct
underlying identities.

---

## 9. The residual risk, stated plainly

Say Sharko's pod is fully compromised — not "a stolen login," but an
attacker running code inside the pod itself. Every check in sections 4
through 6 is *Sharko's own code* checking itself. An attacker who
controls the process can, in principle, route around Sharko's own
checks and act directly as whatever identity the pod holds: the
IAM role, the Kubernetes ServiceAccount, the Git token, the ArgoCD
token. At that point, the blast radius is whatever those identities
are allowed to do — which is exactly why [§8](#8-what-permissions-sharko-holds-and-how-to-cut-them-down)
matters. Scoping those identities tightly is what limits the damage
from this scenario; nothing in Sharko's own code can prevent it,
because the attacker would be running as Sharko, not talking to it.

This is documented, not fixed, and it isn't going to become "fixed" in
a future release either — it's the same trade-off every service that
holds live credentials makes. The honest response to it is: run the
pod with the Restricted Kubernetes Pod Security Standard (non-root,
read-only root filesystem, no privilege escalation, capabilities
dropped — Sharko's chart already ships this), scope every identity
Sharko holds as tightly as your setup allows, and treat "Sharko pod
compromised" as an incident that needs the same response as "the
identity it holds got stolen directly" — because functionally, it is.

One more limit worth saying plainly here, not just in [§5](#5-the-destination-check--no-secret-over-an-unverified-connection):
the destination TLS guard only stops secret **value writes** over an
unverified connection. Reads (listing what's on a cluster, diagnostics,
discovery) and deletes (cleanup during unadopt or cluster removal)
still go through against a cluster that's registered with
skip-verify or plain HTTP. This is a deliberate, recorded decision,
not an oversight — none of those actions send a secret value out, and
blocking them would break the exact cleanup paths that an
insecurely-registered cluster most needs. If a cluster's connection
doesn't verify who it's actually talking to, treat everything Sharko
does against it — not only secret writes — as running over a
connection that was never proven to be the right endpoint.

---

## 10. What is not built yet

None of the following exist in Sharko today. Naming them isn't a
promise they're coming in a specific release — it's making sure nobody
reads this document and assumes a protection is in place when it
isn't.

- **SSO and OIDC.** Sharko authenticates with local users, API keys,
  and optional GitHub OAuth. No SAML, no OIDC identity-provider
  integration.
- **Per-cluster and per-addon roles.** RBAC today is three flat tiers
  — Admin, Operator, Viewer — applied across the whole fleet. An
  Operator-tier user or API key that's meant for one cluster can act
  on every cluster.
- **High availability.** Sharko runs as a single pod. Session state
  and the audit-log ring buffer are in-memory; a restart loses both.
- **Service-mesh mTLS.** Sharko doesn't require or configure mTLS
  between itself and anything it talks to — that's the operator's
  network layer to set up if wanted.
- **HSM support.** Sharko's own long-lived tokens are encrypted with a
  key from an environment variable, not backed by a hardware security
  module.
- **Tamper-evident audit log.** The audit log is append-only from
  Sharko's side but carries no hash chain or signature. If you need
  forensic-grade integrity, ship it to an immutable downstream store
  (object storage with a write-once policy, for example) — Sharko
  doesn't do that for you.
- **A policy engine.** There's no OPA/Rego or similar pluggable policy
  layer. The boundary checks in this document are hard-coded Go logic,
  not configurable policy.
- **More secret backends.** Today's backends are AWS Secrets Manager
  and Kubernetes Secrets. No Vault, no Azure Key Vault, no GCP Secret
  Manager yet.
- **Memory zeroization.** As covered in [§7](#7-where-a-raw-value-exists-and-for-how-long),
  raw values sit in ordinary garbage-collected memory. Nothing
  actively wipes them after use.
- **A paid third-party penetration test.** This document is the
  maintainer's own structured review of the code. It has not been
  reviewed by an external security firm.

This document also makes **no claim that Sharko is "as secure as
ArgoCD."** They're different pieces of software solving different
problems, with different attack surfaces and a different maturity
history. Sharko depends on ArgoCD's own correctness for the actual
cluster deploys it triggers; ArgoCD's own security posture is
documented upstream, not here.

---

## 11. The ArgoCD resource-exclusions question

Sharko already ships a feature at
`GET /api/v1/argocd/resource-exclusions` (documented in
[Configuration → ArgoCD Resource Exclusions](../site/operator/configuration.md#argocd-resource-exclusions))
that checks whether your `argocd-cm` ConfigMap tells ArgoCD to leave
Sharko-managed Secrets alone, and recommends adding that configuration
if it's missing. This document was written to settle a specific
question about that recommendation: is it a real technical need, or is
it the kind of advice this project has explicitly banned elsewhere —
telling operators to hide a fight between two controllers over the
same Secret with an ArgoCD-side setting instead of fixing the fight?

Sharko already carries that exact ban in several other places. The
takeover playbook and the connection-doctor docs both say plainly:
if an ArgoCD Application is genuinely rendering the *same* Secret
Sharko also writes, the fix is to stop the other renderer at the
source, not to add an `ignoreDifferences` rule that hides the clash
while leaving it running.

**This is a different situation, and the exclusion advice stays.** The
reason it's different: an addon-values Secret Sharko pushes is never
part of any ArgoCD Application's Git-tracked manifests in the first
place. ArgoCD's Application controller only tracks and prunes
resources it deployed itself, through a sync, from what's declared in
Git — a Secret Sharko creates directly on the cluster was never
declared anywhere ArgoCD looks, so there's no second renderer to fight
with. What the exclusion actually does is narrower: it keeps these
Secrets out of ArgoCD's own cluster-wide resource cache and its
orphaned-resources view, so an operator looking at ArgoCD's UI doesn't
see a pile of unexplained Secrets and get tempted to "clean up" what
looks like leftover cruft — which would actually be an addon's live
credentials. That's a visibility and noise concern, not an ownership
conflict, and it doesn't rely on the exclusion for correctness —
Sharko's own ownership gate ([§6](#6-the-ownership-gate--sharko-wont-take-over-your-secret))
is what actually stops any cross-tool overwrite either way, exclusion
configured or not.

The older planning note at `docs/design/REMEDIATION-PLAN.md` (line
140 at the time this was written) that first suggested this
configuration has been updated to record this decision and point
here, so nobody re-opens the same question without this context.

The per-addon `ignoreDifferences` entries in the addon catalog are a
completely separate, legitimate feature — they tell ArgoCD to stop
diffing specific fields on resources ArgoCD *does* actively manage
(fields an HPA mutates, for example), which is a normal and expected
use of that ArgoCD setting. Nothing in this section touches those.

---

## 12. Reporting a problem

If you find a security problem in Sharko, follow the process in
[SECURITY.md](../../SECURITY.md) — email
**moran.weissman@gmail.com** with subject line
`[Sharko Security] <summary>`, including the affected version and how
to reproduce it. Do not open a public GitHub issue for a security
problem. The maintainer is solo, so response times are best-effort,
not a formal SLA — SECURITY.md states the current targets plainly.

---

## 13. Where this document came from

This document replaces the earlier v2.0.0 threat model, which used a
heavier STRIDE/OWASP/SLSA framework structure aimed at a security
review committee. This rewrite is aimed at an operator deciding
whether to trust Sharko with production credentials, so it drops that
framework scaffolding in favor of plain description grounded directly
in the code.

It was written and then updated against `main` after task #152's lanes
A (`POST`/`DELETE /addon-secrets` retired, refresh reads Git — merged,
PR #794), B (backend boundary on the AWS and Kubernetes secret-value
readers — merged, PR #791), C (destination TLS refusal — merged, PR
#792), D (secret-length log lines removed — merged, PR #798) and F
(least-privilege RBAC narrowing — merged, PR #793).
[§8](#8-what-permissions-sharko-holds-and-how-to-cut-them-down)
describes the RBAC that is actually deployed today, after story F
landed.

Related reading:

- [`SECURITY.md`](../../SECURITY.md) — disclosure policy.
- [`.claude/team/security-auditor.md`](../../.claude/team/security-auditor.md) — the security-auditor checklist this project runs against.
- [`docs/site/operator/security.md`](../site/operator/security.md) — operator-facing security reference (headers, rate limiting, RBAC tiers, secret encryption).
- [`docs/site/operator/configuration.md`](../site/operator/configuration.md) — includes the ArgoCD resource-exclusions setup this document discusses in §11.
- [`docs/site/operator/self-managed-connections.md`](../site/operator/self-managed-connections.md), [`docs/site/operator/takeover-playbook.md`](../site/operator/takeover-playbook.md), [`docs/site/operator/cluster-reconciler.md`](../site/operator/cluster-reconciler.md), [`docs/site/operator/connection-doctor.md`](../site/operator/connection-doctor.md) — where the "don't paper over a fight with ignoreDifferences" rule is documented for the cluster-connection-Secret case.
- [`docs/site/community/roadmap.md`](../site/community/roadmap.md) — where the items in [§10](#10-what-is-not-built-yet) are tracked going forward.
- `.bmad/output/planning-artifacts/report-secret-sync-security.md` and `.bmad/output/planning-artifacts/epic-secret-sync-security-closure.md` — the report and the sprint plan that produced this rewrite.

*This is the working v4 threat model. It should be revisited whenever
a delivery path, a boundary check, or the permissions Sharko holds
changes — the same standard the v2.0.0 baseline set for itself.*
