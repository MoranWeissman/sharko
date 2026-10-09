/**
 * gitHostLabel — the user-facing name of the Git host behind a connection,
 * from the connection's `git_provider` value. Returns null when the host is
 * unknown (no connection loaded yet, or a provider Sharko does not name), so
 * callers fall back to neutral wording instead of guessing "GitHub".
 *
 * v4.0.4 U2: "View PR #N on GitHub" and "Set up your personal GitHub PAT"
 * showed on a Gitea connection. Every piece of text that names the Git host
 * goes through here.
 */
export function gitHostLabel(provider: string | null | undefined): string | null {
  switch ((provider ?? '').toLowerCase()) {
    case 'github':
      return 'GitHub'
    case 'gitea':
      return 'Gitea'
    case 'azuredevops':
      return 'Azure DevOps'
    default:
      return null
  }
}

/**
 * The words that name the personal token, by Git host. v4.0.4 U2: the
 * attribution banner said "GitHub PAT" on a Gitea connection. Neutral
 * ("personal Git token") when the host is unknown.
 */
export function personalTokenName(hostLabel: string | null): string {
  return hostLabel ? `personal ${hostLabel} token` : 'personal Git token'
}
