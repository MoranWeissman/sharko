# Runbook Style Guide

> **Verified:** Authored 2026-06-01 as part of V2-4.1. Structure follows
> the Google SRE workbook "On-Call" + "Practical Alerting" playbook
> guidance — every alert should have a runbook entry detailing alert
> severity, impact, debugging suggestions, and mitigation actions
> ([SRE workbook — On-Call](https://sre.google/workbook/on-call)). The
> in-repo exemplar is
> [`docs/site/operator/budget-burn-runbook.md`](../operator/budget-burn-runbook.md)
> (V2-3.4 — already conforms to the structure below); use it as the
> reference shape, not as a template to copy verbatim.

This guide is for anyone adding or editing an operator runbook under
`docs/site/operator/`. It exists to keep runbooks **consistent enough that
an operator paged at 3am does not have to learn a new format per page**.
Every runbook follows the same shape: title, severity, symptoms,
diagnosis, mitigation, root-cause patterns, prevention, related runbooks.

If you are writing a developer-guide page (a contribution runbook, a
release procedure, a test playbook), the style is similar but the
audience expectation differs — see [Developer-guide vs. operator
runbooks](#developer-guide-vs-operator-runbooks).

---

## Why runbooks matter

Sharko is a CNCF-sandbox-aspiring project that ships into someone else's
production cluster. The operator who hits a Sharko failure is **rarely
the person who wrote the code**. They are an SRE who installed a Helm
chart, set up an Ingress, and now their pager is going off. They have a
finite budget of cognitive load and a finite budget of patience.

Three concrete goals drive every runbook:

1. **No 3am Slack to the maintainer.** If the runbook tells the operator
   what to look at, what to try, and what the common root causes are,
   they can mitigate without paging a human upstream. We are not a
   24×7-SaaS team; runbooks are how a small-maintainer project still
   ships to production.
2. **Mean time to repair drops.** Per the
   [SRE workbook chapter on on-call](https://sre.google/workbook/on-call),
   the single most effective lever on MTTR after detection latency is
   the *quality of the playbook entry linked from the alert*. A good
   runbook turns a 45-minute investigation into a 5-minute mitigation.
3. **Knowledge stays in the repo.** When the maintainer learns a new
   failure mode from a real incident, that knowledge belongs in a
   runbook, not in a Slack DM. The repo is the source of truth.

The runbook is **not** the place to explain how the system works in
general — that's the architecture / developer guide. The runbook is the
place to tell an on-call human, in the moment, what to look at and what
to try. It assumes they already know roughly what Sharko is and roughly
what the failing component does.

---

## Developer-guide vs. operator runbooks

Both audiences want the same shape (symptoms → diagnosis → mitigation
→ root-cause → prevention), but the voice differs.

| Aspect | Operator runbook (`docs/site/operator/`) | Developer-guide runbook (`docs/site/developer-guide/`) |
|---|---|---|
| Reader | Platform engineer running Sharko in production | Sharko contributor or someone running e2e tests locally |
| Time pressure | High (pager-driven) | Low (planned work) |
| Tone | Imperative, terse, command-first | Explanatory, command-first but with reasoning |
| Tool surface | `kubectl`, `curl`, Prometheus, Grafana, log aggregator | `go test`, `make`, local CLI, kind, gitfake |
| Length floor | 300 lines (shorter = under-documented) | 200 lines (lower floor — contribution playbooks can be terser) |
| Length ceiling | 800 lines (above = split) | 1000 lines (above = split) |
| Severity field | Required (P0/P1/P2) | Optional (most are not pager-shaped) |

This guide focuses on **operator** runbooks. The developer-guide runbooks
in this repo (`catalog-scan-runbook.md`, `personal-smoke-runbook.md`,
`e2e-testing.md`) follow the same skeleton but skip severity and adopt a
slightly more explanatory tone.

---

## Required sections

Every operator runbook has these eight sections, **in this exact order**.
The order matches the order an on-call human reads the page: confirm I
have the right page, see how bad this is, see what the symptom is, see
where to look, see what to try, understand the most common causes,
understand how to stop this recurring, follow the trail to related
problems.

### 1. Title

```markdown
# <Failure name>
```

The title is the failure mode, not the system. **Good:**
`# Reconciler Crash Loop`, `# AWS IAM Token Mint Failure`. **Bad:**
`# Cluster Reconciler` (that's a system overview, not a runbook),
`# Documentation for Cluster Reconciler` (cargo-cult-ish).

The first H1 is the title. Filename matches the failure name in
kebab-case: `reconciler-crash-loop.md`.

### 2. Severity

```markdown
**Severity:** P0 page | P1 ticket | P2 next sprint
```

Pick **one**, on the first non-title line. The vocabulary matches the
V2-3 alert severity vocabulary used by
`charts/sharko/templates/prometheusrules.yaml`:

- **P0 (page)** — wake someone up. Cluster registration is broken;
  secrets store is offline; reconciler is crash-looping; ArgoCD is
  unreachable; auth is bypassable; data is being silently lost. The
  fleet is in a state that gets worse the longer it sits.
- **P1 (ticket within 24h)** — file a ticket, fix the next business
  day. A single cluster is failing; a specific addon is broken; a rate
  limit was hit; a signature verification failed on one source. The
  rest of the fleet is fine; the working population can usually retry
  through it.
- **P2 (next sprint)** — track and fix when convenient. Transient
  diagnostic-only failures; an edge case affecting one operator's
  workflow; cosmetic UI issues; "noisy" log lines that don't
  correspond to a real problem.

Severity is **about user impact**, not technical depth. A crash deep in
the reconciler that self-heals on the next 30s tick is **not** P0 — it's
P2. A successful API response that silently fails to push the credential
to the remote cluster *is* P0, because the user-visible state is wrong.

If you cannot decide between two tiers, **pick the higher one** and
document why in the page. Re-tiering down is cheap; missing a real P0
is not.

### 3. Symptoms

```markdown
## Symptoms

What an operator sees when this fires:

- UI shows `<exact text>` on the `<which>` page
- `kubectl logs` line:
  ```
  <exact log line they would grep for>
  ```
- Alert `<AlertName>` is firing (from `charts/sharko/templates/prometheusrules.yaml`)
- HTTP response: `<status> <body>`
```

Symptoms are **observable, exact strings**. An operator should be able
to copy a substring from their pager / their log into Ctrl-F on this
page and land on the right runbook. If the symptom is "the dashboard
feels slow," that's not specific enough — name the metric, the
threshold, and the user-visible degradation.

Three rules:

1. **Symptoms come BEFORE diagnosis.** Operators recognize the symptom
   first, then look for the diagnosis. Reversing the order forces them
   to read past content they don't yet care about.
2. **Use exact text from the codebase.** If the error message is
   `"no active ArgoCD connection"`, write it verbatim — including
   capitalization and punctuation. Operators will grep for it.
3. **Include the alert name if applicable.** Cross-reference the
   `alertname` from `charts/sharko/templates/prometheusrules.yaml` so
   the alert's `runbook_url` annotation lands on the right anchor.

### 4. Diagnosis

```markdown
## Diagnosis

Where to look to confirm the failure mode and narrow it.

1. **<First check>** — Prometheus query, log grep, or health endpoint.
2. **<Second check>** — narrows further.
3. **<Third check>** — distinguishes this failure from adjacent ones.
```

Diagnosis is the funnel that takes an operator from "something is wrong"
to "I know which lane to mitigate in." Three checkable observations is
the floor; more is fine.

Each diagnosis step includes the exact command. Operators are pasting,
not typing.

```sh
kubectl logs -n <ns> -l app=sharko --tail=500 \
  | jq 'select(.level == "ERROR" and (.msg | test("register|cluster"; "i")))'
```

For metric-driven diagnosis, include the PromQL query inline (not just
"check the dashboard"):

```promql
sum(rate(sharko_cluster_registration_errors_total[5m]))
/
clamp_min(sum(rate(sharko_cluster_registration_total[5m])), 1e-9)
```

For log-driven diagnosis, lean on the V2-2.2 `request_id` correlation
pattern — every Sharko log line carries a `request_id`, and a single
`jq 'select(.request_id == "req-<id>")'` joins lines across middleware,
service, orchestrator, reconciler, and audit. The full pattern lives in
[`../developer-guide/logging.md`](logging.md#correlation-ids).

For health-endpoint diagnosis, use `GET /api/v1/health` (or the
operation-specific endpoint) and read the structured response. Don't
guess at internal state from log noise when there's an endpoint that
reports it directly.

### 5. Mitigation

```markdown
## Mitigation (try in order)

1. **<Most likely fix>** — rationale + the exact command(s).
2. **<Second most likely>** — rationale + commands.
3. **<Third>** — rationale + commands.
4. **<Last resort>** — rationale + commands. Mark explicitly as
   "last resort."
```

Mitigation is a **numbered list, not a bulleted list**, because the
order matters. The first item should be the most-likely-to-work fix; the
last item is the destructive or disruptive one ("restart the pod,"
"scale to zero"). An on-call operator works the list top to bottom and
stops at the first success.

Three to five steps is the right range. Fewer means the runbook is
under-documented; more means the failure mode is actually two failure
modes glued together — split it.

Mitigation steps include:

- The **rationale** in one sentence ("the most common cause is the
  ArgoCD service-account token expiring") — so the operator
  understands why this step might fix it.
- The **exact command** — copy-pasteable, not "use `kubectl` to
  check…".
- The **expected indicator of success or failure** — what output
  proves this step worked vs. didn't.

### 6. Root-cause patterns

```markdown
## Root-cause patterns

### <First common cause>

<1-3 paragraphs>: what this cause looks like in the logs, why it
happens, and how to confirm it.

### <Second common cause>

<1-3 paragraphs>.

### <Third common cause>

<1-3 paragraphs>.
```

Root-cause patterns are **the post-mortem section that lives in the
runbook**. After someone mitigated, this is what helps them write
the next preventative change. Two to four common causes is the floor;
each gets 1-3 paragraphs explaining what makes this cause distinctive
in the logs / metrics / behavior.

**This is NOT a duplicate of the mitigation section.** Mitigation tells
the operator what to *do*; root-cause tells them what was *wrong*. The
same operator reads both, but at different moments — mitigation first
(under pressure, scanning for the fix), root-cause after (planning the
follow-up issue).

### 7. Prevention

```markdown
## Prevention

How to make this failure mode less likely going forward.

- **Monitoring:** the specific alert or query that catches this earlier
- **Gating:** the CI check / pre-commit hook / Helm value that
  prevents the misconfiguration
- **Scheduled work:** the periodic task that catches drift before it
  becomes a page (e.g. credential rotation, token expiry watch)
```

Prevention is **forward-looking**. If your runbook says "the most
common cause is the ArgoCD token expiring," prevention says
"monitor the token expiry; alert at T-7d." If your runbook says "git
provider rate-limited," prevention says "watch
`github_rate_limit_remaining`; pre-rotate the PAT when it crosses
20%."

This section is **the hardest one to write** because it requires the
author to project forward, not just describe what shipped. Three
preventative measures is the floor. If you genuinely cannot think of
prevention beyond "fix the bug in the next sprint," write that
honestly — but the section is required, not optional.

`Prevention: TBD` is **not acceptable** and will fail review. If
prevention is genuinely unknown, write "No prevention identified —
this failure mode is reactive-only until <specific work> ships."

### 8. Related runbooks

```markdown
## Related runbooks

- [`<other-runbook>.md`](other-runbook.md) — when to escalate from this
  page to that page
- [`<another>.md`](another.md) — alternate failure mode with similar
  symptoms
```

Related runbooks is the cross-link footer. Two patterns:

- **Escalation links** — "if mitigation doesn't work, the failure
  has likely cascaded; see X."
- **Adjacent failure links** — "the symptom is similar to Y; if your
  symptom is actually Z, jump to Y."

If genuinely no other runbook is related, write exactly:

```markdown
## Related runbooks

No related runbooks at this time.
```

The compliance checklist looks for that exact phrase — do not invent a
related runbook just to fill the section.

---

## Optional sections

Three sections are conditional. Include them when applicable; skip them
when not.

### Rollback plan

When the runbook's mitigation involves an **operator action that can be
reversed** (a Helm upgrade, a config change, a manual `kubectl apply`),
include a rollback subsection. Format:

```markdown
## Rollback plan

If <mitigation step N> made things worse:

1. <How to undo step N exactly>
2. <How to confirm the rollback worked>
```

Skip this section for mitigations that are inherently safe (e.g.
"restart the pod") or read-only (e.g. "grep the logs").

### Monitoring queries

When the runbook diagnosis depends on a non-obvious metric or query,
include a `## Monitoring queries` section as a copy-paste reference.
Format:

```markdown
## Monitoring queries

### Detect this failure

```promql
<the alerting query>
```

### Quantify ongoing impact

```promql
<the saturation / error-budget query>
```
```

Skip this section if all the queries are already inlined in the
Diagnosis section and there is no second-order metric worth surfacing.

### Escalation contacts

Currently, Sharko's escalation contact is **the maintainer's email,
`moran.weissman@gmail.com`**. The runbook should say this explicitly,
and it should say it in one specific shape that all runbooks share:

```markdown
## Escalation

If the mitigations above do not resolve the failure within <window>,
email the maintainer: `moran.weissman@gmail.com`. Include:

- The runbook URL you used (this page)
- The exact alert name (if applicable) or the error message
- The Sharko version (`sharko version` or the Helm chart version)
- A 5-minute window of relevant logs (filtered by `request_id` per the
  [correlation pattern](../developer-guide/logging.md#correlation-ids))

The maintainer is a single human, not a 24×7 rotation. Expect a
business-day SLA, not a paged response. As Sharko matures past CNCF
sandbox, this section will be replaced with on-call rotation details.
```

This is a **transition state**. The section as written acknowledges
that Sharko is a small-maintainer project, sets a realistic SLA
expectation, and reserves a slot for the post-CNCF-sandbox on-call
rotation. Do not pretend Sharko has 24×7 escalation paths it does not
have.

---

## Tone discipline

The voice is **operator-on-call**, not **planning-meeting**. The reader
has a pager going off. They want imperatives, exact commands, and an
honest assessment of severity. They do not want marketing copy, breezy
framing, or asides about how the system was designed.

### Good vs. bad examples

#### Intro

**Good:**

> Cluster registration is failing fast enough that if the current rate
> continued, the entire monthly 99.9% error budget would be consumed in
> ~2 days. This is the path platform engineers use to onboard new
> clusters — sustained failure here directly blocks fleet expansion.
> Page on-call.

**Bad:**

> Sharko's powerful cluster registration system is occasionally
> experiencing some hiccups. As you know, registration is a really
> important part of the platform, and we want to make sure it works
> smoothly! Don't worry — we have some great mitigations below.

The good version states severity, impact, and time horizon. The bad
version is breezy, condescending, and information-free. Reject any PR
where the intro reads like marketing copy.

#### Mitigation step

**Good:**

> 1. **ArgoCD reachability** — `kubectl -n argocd get pods` and
>    `kubectl -n argocd logs deploy/argocd-server --tail=200`. The
>    `argocd_secret_created` phase writes a Secret via the kube API
>    and then relies on `argocd-application-controller` to surface it;
>    if either is unhealthy, registration fails downstream of Sharko.

**Bad:**

> 1. First, you should probably check ArgoCD. It's the upstream
>    dependency and it can sometimes have issues. The logs are
>    usually a good place to look — see what they say.

The good version names the exact `kubectl` invocation, explains why
this is the first check, and reports what failure looks like. The bad
version is vague handwaving.

#### Symptoms

**Good:**

```markdown
- UI shows `"This cluster uses AWS IAM authentication. Configure AWS
  credentials for the Sharko pod's role to enable Test."` on the
  Cluster detail page when the operator clicks "Test cluster"
- API: `POST /api/v1/clusters/{name}/test` returns `503 Service
  Unavailable` with body `{"error": "iam_auth_unsupported"}`
- No specific alert fires (known limitation, not a runtime failure)
```

**Bad:**

```markdown
- Something doesn't work with AWS clusters
- The UI shows an error
- It should be obvious from the message
```

The good version gives the operator an exact string to search for. The
bad version is the runbook equivalent of "have you tried turning it off
and on again."

### Anti-patterns to reject

- **"Should be obvious"** — if it were obvious, the operator wouldn't
  be on the page. Spell it out.
- **"Just check the logs"** — name the log line, the level, the
  field, the filter. "Just check" is not actionable.
- **"As you know, …"** — assume the reader doesn't know; that's why
  they opened the runbook.
- **"Our powerful X"** — marketing voice. Strip it.
- **"TLDR-free intro"** — every runbook intro must, in 2-3
  sentences, tell the reader what's broken, how bad it is, and
  whether they need to wake someone up.
- **Section names that don't match the style guide** — "Solutions"
  instead of "Mitigation", "Errors" instead of "Symptoms", "What to
  do" instead of "Mitigation." Standardize so operators don't have
  to retrain their eye per page.
- **Bullets where there should be numbered steps** — mitigation is
  ordered; bullets imply unordered. Use 1. 2. 3. not - - -.
- **Emoji decoration** — runbook content is read in a terminal,
  rendered in MkDocs, and pasted into Slack incident channels. Emoji
  decoration breaks at least one of those surfaces. Words only.

---

## Length

Target range: **300-800 lines per runbook.**

- **Below 300 lines** — the runbook is under-documented. Either you
  skipped a required section, or the failure mode is too narrow to
  justify its own page. Either flesh it out or merge it into a
  related runbook.
- **Above 800 lines** — the runbook is doing too much. Either you're
  documenting two failure modes that should be split into two pages,
  or you're including system-overview content that belongs in the
  developer guide.

These are heuristics, not hard caps. A complex failure mode with five
common root causes and an extensive monitoring section can run to ~900
lines and still be the right shape. A trivially-bounded failure mode
(e.g.
[`aws-iam-cluster-auth.md`](../operator/aws-iam-cluster-auth.md))
can run to ~150 lines because the entire fix is a single workaround;
that page is honest about its scope and does not need to pad to 300.

If you're outside the target range, **say so explicitly** in the
runbook's intro:

> This page is short because the entire mitigation is a single config
> change. If you need a workaround for the underlying limitation, see
> <link>; everything else is tracked under <epic>.

Or:

> This page is long because <failure mode> has five distinct
> root-cause clusters and the mitigations differ enough that splitting
> would force operators to read two pages to find their lane. Section
> 5 ("Root-cause patterns") is the longest; jump there if mitigation
> didn't resolve.

---

## When to write one runbook vs. multiple

Sometimes a single failure has multiple root causes that share
mitigations. Sometimes multiple failures share a root cause. The
decision rule:

**One runbook covers multiple failure modes IF AND ONLY IF they share
the same diagnosis path AND the same mitigation steps.**

Examples:

- **One runbook is correct:** "Reconciler tick failed" covers
  git-fetch-failed, vault-get-failed, and schema-validation-failed
  because the diagnosis path is the same (look at the reconciler log,
  find the `audit.action` value, route to that subcase) and the
  mitigation steps overlap (the first three steps are identical; only
  step 4 differs per subcase).
- **Two runbooks are correct:** "Cluster registration broken (ArgoCD
  outage)" and "Cluster registration broken (Git provider PAT
  expired)" — same symptom, but the diagnosis path is materially
  different (look at ArgoCD vs. look at the Git provider) and the
  mitigation steps are entirely separate.

When in doubt, **start with one runbook** and split when the merged
page exceeds 800 lines or when the conditional logic ("if symptom A
then steps 1-3; if symptom B then steps 4-6") starts to dominate the
mitigation section.

The failure-mode index
([`docs/site/operator/failure-mode-index.md`](../operator/failure-mode-index.md))
records the grouping decision per failure mode in its `Notes` column
so future readers can see the rationale.

---

## Cross-linking conventions

Runbooks live in two places: `docs/site/operator/` (operator-facing) and
`docs/site/developer-guide/` (contributor-facing). Cross-link them
**relative to the file's directory**:

- From `docs/site/operator/foo.md` to another operator page:
  `[link](bar.md)` — no `./` prefix needed.
- From `docs/site/operator/foo.md` to a developer-guide page:
  `[link](../developer-guide/bar.md)`.
- From `docs/site/developer-guide/foo.md` to an operator page:
  `[link](../operator/bar.md)`.

Anchor links use the auto-generated MkDocs slug: a section titled
`## Reconciler Crash Loop` becomes `#reconciler-crash-loop`. Verify the
anchor by building locally with `mkdocs build --strict` (broken links
fail the build).

Do **not** cross-link to internal Slack channels, ticketing URLs, or
employee email addresses. Sharko is an open-source project; the docs
are public. The only acceptable external escalation contact is the
maintainer email documented above.

---

## Verification before merging

Per `.claude/team/docs-writer.md` (the docs-writer agent role file in
this repo), every operator runbook MUST carry a `> **Verified:** ...`
header. The header records:

- **When** the page was last verified-by-execution (date)
- **Against what** (specific commit / PR / shipped surface)
- **Re-verify expectation** (what kind of change requires
  re-verification)

Example:

```markdown
> **Verified:** Authored 2026-06-01 against the V2-3.3 alerts shipped
> in `charts/sharko/templates/prometheusrules.yaml` (PR #372). Every
> alert in that file has a 1:1 section below; section anchors match
> the `runbook_url` annotation of each alert. Re-verify before
> changing alert names, expressions, or thresholds — the
> recording-rule names, alert names, and runbook anchors are
> load-bearing together.
```

The reviewer rejects runbook PRs that:

- Add a runbook without the header
- Modify a runbook without updating the header date
- Carry a header date older than the most recent commit to the
  runbook file

**Authoring discipline:** never write runbook steps you have not
personally executed. Read-only inspection of code is insufficient.
This rule exists because a runbook authored without execution (the
historical "BUG-015" lesson) shipped a broken procedure that blocked
the maintainer's smoke pass. Every step in the runbook must have been
typed into a real terminal against a real system.

---

## Compliance checklist

Every operator runbook PR includes this checklist in its description.
Reviewers paste-run it per file. The PR 2 (V2-4.3) and PR 3 (V2-4.4)
agents in the V2-4 sprint run this checklist programmatically per
runbook.

```markdown
- [ ] Title matches `# <Failure name>` (not "Documentation for X" or
      "X Overview")
- [ ] `**Severity:**` line present on first non-title line (P0 / P1 / P2)
- [ ] Verified-by-execution header present and date current
- [ ] Symptoms section appears BEFORE Diagnosis (not after)
- [ ] Symptoms include exact log lines / error messages / alert names
      (copy-pasteable strings, not paraphrases)
- [ ] Diagnosis has 3+ concrete checks (PromQL, log grep, or health
      endpoint), each with exact command
- [ ] Mitigation uses numbered list (1. 2. 3.) not bullets
- [ ] Mitigation has 3-5 steps in priority order, each with rationale +
      exact command
- [ ] Root-cause patterns section present with 2+ named causes,
      1-3 paragraphs each
- [ ] Prevention section present and non-empty (NOT "TBD")
- [ ] Related runbooks section present with at least one link OR the
      exact phrase "No related runbooks at this time"
- [ ] Intro is operator-on-call voice (no marketing copy, no "should
      be obvious," no "as you know")
- [ ] Length is 300-800 lines (or page explicitly justifies being
      outside that range in the intro)
- [ ] All cross-links resolve (`mkdocs build --strict` clean)
- [ ] No emoji decoration; no internal Slack / employee email references
- [ ] If applicable, alert name from
      `charts/sharko/templates/prometheusrules.yaml` is referenced and
      the section anchor matches the alert's `runbook_url` annotation
```

A runbook PR that fails any of these gets sent back for revision. The
checklist is the contract between the author and the reviewer.

---

## References

- [SRE workbook — On-Call](https://sre.google/workbook/on-call) — the
  canonical guidance that on-call playbook entries reduce stress,
  improve MTTR, and minimize human error. The required-sections
  structure in this guide is the on-call chapter's playbook entry
  pattern adapted to Sharko's surface.
- [SRE workbook — Implementing SLOs](https://sre.google/workbook/implementing-slos)
  — the SLO-driven alert vocabulary that the V2-3 alerts and this
  guide's severity tiers (P0 / P1 / P2) align with.
- [SRE book — Practical Alerting](https://sre.google/sre-book/practical-alerting)
  — the upstream reference for the "every alert has a playbook
  entry" rule that gates V2-3.3 and V2-4.
- [`docs/site/operator/budget-burn-runbook.md`](../operator/budget-burn-runbook.md)
  — the in-repo exemplar (V2-3.4) of every section in this style
  guide. Read this page alongside the style guide to see the rules
  applied.
- [`docs/site/operator/failure-mode-index.md`](../operator/failure-mode-index.md)
  — the inventory of every operator-facing failure mode in Sharko,
  mapped to its runbook URL (or marked `GAP` if no runbook exists
  yet). The index is the worklist that drives V2-4.3 (write missing
  runbooks) and V2-4.4 (refresh existing runbooks against this style
  guide).
- [`docs/site/developer-guide/logging.md`](logging.md) — the V2-2.2
  `request_id` correlation pattern that every runbook's Diagnosis
  section leans on for log-driven debugging.
- [`.claude/team/docs-writer.md`](https://github.com/MoranWeissman/sharko/blob/main/.claude/team/docs-writer.md)
  — the docs-writer agent role file in this repo, including the
  verified-by-execution rule and the `mkdocs --strict` build gate.
