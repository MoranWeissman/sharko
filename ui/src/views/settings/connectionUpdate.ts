// Shared PUT /connections/{name} payload builder for settings pages that
// save ONE section of a connection (GitOps settings, secrets provider) and
// never intend to touch the git or ArgoCD connection details themselves.
//
// Why this exists (walk finding, gitea gitops-save bug): GitOpsSection and
// SecretsProviderSection each used to have their own local
// `buildConnectionPayload` that RECONSTRUCTED the git block from
// `git_repo_identifier` (owner/repo string) — building a repo_url for
// github and azuredevops (fixed, well-known hosts) but leaving `git: {
// repo_url: "" }` with no provider at all for gitea, which is self-hosted
// with an arbitrary host GET /connections never exposes. The server's
// required-field gate rejected that with "git.provider is required",
// so saving GitOps settings failed outright on every Gitea connection.
//
// The reconstruction was also lossy in a way that didn't error but WOULD
// have silently corrupted data: the argocd block was rebuilt with
// `insecure: true` hard-coded, regardless of what was actually stored —
// a connection verified over TLS would have flipped to insecure on the
// next unrelated settings save.
//
// The fix is architectural, not cosmetic: don't reconstruct what the
// client doesn't reliably know. Send the connection's name, echo back the
// one git fact the client DOES know exactly (the provider, from the
// masked GET /connections response), and leave repo_url/owner/repo/argocd
// out of the payload entirely. The server's PUT handler
// (internal/api/connections.go handleUpdateConnection) treats a git block
// with no identifying fields as "not touching git" and falls back to the
// full stored config — the same partial-update behavior it already gave
// gitops/provider/addon_secret_provider. Extra keys for the section
// actually being saved (gitops, provider, addon_secret_provider) are
// merged in via `extra`.
export interface ConnectionForUpdate {
  name: string
  git_provider: string
}

export function buildConnectionUpdatePayload(
  conn: ConnectionForUpdate,
  extra: Record<string, unknown> = {}
): Record<string, unknown> {
  return {
    name: conn.name,
    // Echoed verbatim from the stored connection — never rebuilt from
    // parts. repo_url / owner / repo / organization / project / repository
    // are intentionally omitted: the server backfills them from the saved
    // connection when a request carries none of them.
    git: { provider: conn.git_provider },
    ...extra,
  }
}
