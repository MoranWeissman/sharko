# Sharko — Post v1.0.0 TODO

> Features, improvements, and technical debt identified during v1.0.0 development.
> Organized by priority. Check off as completed.

---

## High Priority (before public release)

### GitOps Integration
- [ ] **Webhook listener** — GitHub/GitLab webhook endpoint so Sharko gets notified when someone pushes to the addons repo directly
- [ ] **Conflict detection** — detect and handle merge conflicts when Sharko creates a PR while someone else edited the same file
- [ ] **Schema validation CI** — GitHub Action for the addons repo that validates YAML schema on every push/PR (or `sharko validate` CLI command)
- [ ] **Audit log for manual changes** — track who changed what in the repo outside of Sharko

### Security Hardening
- [ ] **Content-Security-Policy header** — add CSP header for defense-in-depth
- [ ] **Strict-Transport-Security header** — add HSTS header
- [ ] **Document trusted proxy requirement** — X-Forwarded-For is trusted for rate limiting, server must sit behind a proxy
- [ ] **Document auth-disabled risk** — when no users are configured, all endpoints are public

### Notifications System
- [ ] **Wire VersionProvider to real Helm repo checks** — currently the checker runs but LatestVersion is empty (no Helm index fetch)
- [ ] **Security advisory detection** — parse release notes for CVE mentions
- [ ] **Notification persistence** — currently in-memory, lost on restart. Move to K8s ConfigMap or file store

### Documentation
- [ ] **ReadTheDocs website** — MkDocs + Material theme, hosted docs site (ArgoCD-style)
- [ ] **Operator manual** — installation, configuration, upgrading, troubleshooting
- [ ] **User guide restructure** — split into per-topic pages (managing clusters, managing addons, etc.)

---

## Medium Priority (v1.1.0)

### Product Features
- [ ] **AI-parsed release notes** — use configured AI provider to parse Helm chart changelogs for upgrade comparison
- [ ] **arm64 Docker image** — re-enable multi-platform build when needed for Graviton clusters
- [ ] **CLI binary distribution** — goreleaser for macOS/Linux/Windows binaries
- [ ] **Dark mode refinements** — dark mode exists but hasn't been tested with the sky-blue palette
- [ ] **Addon dependency ordering** — declare that addon B depends on addon A (beyond sync waves)

### Developer Experience
- [ ] **E2E tests** — test against real ArgoCD (Kind + ArgoCD in CI)
- [ ] **Helm chart validation** — test `charts/sharko/` renders correctly with `helm template`
- [ ] **Code splitting** — UI JS bundle is ~1MB, split with dynamic imports
- [ ] **Storybook** — component library for UI development

### API
- [ ] **Pagination** — large cluster/addon lists need pagination
- [ ] **Filtering/sorting** — query params for list endpoints
- [ ] **Rate limiting on all write endpoints** — currently only login is rate-limited

---

## Low Priority (v1.x / v2.0)

- [ ] **Multi-tenancy** — multiple connections/environments
- [ ] **Kubernetes operator (CRDs)** — manage addons via K8s custom resources
- [ ] **Webhook events** — emit events on addon changes for external consumers
- [ ] **Backup/restore** — export/import Sharko configuration
- [ ] **Upgrade path documentation** — how to upgrade from one Sharko version to another
- [ ] **Azure DevOps Git provider testing** — currently only GitHub is well-tested
- [ ] **Plugin system** — extend Sharko with custom addon logic
- [ ] **Metrics endpoint** — Prometheus `/metrics` for monitoring Sharko itself

---

## Technical Debt

- [ ] **`name` vs `appName` reconciliation** — individual addon files use `appName` but some code paths still reference `name`
- [ ] **Catalog file duplication** — addons exist both in `charts/<name>/addon.yaml` AND `configuration/addons-catalog.yaml`. Need to decide on single source of truth
- [ ] **Test coverage** — Go coverage is ~40%, UI coverage not measured. Target 70%+
- [ ] **Login page mascot gap** — PNG has transparent padding, needs manual cropping for pixel-perfect alignment
