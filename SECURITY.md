# Security Policy

The Sharko maintainers take security seriously. This document describes
how to report a security vulnerability and what to expect after you do.

## Reporting a Vulnerability

**Please do not file public GitHub issues for security vulnerabilities.**
Public reports give attackers a head start on exploitation before a fix
is available.

### Preferred channel

Email **moran.weissman@gmail.com** with the subject line `[Sharko
Security]` followed by a brief summary. Include:

- A clear description of the vulnerability
- Steps to reproduce (or a proof-of-concept)
- The affected Sharko version(s) and deployment surface (server,
  CLI, UI, Helm chart)
- Any known mitigations
- Whether you intend to disclose publicly, and on what timeline

If you would prefer to encrypt the report, ask in your initial email
and a PGP key will be provided. The project does not currently publish
a PGP key by default; one is available on request.

> **Note:** You can also report privately through GitHub's built-in
> vulnerability reporting (Security tab → "Report a vulnerability" on
> the repository, which opens a private Security Advisory). Both
> channels reach the lead maintainer directly. See
> [GOVERNANCE.md](GOVERNANCE.md) for the governance transition plan.

### What to expect

- **Acknowledgement:** within **5 business days** of your report
  (best-effort; the project is pre-release with a single lead
  maintainer — see [MAINTAINERS.md](MAINTAINERS.md)).
- **Triage and severity assessment:** within **10 business days** of
  acknowledgement.
- **Fix timeline:** depends on severity. Critical issues are
  prioritized above feature work. Lower-severity issues are scheduled
  into the next appropriate release.
- **Coordinated disclosure:** we will agree a disclosure date with you
  before announcing the fix publicly. We aim for a window that gives
  operators time to upgrade but does not delay disclosure
  unreasonably.
- **Credit:** unless you request anonymity, we will credit you in the
  release notes and the security advisory.

### Scope

Vulnerabilities of interest include (non-exhaustive):

- Authentication, authorization, and session-handling flaws in the
  Sharko server (`internal/auth*`, `internal/authz`, API middleware)
- Credential leakage in API responses, logs, audit trail, or error
  messages (cluster kubeconfigs, addon secrets, Git PATs, API keys,
  ArgoCD account tokens)
- Bypass of the ownership-label gate
  (`app.kubernetes.io/managed-by: sharko`) that allows Sharko to
  modify or delete a Secret it does not own (see
  `.claude/team/security-auditor.md` §11)
- Bypass of the schema-envelope read-time validator allowing malformed
  or untrusted input to traverse the trust boundary (see
  `.claude/team/security-auditor.md` §12)
- Catalog-signing trust-policy bypass: unanchored trust-regex
  exploitation, legacy-Bundle-format acceptance, or `workflow_run`
  SAN spoofing (see `.claude/team/security-auditor.md` "Catalog
  signing surface")
- AVP / Redis bridging of secret material that violates the no-AVP /
  no-Redis-leak design constraint (see
  `.claude/team/security-auditor.md` §13)
- Container escape, privilege escalation, or Kubernetes RBAC abuse
  via the Sharko pod
- Injection vulnerabilities in any Sharko surface (CLI, API, UI)

Out of scope:

- Vulnerabilities in upstream dependencies that have already been
  publicly disclosed and have an available upgrade — please open a
  regular issue or PR to bump the dependency.
- Theoretical issues without a demonstrable impact.
- Issues that require physical access to the developer's laptop or
  the Kubernetes control plane.
- Denial-of-service via legitimate but expensive API calls (rate
  limiting and quota enforcement are tracked separately; report
  via a normal issue).
- Findings against test fixtures, demo mode (`make demo`), or
  intentionally insecure development defaults — these are documented
  as not production-grade.

## Supported Versions

Sharko is currently **pre-release** (`v1.x`). The first production
release will be `v2.0.0` (see
[`.claude/team/product-manager.md`](.claude/team/product-manager.md)).

| Version    | Supported for security fixes |
| ---------- | ---------------------------- |
| `v2.x`     | Not yet released             |
| `v1.x` (current pre-release line) | Latest minor only — please upgrade to the most recent `v1.x` minor before reporting |
| `v0.x` and earlier | No |

Once `v2.0.0` ships, this table will be updated to follow the standard
CNCF support window: latest minor and the immediately preceding minor.

## Security-Sensitive Code Paths

Contributors should review
[`.claude/team/security-auditor.md`](.claude/team/security-auditor.md)
before modifying any of the following:

- `internal/api/` — request handling, auth middleware, write-endpoint
  admin checks
- `internal/auth*` and `internal/authz/` — sessions, API keys, RBAC
- `internal/argosecrets/` and `internal/clusterreconciler/` —
  cluster-secret payloads and the ownership-label gate
- `internal/catalog/` and `internal/catalog/signing/` — catalog
  signing, trust policy, Sigstore Bundle handling
- `internal/secrets/` — addon-secret reconciliation to remote clusters
- `internal/schema/` — envelope validation (the trust boundary for
  YAML inputs)

Pull requests touching these paths require security-auditor review.

## Hardening Guidance for Operators

Operator-side hardening guidance lives in the documentation:

- [Authentication and RBAC](docs/site/operator/) — admin password
  rotation, API-key lifecycle
- [Cluster secret reconciler runbook](docs/site/operator/cluster-reconciler.md)
  — ownership-label semantics, adopt/unadopt flow
- [YAML schema migration runbook](docs/site/operator/yaml-schema-migration.md)
  — envelope migration and validator behavior
- [Catalog scan runbook](docs/site/developer-guide/catalog-scan-runbook.md)
  — third-party scanner bot trust model

## Public Security Advisories

Once Sharko has been accepted into a foundation home, security
advisories will be published as GitHub Security Advisories on the
repository and announced through the project's standard release
channels. Until then, advisories are included in the relevant release
notes and pinned to the top of the changelog.
