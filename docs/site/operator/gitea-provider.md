# Reference — Gitea Provider

Sharko can keep its addons repository on a [Gitea](https://about.gitea.com/)
server that you run yourself, as well as on GitHub or Azure DevOps. This page
covers how to point Sharko at Gitea, what it needs, and what works a little
differently there.

## Choosing Gitea

Set the Git provider to `"gitea"` when you create or edit the connection.
Gitea can run at any address, so Sharko cannot tell from the address alone
that it is Gitea, the way it can for GitHub or Azure DevOps. You have to say
so.

**In the UI:** Settings → Connection → Git → **Provider: Gitea**.

**In Helm values:** set `connection.git.provider: "gitea"` together with
`connection.git.repoURL`. See
[Git-Native Server Configuration](git-native-config.md). You still enter the
Gitea token in the Settings UI.

**Through the API:**

```bash
curl -X POST https://sharko.example.com/api/v1/connections/ \
  -H "Authorization: Bearer $SHARKO_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "name": "gitea-main",
    "set_as_default": true,
    "git": {
      "provider": "gitea",
      "repo_url": "https://gitea.example.org/example-org/sharko-addons.git",
      "token": "<gitea access token>"
    },
    "argocd": {
      "server_url": "https://argocd-server.argocd.svc.cluster.local",
      "namespace": "argocd",
      "token": "<argocd account token>"
    }
  }'
```

## What you need to fill in

| Field | Where | Notes |
|-------|-------|-------|
| Provider | `git.provider` | Must be exactly `"gitea"`. Sharko never guesses it from the address |
| Repository address | `git.repo_url` | The full address of the repository. Sharko works out the Gitea server address from it (the scheme and host, for example `https://gitea.example.org`). **Always fill this in**, even if you also fill in `owner` and `repo` (see [below](#things-that-work-differently-on-gitea)) |
| Owner | `git.owner` (or read from `repo_url`) | The Gitea organization or user that owns the repository |
| Repo | `git.repo` (or read from `repo_url`) | The repository name |
| Token | `git.token` | A Gitea [access token](https://docs.gitea.com/development/api-usage#authentication) that can read and write the repository. The field has the same name as for GitHub (Azure DevOps calls it `pat`) |

You can leave out `owner` and `repo` when `repo_url` is a full
`https://host/owner/repo` address (with or without `.git` at the end). Sharko
reads them from the address, the same way it does for GitHub. Fill them in
yourself if your Gitea uses an address that cannot be split that way, for
example a server that sits under an extra path.

When you run Sharko on your own machine, you can set `SHARKO_DEV_MODE=true`
and a `GITEA_TOKEN` environment variable. Sharko then uses that token
without you saving one in a connection. GitHub (`GITHUB_TOKEN`) and Azure
DevOps (`AZURE_DEVOPS_PAT`) work the same way. Do not do this on a shared
Sharko.

Sharko's local playground uses Gitea. See
[Local Playground](../developer-guide/playground.md).

## Who is shown as the author of commits

Sharko writes commits on Gitea the same way it does on GitHub. See
[Git Attribution](../user-guide/attribution.md) for the whole picture.
Every commit Sharko makes names an author and a committer, and its message
ends with a `Signed-off-by:` line. When the person who asked for the change
is not the author, there is also a `Co-authored-by:` line.

- **When you have saved a personal Gitea token** in Settings → My Account,
  Sharko uses it for changes to settings and configuration. The commit
  then shows you as the author and committer.
- **When you have not**, the commit shows Sharko's own account as author and
  committer: `Sharko Bot <sharko-bot@users.noreply.github.com>`. Every Git
  provider uses that same address. The `.github.com` part does not mean the
  commit went to GitHub. For changes to settings and configuration, the API
  answer also carries `attribution_warning: "no_per_user_pat"`, and you show
  up only in the `Co-authored-by:` line.

## Things that work differently on Gitea

- **Always fill in `repo_url`.** Sharko saves a connection that has only
  `owner` and `repo`. But it builds the Gitea server address from
  `repo_url` alone, so the first time it tries to write to Gitea, it fails
  with "repo_url is empty".
- **One commit per file.** GitHub and Azure DevOps write several files as a
  single commit. Gitea gets one commit for each file. A change that touches
  several files, like registering a cluster or adding an addon to the
  catalog, still opens just one pull request. That pull request just has
  more than one commit in it.
- **Merging can take a few seconds.** After a pull request is opened, Gitea
  takes a moment to work out whether it can be merged. Sharko asks up to 10
  times, about 2 seconds apart, before it merges. So auto-merge on Gitea
  can take a few seconds longer than on GitHub. That is normal. The pull
  request is not stuck.

## Related pages

- [Git Attribution](../user-guide/attribution.md) — who is shown as the
  author of each commit, in full.
- [Git-Native Server Configuration](git-native-config.md) — setting the
  connection's non-secret fields, Gitea included, in Helm values.
- [Local Playground](../developer-guide/playground.md) — the local setup
  that runs Gitea.
