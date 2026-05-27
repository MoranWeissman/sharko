# Logging Audit Punch List

> **Verified:** Audit conducted 2026-05-27 against `main` HEAD as part of
> V2-2.3 (closing the V2-2 logging-hardening epic). Counts and file:line
> refs are accurate as of that snapshot. **This is a working doc:** items
> get fixed in follow-up sprints and removed from this list as closed.
> When every item is closed, delete the file.

This file is **not** the discipline doc — that's
[`logging.md`](logging.md). This file is a punch list of concrete
level-discipline and call-site issues found during the V2-2.3 audit.
Issues are filed here, **not fixed in V2-2.3**; the V2-2.3 gate is
**decision + doc**, per the sprint brief.

## Audit scope

- Surface: every `slog.{Debug,Info,Warn,Error}` call in `internal/` and
  `cmd/` (excluding `_test.go`).
- Snapshot stats:
  - **53 files** contain slog calls (162 Info / 65 Warn / 36 Error /
    17 Debug = 280 total).
  - **0** files use stdlib `log` (V2-2.1 sweep + CI gate prevents
    regression).
  - **0** `slog.X(fmt.Sprintf(...))` anti-patterns found
    (`grep -rEn 'slog\.(Info|Warn|Error|Debug)\([^)]*%[svdwT]'` returns
    no matches).

## Headline finding (marquee)

### auth/store.go:634 — bootstrap admin password logged in plain text

```go
slog.Info("bootstrap admin generated", "username", "admin", "password", password)
```

**Severity: HIGH (operational secret exfiltration via log scrape).**

