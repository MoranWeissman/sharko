# Logging Discipline Guide

> **Verified:** Authored 2026-05-27 against the V2-2 logging epic surface
> (PR #367 slog sweep + correlation IDs + PR #368 RedactHandler wrapper).
> Snippets are read directly from `internal/logging/`, `internal/api/router.go`,
> `internal/clusterreconciler/reconciler.go`, and `cmd/sharko/serve.go` as
> shipped on `main` at the time of writing. Re-verify before changing
> patterns: the package boundary and wrapper-installation order are
> load-bearing.

Sharko emits structured JSON logs through Go's `log/slog`. This guide
codifies the discipline that keeps those logs **`jq`-able, on-call-friendly,
and secret-safe**. New code should be right by construction; the
`RedactHandler` from V2-2.4 is the safety net, **not** the license to log
secrets.

If you read only one section: jump to the [level matrix](#level-matrix) and
the [don't do](#dont-do-anti-patterns) list. Everything else is supporting
material.

---

## Why we care

- **Operability** — every log line is JSON; operators run `jq` across the
  stream without per-component format-normalization. Mixed-format logs are
  a 3am-page problem.
- **Correlation** — every request carries a `request_id`. Tracing one
  cluster registration through middleware → service → orchestrator →
  reconciler → audit takes one `jq 'select(.request_id=="req-...")'`,
  not five `grep`s with different field shapes.
- **Secret safety** — credential-shaped values are redacted at the handler
  boundary. A leak in a log line is a leak in a SOC2 audit, a CNCF
  due-diligence review, and an angry customer email at the same time.
- **CNCF maturity expectation** — incubation-grade projects ship
  structured-logging discipline, request correlation, and redaction. We
  bake them in now so they don't become a graduation blocker later.

---

## Architecture overview

The logging stack has three layers, wired once at server startup in
`cmd/sharko/serve.go`:

```
slog call site            ┌──────────────────┐
"slog.Info(...)"  ────►   │  RedactHandler   │  (V2-2.4 wrapper — first in chain)
                          └────────┬─────────┘
                                   │ redacts sensitive attr values
                                   ▼
                          ┌──────────────────┐
                          │  JSONHandler     │  (stdlib — serializes to JSON)
                          └────────┬─────────┘
                                   │
                                   ▼
                                stdout  ──►  container log driver
                                              (kubectl logs, Loki, etc.)
```

Two non-negotiables:

1. **`RedactHandler` is FIRST in the chain.** If you wire it after the
   JSON handler, the JSON handler will serialize the raw value before
   redaction runs — defeating the entire wrapper.
2. **`slog.SetDefault` once, at process start.** Every package calls
   `slog.Info` / `slog.Warn` / etc. against the package-level default —
   there is no `sharko/log` re-export, no DI, no logger argument threaded
   through every function. The wrapper installation is the only
   centralized configuration.

The level is controlled by the `SHARKO_LOG_LEVEL` env var (`debug` /
`info` / `warn` / `error`). Default is `info`. Helm sets this via
`config.logLevel`.

---

## Correlation IDs

Every log line emitted in the context of a request carries a
`request_id` attribute. The ID has two shapes:

- **`req-<16-hex>`** — generated at the API boundary by
  `requestIDMiddleware` in `internal/api/router.go`. The middleware
  honours an inbound `X-Request-ID` header (capped at 128 bytes) so a
  caller can trace its own request end-to-end; otherwise it generates
  a fresh `req-<hex>` via `logging.NewRequestID()`.
- **`<source>-<unix_ts>`** — synthetic IDs for background work that has
  no request:
  - `recon-<ts>` — cluster reconciler tick (30s cadence)
  - `prtrack-<ts>` — PR tracker poll
  - `secrets-<ts>` — secrets reconciler tick
  - `prtrack-startup-<ts>` — startup reconcile pass

The shape is intentional: the `req-` prefix means "an HTTP caller drove
this"; everything else means "this happened on a timer or a trigger." A
single `jq 'select(.request_id | startswith("recon-"))'` filters every
reconciler line ever emitted.

### Patterns

**Read the ID from context:**

```go
import "github.com/MoranWeissman/sharko/internal/logging"

id := logging.RequestID(ctx)  // returns "" if no ID is set
```

**Build a contextual logger (preferred for functions with 2+ log lines):**

```go
func (s *Service) DoWork(ctx context.Context) {
    log := logging.LoggerFromContext(ctx)  // slog.Default() + request_id attr
    log.Info("work started")
    // ... do work ...
    log.Info("work completed", "items", n)
}
```

**Inline at a single call site (preferred when you log once):**

```go
slog.Info("one-shot event",
    "request_id", logging.RequestID(ctx),
    "cluster", name)
```

**Or use `logging.Attr` with `slog.LogAttrs`:**

```go
slog.LogAttrs(ctx, slog.LevelInfo, "one-shot event",
    logging.Attr(ctx),
    slog.String("cluster", name))
```

**Background work — generate a synthetic ID at the top of the tick:**

```go
func (r *Reconciler) tick() {
    ctx := logging.WithRequestID(context.Background(),
        fmt.Sprintf("recon-%d", time.Now().Unix()))
    r.reconcileOnce(ctx)
}
```

Use Unix seconds — cheaper than a UUID, naturally sorted, easily searched.

---

## Level matrix

The single most important table in this doc. Apply it consistently and the
on-call pager stays quiet.

| Level   | Default-on in prod? | Use for | Examples |
|---------|---------------------|---------|----------|
| `Debug` | No (`info` is default) | Verbose flow tracing; per-tick heartbeats; pre-fetch + post-fetch state diffs; hash comparisons; "unchanged, skipping" notices | reconciler "no change since last tick", `hash comparison`, individual fetch/parse steps inside a loop |
| `Info`  | Yes — should be **LOW volume** | Meaningful state transitions: something the operator would want to see when reading the log without `grep` | "cluster registered", "addon enabled", "PR merged", "reconciler converged with N changes", "server listening on :8080" |
| `Warn`  | Yes | Degraded but **recovered** conditions: the system handled it, but someone might want to know | "retry triggered", "fallback path taken", "eventual-consistency not yet converged", "deprecated API used", "rate limit hit" |
| `Error` | Yes | Failed operations the system could **NOT** recover from autonomously | handler returned 5xx, reconciler failed to converge after retries, credential refresh failed, secret write rejected by remote cluster |

### Mnemonic

- **Debug = a developer's diary** — verbose, only when explicitly opened.
- **Info = the day's headlines** — every line earns its place; an
  operator reading raw `kubectl logs` should not feel they need to
  `grep`.
- **Warn = the doctor frowned but you're walking out** — degraded,
  recovered, worth knowing.
- **Error = the page** — a human needs to look. If you don't want to
  page on it, it's a Warn.

### Common misclassifications

- **Info → Debug** — anything inside a loop that fires per-iteration; a
  reconciler that emits `Info` per cluster on every tick will drown the
  log within minutes. Lift the per-iteration to Debug; emit one Info
  summary at the end (`"reconcile complete", "checked", 47, "updated", 2`).
- **Warn → Error** — a "silent fallback" that's actually a data-loss
  bug (e.g. "failed to write secret, continuing"). If the user-visible
  state is now wrong, it's an Error.
- **Error → Warn** — recovery worked (retry succeeded on the third
  attempt). The retry path itself logs Warn; only the final
  unrecoverable failure logs Error.

---

## Sensitive-field discipline

The `RedactHandler` wrapper (V2-2.4, `internal/logging/redact.go`) sits
first in the slog handler chain and walks every attribute on every
record. It redacts values that match any of three detectors:

1. **Sensitive-key heuristic** — attribute KEY matches a canonical
   credential name (`token`, `password`, `kubeconfig`, `secret`, `pat`,
   `authorization`, `api_key`, ...) or has a credential-shaped suffix
   (`_token`, `_password`, `_secret`, `_key`). Case-insensitive.
2. **JWT-shape detector** — attribute VALUE matches
   `^eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`.
3. **Base64-blob detector** — attribute VALUE is >100 chars and
   consists entirely of the base64 alphabet.

All three collapse to the literal string `[REDACTED]` — deliberately
type-blind so a reader of the logs cannot tell whether the redacted
value was a JWT, a kubeconfig, or a password.

### Critical rule

**The wrapper is a safety net, not a license.** Treat
`slog.X("...", "token", value)` as a bug — the wrapper saves you, but
the bug is still in the code. Reviewers should reject PRs that pass
raw credentials to slog even when the wrapper would catch them.

Why: redaction is a defense-in-depth layer, not a primary control. A
future refactor of the wrapper, a new sink that bypasses the wrapper,
or a copy-paste of the slog call to a non-slog logger all silently
reintroduce the leak. Fix the call site.

### Patterns when you need a stable identifier for a secret

If you need to correlate two log lines referring to the same credential
without leaking the credential itself, log a hash prefix instead:

```go
import (
    "crypto/sha256"
    "fmt"
)

func tokenFingerprint(token string) string {
    sum := sha256.Sum256([]byte(token))
    return fmt.Sprintf("sha256:%x", sum[:8])  // 16 hex chars, irreversible
}

slog.Info("token rotated", "fingerprint", tokenFingerprint(newToken))
```

Or, for kubeconfig / certificate material:

```go
// Just log the length and a fingerprint — never the body.
slog.Info("kubeconfig received",
    "cluster", name,
    "length_bytes", len(kc),
    "fingerprint", tokenFingerprint(string(kc)))
```

### What is NOT sensitive

Resource **names** are not sensitive. `"secret_name": "argocd-cluster-prod-eu"`
is fine — it's the name of a K8s Secret resource, not the secret body.
The redaction wrapper currently has false positives on the field name
`secret` when the value is just a name; the call site should rename the
field to `secret_name` to be both clearer and not-redacted. See the
[audit punch list](logging-audit-punchlist.md) for the open items.

---

## The `_unsafe_` opt-out

There is one escape hatch: an attribute key with the prefix `_unsafe_`
bypasses ALL three redaction detectors. This is for **deliberate
dev-debug instrumentation** where the operator explicitly wants the raw
value in the log.

```go
slog.Debug("raw kubeconfig for one-shot debug",
    "_unsafe_kubeconfig", string(kc))  // bypasses redaction
```

### When (and only when) to use `_unsafe_`

- A bug reproduction that requires the full raw value, gated behind a
  log level the operator must explicitly opt into (`Debug`).
- A one-shot diagnostic added during an incident, with a TODO to remove
  it before the next release.

### Review expectation

**Every PR introducing `_unsafe_` MUST be flagged in code review.** The
prefix is intentionally ugly — it should look wrong in a diff and
trigger a reviewer comment. If the reviewer cannot articulate why the
opt-out is necessary, the answer is to use a fingerprint instead.

No global "disable redaction" flag exists by design. A stray
`_unsafe_` import cannot silently widen the surface — every leak is
explicit at the call site, and every call site is grepable.

---

## Common patterns (copy-pasteable)

### Request-scoped logging in an HTTP handler

```go
func (s *Server) handleRegisterCluster(w http.ResponseWriter, r *http.Request) {
    ctx := r.Context()
    log := logging.LoggerFromContext(ctx)  // request_id already attached by middleware

    var req RegisterClusterRequest
    if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
        log.Warn("invalid register-cluster body", "error", err)
        writeError(w, http.StatusBadRequest, "invalid JSON")
        return
    }

    result, err := s.clusterSvc.Register(ctx, req)
    if err != nil {
        log.Error("cluster register failed", "cluster", req.Name, "error", err)
        writeError(w, http.StatusInternalServerError, "registration failed")
        return
    }
    log.Info("cluster registered", "cluster", req.Name, "pr_id", result.PRID)
    writeJSON(w, http.StatusCreated, result)
}
```

### Background-work synthetic ID generation

```go
func (r *Reconciler) Start(ctx context.Context) {
    go func() {
        ticker := time.NewTicker(r.tickInterval)
        defer ticker.Stop()
        for {
            select {
            case <-ctx.Done():
                return
            case <-ticker.C:
                tickCtx := logging.WithRequestID(ctx,
                    fmt.Sprintf("recon-%d", time.Now().Unix()))
                r.reconcileOnce(tickCtx)
            }
        }
    }()
}
```

### Adding domain attrs once with `.With(...)`

```go
func (s *Service) ReconcileCluster(ctx context.Context, name string) error {
    log := logging.LoggerFromContext(ctx).With("cluster", name)
    log.Info("reconcile started")
    // ... every log call here automatically carries request_id AND cluster
    if err := s.fetchState(ctx, name); err != nil {
        log.Error("fetch failed", "error", err)
        return err
    }
    log.Info("reconcile complete")
    return nil
}
```

Use `.With(...)` when:
- The function has 3+ log lines AND
- The shared attribute is meaningful to every one of them.

Don't use `.With(...)` for one-off lines — inline KV pairs read better.

### Error-with-stack pattern

`slog` does not capture stack traces natively. For a fatal that requires
post-mortem analysis, log the chained error:

```go
if err := s.doDangerous(ctx); err != nil {
    log.Error("dangerous op failed",
        "operation", "register_cluster",
        "cluster", name,
        "error", err)  // %v formatted by slog; chain via fmt.Errorf("...: %w", ...)
    return fmt.Errorf("register cluster %s: %w", name, err)
}
```

The `fmt.Errorf("...: %w", err)` chain at the return point preserves the
call-graph context for the outermost handler, which logs once with the
fully chained message.

### Counter / timer pattern (V2-3 Prometheus follow-up)

Until V2-3 ships Prometheus counters for the slog event stream, use a
consistent attribute shape so the events are derivable from log
aggregation today AND scrapeable as metrics tomorrow:

```go
start := time.Now()
err := s.doWork(ctx)
log.Info("work completed",
    "operation", "register_cluster",
    "result", resultLabel(err),       // "success" | "failure"
    "duration_ms", time.Since(start).Milliseconds())
```

Keep `operation` and `result` as low-cardinality labels — they will map
cleanly to Prometheus labels when V2-3 lands.

---

## Don't do (anti-patterns)

### 1. Sprintf-into-message instead of structured attrs

```go
// BAD — value is buried in the message; not queryable.
slog.Info(fmt.Sprintf("cluster %s registered with %d addons", name, n))

// GOOD — every field is a queryable attr.
slog.Info("cluster registered", "cluster", name, "addon_count", n)
```

The whole point of slog is that the message is a fixed-cardinality
string and the variables are attributes. `jq 'select(.addon_count > 5)'`
works on the GOOD version and is impossible on the BAD one.

### 2. Logging the same event twice at different layers

```go
// BAD — handler logs, service logs, orchestrator logs the same fact.
// Result: one user action = three log lines saying "cluster registered"
// at three layers with three slightly-different shapes.

// GOOD — the bottom layer logs the canonical event; upper layers log
// only what THEY uniquely know (e.g. that a request came in, an HTTP
// status was returned).
```

If two layers want to log the same fact, push the log to the deepest
layer that has the full context, and have the upper layers contribute
their unique pieces (request boundary, status code) instead.

### 3. Logging entire request / response bodies

```go
// BAD — body might contain a token, a kubeconfig, or 4MB of YAML.
slog.Debug("got request", "body", string(bodyBytes))

// GOOD — log a fingerprint or summary.
slog.Debug("got request",
    "content_length", len(bodyBytes),
    "fingerprint", tokenFingerprint(string(bodyBytes)))
```

The redaction wrapper catches base64 blobs and JWTs but does not
truncate plain YAML or JSON. A multi-MB body in your log stream is its
own problem even if it contains no secrets.

### 4. Logging at Info what should be Debug

```go
// BAD — fires N times per tick, every tick, forever.
for _, cluster := range clusters {
    log.Info("[reconcile] checking cluster", "cluster", cluster.Name)
}

// GOOD — Debug for the per-iteration trace; Info for the summary.
for _, cluster := range clusters {
    log.Debug("[reconcile] checking cluster", "cluster", cluster.Name)
}
log.Info("[reconcile] complete", "checked", len(clusters), "changed", changed)
```

If a log line fires more than ~once per request or per minute of
background work, it's almost certainly Debug.

### 5. Logging at Warn what should be Error

```go
// BAD — secret write to remote cluster failed; user thinks it worked.
log.Warn("secret push failed, continuing", "cluster", name, "error", err)
// The cluster is now missing a credential. That's not "degraded but
// recovered" — that's silent data loss.

// GOOD — surface the failure at the level that matches the impact.
log.Error("secret push failed", "cluster", name, "error", err)
return fmt.Errorf("push secret %s to %s: %w", secretName, name, err)
```

If the user-visible state ends up wrong, it's an Error. Warn is for
"we recovered; here's a heads-up."

### 6. Logging credentials by attribute name

```go
// BAD — even though the redaction wrapper will catch it, the call site
// is still wrong. A future refactor or a new sink could leak it.
log.Info("token issued", "token", rawToken)

// GOOD — log a fingerprint, not the value.
log.Info("token issued", "fingerprint", tokenFingerprint(rawToken))
```

Treat every credential-shaped attribute name as a code-review smell.
The wrapper saves you; fix the call site anyway.

---

## See also

- `internal/logging/request_id.go` — correlation-ID helpers
- `internal/logging/redact.go` — RedactHandler implementation + detector heuristics
- [Logging audit punch list](logging-audit-punchlist.md) — open items
  from the V2-2.3 audit; deleted as items get fixed
- `cmd/sharko/serve.go` — slog wiring at process start
- `internal/api/router.go` — `requestIDMiddleware` and the access-log
  middleware
