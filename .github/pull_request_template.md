## Summary
<!-- Brief description of changes -->

## Type
- [ ] Bug fix
- [ ] New feature
- [ ] Documentation
- [ ] Refactor
- [ ] CI/CD
- [ ] Governance / community

## Test Plan
<!-- How was this tested? -->

## Checklist
- [ ] Tests pass (`make test`)
- [ ] Build succeeds (`make build`)
- [ ] Swagger docs updated if API changed (`swag init -g cmd/sharko/serve.go -o docs/swagger`)
- [ ] No forbidden content (organization names, internal domains)
- [ ] **Commits signed off (DCO)** — every commit has a `Signed-off-by:` trailer matching the author. Use `git commit -s` to add automatically. See [CONTRIBUTING.md](../CONTRIBUTING.md#sign-off-your-commits-dco).
  - Note: `Signed-off-by:` is the DCO attestation and is required. `Co-Authored-By:` is a different trailer that this repo does **not** use — see [CLAUDE.md](../CLAUDE.md).
- [ ] AI assistance disclosed (if applicable) — note in this PR description, not in commit metadata.

<!-- Adopter prompt -->

> **Adopting Sharko?** If your organization is running Sharko (in production, staging, or as a POC), please consider adding yourself to [`ADOPTERS.md`](../ADOPTERS.md) in a separate PR. Public adoption signals matter for the project's CNCF Sandbox application.