The bootstrap-admin path emits the freshly-generated admin password as a
plain attribute. The V2-2.4 `RedactHandler` wrapper (PR #368) now
catches this — the `password` key matches the sensitive-key heuristic
and the value collapses to `[REDACTED]` before serialization — but the
**call site is still wrong** for three reasons:

1. **Defense-in-depth violation.** A future refactor of the wrapper,
   a new log sink that bypasses the wrapper (e.g. a direct file dump
   for incident capture), or a copy-paste of this slog call into a
   non-slog logger silently re-introduces the leak.
2. **The intent is to display the password to the operator.** The
   redaction wrapper now actively defeats this — operators see
   `password=[REDACTED]` in the log and lose the credential. The
   bootstrap path also writes `sharko-initial-admin-secret` (lines
   646-657), so log-scraping is no longer the only retrieval path,
   but the broken log message is now a UX bug.
3. **Even the surrounding ASCII banner is now misleading.** Lines
   633-636 read:
   ```
   === BOOTSTRAP ADMIN CREDENTIAL ===
   bootstrap admin generated username=admin password=[REDACTED]
   This is the only time this credential will be shown. Store it securely.
   === END BOOTSTRAP ADMIN CREDENTIAL ===
   ```
   "This is the only time this credential will be shown" is now a lie
   when `[REDACTED]` is what shows up.

**Recommended fix (follow-up sprint):**
- Remove the password from the slog call entirely.
- Rewrite the banner to direct operators to
  `kubectl get secret sharko-initial-admin-secret`.
- If display-in-log is intentionally still supported (matches ArgoCD's
  default), gate it behind an explicit env var
  (`SHARKO_LOG_BOOTSTRAP_PASSWORD=true`) AND use the `_unsafe_`
  prefix so the bypass is explicit at the call site.

---

## Info → Debug candidates

Per-iteration / per-tick events that should not fire on the default
`info` level. These flood the log stream in production and obscure
genuine state-transition Info lines.

- [ ] `internal/secrets/reconciler.go:192` —
  `log.Info("[secrets] reconcile started")` fires every 5 minutes
  (default reconcile interval). Lift to **Debug** and keep only the
  `[secrets] reconcile complete` Info summary line (281).
- [ ] `internal/argosecrets/reconciler.go:178` —
  `log.Info("[argosecrets] reconcile started")` fires every 3 minutes.
  Same pattern: lift to **Debug**; keep the line-305 summary as Info.
- [ ] `internal/orchestrator/secrets.go:80` —
  `log.Info("[secrets] createAddonSecrets called", "addonCount", ...)` —
  trace-style "called" log; lift to **Debug**.
- [ ] `internal/orchestrator/secrets.go:92` —
  `log.Info("[secrets] fetching secret value", ...)` — fires per secret
  inside a loop; lift to **Debug**.
- [ ] `internal/orchestrator/secrets.go:108` —
  `log.Info("[secrets] pushing secret to cluster", ...)` — fires per
  push inside a loop; lift to **Debug**. Keep the summary at the end of
  the operation at Info.
- [ ] `internal/orchestrator/secrets.go:165, 195` —
  `log.Info("secret already gone", ...)` — fires per missing secret in a
  cleanup loop; lift to **Debug**.
- [ ] `internal/api/clusters_discover.go:189` —
  `slog.Info("[cluster-test] fetching credentials", ...)` — fires per
  cluster-test request *before* the actual operation; the operation
  itself logs Info on completion. The pre-fetch trace should be
  **Debug**.
- [ ] `internal/ai/memory.go:45` —
  `slog.Info("agent memory loaded", "entries", N)` — fires on every
  agent session init. Either lift to **Debug** OR keep at Info if
  session inits are genuinely rare in practice.
- [ ] `internal/api/init.go:384` —
  `slog.Info("init operation abandoned — no heartbeat from client", ...)`
  — actually this might belong at **Warn**, not Debug. An abandoned
  init is a degraded condition; client crashed mid-flight. Reclassify
  to Warn.

## Warn → Error candidates

Cases where a "fallback / continuing" log line hides a real failure
that leaves user-visible state inconsistent.

- [ ] `internal/secrets/reconciler.go:204, 209` —
  `log.Warn("[secrets] failed to read catalog", "error", err)` and
  `log.Warn("[secrets] failed to parse catalog", "error", err)`. If
  the catalog read or parse fails, the reconciler returns and **all
  secret reconciliation is silently skipped for this cycle** — the
  next tick will retry, but until then no secrets are pushed. This is
  "we recovered by giving up", not "degraded but functional." Reclassify
  to **Error** so it pages.
- [ ] `internal/secrets/reconciler.go:232, 237` — same pattern for
  `managed-clusters.yaml`. Same reclassification.
- [ ] `internal/secrets/reconciler.go:367` —
  `log.Warn("[secrets] secret rotated, updating", ...)` — a rotation
  is a meaningful state transition, not a degraded condition. Reclassify
  to **Info** (move *down* — Warn here is over-alarming and creates
  noise in pager queries).

## Error → Warn candidates

Cases logged as Error where the system fully recovered without operator
intervention.

- [ ] None found in this audit pass. The codebase generally errs on the
  side of under-alarming (more Warn → Error candidates than the
  reverse). If a follow-up sprint introduces retry loops, re-audit the
  pre-success failure logs and demote them to Warn.

## Sprintf-into-message offenders

- [ ] None found. (`grep -rEn 'slog\.(Info|Warn|Error|Debug)\([^)]*%[svdwT]'` is clean.)
  CI does not yet enforce this — consider adding a grep gate in
  `.github/workflows/ci.yml` to prevent regression. **(Optional
  follow-up — not blocking.)**

## Sensitive-field-by-key offenders

The V2-2.4 RedactHandler wrapper catches all of these. The **call sites
are still wrong** for the defense-in-depth reasons documented in
[`logging.md`](logging.md#critical-rule).

- [ ] **`internal/auth/store.go:634`** — `"password"` attribute holds
  the raw bootstrap admin password. See [headline finding](#headline-finding-marquee)
  above.
- [ ] `internal/ai/memory.go:84` —
  `slog.Info("agent memory saved", "content", content, "category", category)`.
  The `content` attribute is the raw memory entry — likely user-supplied
  conversational text that could contain sensitive operational details
  pasted into the AI assistant by an operator. The wrapper does NOT
  catch arbitrary user prose, so this is a real leak surface. Fix:
  log a fingerprint + length, not the body. **Severity: MEDIUM.**

### False-positive call sites (wrapper over-redacts a resource name)

These are not security bugs but UX bugs introduced by the redaction
wrapper. Renaming the attribute key prevents the false-positive without
weakening the wrapper.

- [ ] `internal/auth/store.go:126` —
  `slog.Info("auth store initialized in K8s mode", "namespace", ..., "secret", s.secretName)`.
  The `secret` key triggers the wrapper; the value is a Secret resource
  *name*, not a Secret body. Rename attr to `secret_name` so the
  message reads cleanly.
- [ ] `cmd/sharko/serve.go:126` —
  `slog.Info("connection config stored in encrypted k8s secret", "namespace", namespace, "secret", secretName)`.
  Same fix: rename attr to `secret_name`.

## Double-logging at multiple layers

A focused review found no obvious double-log offenders in the current
slog surface. (The previous audit-middleware + handler-log split was
resolved when audit emission moved fully to the dedicated
`auditMiddleware` chain.) Re-audit after every new feature: it is the
easiest anti-pattern to re-introduce inadvertently.

## Process / tooling follow-ups (separate from level discipline)

These are not call-site fixes; they are gaps that, if closed, would
prevent regressions in the discipline `logging.md` codifies.

- [ ] Add a `make logs-lint` target that runs:
  - `grep -rEn 'slog\.(Info|Warn|Error|Debug)\(fmt\.Sprintf' internal/ cmd/`
    (fail on any match — Sprintf-into-message anti-pattern).
  - `grep -rEn 'slog\.(Info|Warn|Error|Debug)\([^)]*"password"' internal/ cmd/`
    (warn on any match — manual review whether the value is the secret
    body or just a name).
- [ ] Add the same checks as a CI step (cheap regex, runs in seconds).
- [ ] Consider a periodic "Info-rate sampling" check in CI smoke runs:
  count Info lines per minute during the e2e suite; if a new PR
  dramatically increases per-minute Info rate, flag for review (catches
  silent regressions where someone adds a new Info log inside a tick).

---

## Maintenance

- **When closing an item:** delete its bullet from this file (don't
  strikethrough — the file is a *current* punch list, not a changelog).
  Reference the closing commit/PR in the commit message that removes
  the bullet.
- **When the list is empty:** delete this entire file. The discipline
  doc (`logging.md`) is the permanent reference; this file's only
  reason to exist is the open items.
- **Re-audit cadence:** every minor release. Run the same surface scan
  and add any new findings here.
