---
story_key: V123-1-1-resolve-open-question-7-1-wire-sharko-catalog-urls-env
epic: V123-1 (Third-party private catalogs)
status: done
effort: S
dispatched: 2026-04-21
merged: 2026-04-21 (PR #267 → main @ bf6186a)
---

# Story V123-1.1 — Resolve open question §7.1 + wire `SHARKO_CATALOG_URLS` env

## Brief (from epics-v1.23.md)

As a **Sharko operator**, I want to configure third-party catalog URLs via an environment variable, so that I can extend the catalog without forking Sharko.

**Acceptance criteria:** env parse (multiple URLs), refresh interval override, empty env = embedded only, HTTPS-only enforcement.

**Dependencies:** none. First story of v1.23 execution.

## Implementation notes

- New package `internal/config/catalog_sources.go` (311 lines) exposes `CatalogSource` + `CatalogSourcesConfig` + `LoadCatalogSourcesFromEnv()`.
- HTTPS-only enforcement rejects `http://`, `file://`, malformed URLs at startup.
- SSRF guard — rejects URLs resolving to RFC1918 / loopback / link-local / IPv6 unique-local ranges.
- Hostname-based SSRF check is in scope (not deferred as originally considered) via a package-level `lookupHostFn` var the tests override. DNS failure at startup is **fail-open** — runtime fetcher re-resolves each poll.
- `SHARKO_CATALOG_URLS_ALLOW_PRIVATE=true` escape hatch for home-lab / dev; logs a WARN and continues.
- Refresh interval bounds: 1m min, 24h max, 1h default.
- Deduplication: case-insensitive host, `:443` stripped, trailing `/` stripped (for non-root paths), query+fragment dropped.
- Server bootstrap wiring at `cmd/sharko/serve.go`; stashed on `Server.catalogSources` via `SetCatalogSources` / `CatalogSources` accessors on the router.
- Startup log emits `count` + `refresh_interval` + `allow_private` only — **never log URLs** (may contain auth tokens in path).

## Decisions

- **Open question §7.1 resolved: env-only**, recorded in the design doc §7.1. Reason: stateless principle (NFR-V1.21 / §2.7) forbids ConfigMap persistence for catalog config.
- **`AllowPrivate` exposed on config struct** (rather than hidden in local var) so V123-1.5's `GET /api/v1/catalog/sources` response can surface an "unsafe mode" indicator if needed.
- SSRF is a v1.23 concern (not deferred to a later story) because the catalog fetcher has privileged network access from inside the cluster; aiming it at cloud-metadata (`169.254.169.254`) or internal ArgoCD endpoints is a real attack surface.

## Commits (on main)

- `68d7213` feat(catalog): SHARKO_CATALOG_URLS env parser + SSRF guard (V123-1.1)
- `e41f9c6` docs(design): resolve v1.23 open question §7.1 (env-only) + note SSRF guard
- `c4d6660` docs(operator): document third-party catalog source configuration
- `f42e9fc` style(catalog): use strings.SplitSeq iterator for env parse (Go 1.24 lint)

## PR

- #267 — merged 2026-04-21 via merge commit `bf6186a`

## Files touched

- `internal/config/catalog_sources.go` (+311 new)
- `internal/config/catalog_sources_test.go` (+355 new)
- `cmd/sharko/serve.go` (+28)
- `internal/api/router.go` (+5)
- `internal/api/catalog.go` (+16)
- `docs/design/2026-04-20-v1.23-catalog-extensibility.md` (§7.1, §2.1)
- `docs/site/operator/catalog-sources.md` (+125 new)
- `mkdocs.yml` (+1)

## Tests

- `./internal/config/...` grew from 23 → 41 test funcs (18 new, plus sub-tests totalling 40+ PASSes).
- Canonical tests for acceptance criteria: `TestLoadCatalogSourcesFromEnv_MultipleHTTPS`, `_RefreshIntervalOverride`, `_Empty`, `_RejectsHTTP`, `_RejectsFileScheme`.
- Hostname SSRF: `_HostnameResolvesToPrivate` (block), `_HostnameResolvesToPublic` (allow), `_DNSFailureIsFailOpen` (documents the intentional fail-open).

## Gotchas for V123-1.2 (next story)

1. **Never log catalog URLs** in the fetcher — URL paths may encode auth tokens.
2. **`CatalogSource.URL` is already canonical** — use it verbatim as the request URL without re-parsing.
3. **`AllowPrivate` on config is informational** — the guard already ran at startup. Fetcher doesn't need to re-check.
4. **Design §2.7 stateless** — V123-1.2's "last-successful snapshot" must stay in memory; no on-disk cache.
5. DNS at startup is fail-open; runtime fetcher re-resolves each poll — so if a private IP slips past startup validation (public hostname resolves to private IP only at runtime), the fetch itself needs a defense. Worth a runtime check in V123-1.2.
