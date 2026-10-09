/**
 * Where an addon's values file lives in the GitOps repo, and the link that
 * opens it on the Git host.
 *
 * v4.0.4 U9b: the "Edit in GitHub" links always pointed at the old (v3)
 * layout — configuration/addons-global-values/<addon>.yaml and
 * configuration/addons-clusters-values/<cluster>.yaml — so on a repo in the
 * new format they opened a file that does not exist. The new (v4) layout is
 * values/global/<addon>.yaml and values/clusters/<cluster>/<addon>.yaml
 * (internal/orchestrator/v4paths.go).
 *
 * When the repo layout is not known yet, no path is returned, so no link is
 * shown: a missing link is better than one that opens the wrong file.
 */
export type RepoFormat = 'v3' | 'v4'

export function toRepoFormat(format: string | null | undefined): RepoFormat | undefined {
  return format === 'v4' || format === 'v3' ? format : undefined
}

export function valuesFilePath(
  format: RepoFormat | undefined,
  addon: string,
  cluster?: string,
): string | undefined {
  if (!format || !addon) return undefined
  if (format === 'v4') {
    return cluster ? `values/clusters/${cluster}/${addon}.yaml` : `values/global/${addon}.yaml`
  }
  return cluster
    ? `configuration/addons-clusters-values/${cluster}.yaml`
    : `configuration/addons-global-values/${addon}.yaml`
}

/**
 * The browser link to a values file. Only GitHub links are built: the
 * connection does not carry the web address of a Gitea or Azure DevOps
 * server, so Sharko cannot build a working link for them. `repoBase` is
 * "https://github.com/<owner>/<repo>" or empty.
 */
export function valuesFileURL(
  repoBase: string | undefined,
  branch: string,
  format: RepoFormat | undefined,
  addon: string,
  cluster?: string,
): string | undefined {
  const path = valuesFilePath(format, addon, cluster)
  if (!repoBase || !path) return undefined
  return `${repoBase}/blob/${branch || 'main'}/${path}`
}

/** The link's words, naming the Git host (as in U2). */
export function editOnHostLabel(hostLabel: string | null): string {
  return hostLabel ? `Edit in ${hostLabel}` : 'Open the file in your Git host'
}
