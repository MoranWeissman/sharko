# Reference — Gitea Provider

Sharko can keep its GitOps repo on a self-hosted [Gitea](https://about.gitea.com/)
server, alongside GitHub and Azure DevOps. This page covers how to point
Sharko at one, what it needs, and where the rough edges are today.

## Choosing the Gitea provider type

Set `git.provider` to `"gitea"` when you create or edit the connection.
Gitea can run on any hostname you like, so — unlike GitHub or Azure DevOps —
Sharko cannot guess "this is Gitea" from the URL alone. You have to say so
explicitly.

**Via the UI:** Settings → Connection → Git → **Provider: Gitea**.

**Via the API:**

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

## Required fields

| Field | Where | Notes |
|-------|-------|-------|
| `provider` | `git.provider` | Must be the literal string `"gitea"` — never inferred from the URL. |
| Base URL | `git.repo_url` (scheme + host, e.g. `https://gitea.example.org`) | Sharko derives the Gitea API base URL from this — `/api/v1` is appended automatically by the Gitea SDK. **Always send `repo_url`**, even if you also send `owner`/`repo` explicitly (see [Honest limits](#honest-limits) below). |
| Owner | `git.owner` (or the second-to-last path segment of `repo_url`) | The Gitea organization or user that owns the repo. |
| Repo | `git.repo` (or the last path segment of `repo_url`) | The repository name. |
| Token | `git.token` | A Gitea [access token](https://docs.gitea.com/development/api-usage#authentication) with read/write on the repository — the same field name GitHub uses (Azure DevOps calls its field `pat` instead). |

`owner` and `repo` are optional **if** `repo_url` is a full
`https://host/owner/repo` (or `.git`) URL — Sharko parses them out of the
path the same way it does for GitHub. Give them explicitly if your Gitea
instance uses a URL shape that path-parsing can't split cleanly (a repo
behind a non-standard base path, for example).

For local development, `SHARKO_DEV_MODE=true` plus a `GITEA_TOKEN`
environment variable lets Sharko pick up a token without storing one in a
connection record — the same fallback GitHub (`GITHUB_TOKEN`) and Azure
DevOps (`AZURE_DEVOPS_PAT`) already have.

## The local playground uses Gitea by default

`make playground-up` provisions a real, in-cluster Gitea server as the
default Git backend (`PLAYGROUND_GIT_BACKEND=gitea`; set it to `gitfake` for
the lighter-weight alternative). It creates a `gitea`-typed Sharko
connection the same way the example above does — `repo_url` pointing at the
in-cluster Gitea Service, `provider: "gitea"`, and a token minted from
Gitea's own API — then drives the whole register/enable/merge loop through
Gitea's real REST API, including merging the pull requests Sharko opens.
See [Local Playground](../developer-guide/playground.md) for the full walk
and the Gitea admin login it prints (`http://localhost:13000`).

## Commit attribution on Gitea

Sharko's [tiered attribution model](../user-guide/attribution.md) works the
same way on Gitea as it does on GitHub: every write sets an explicit commit
author and committer, and the commit message carries a `Signed-off-by:`
trailer (plus a `Co-authored-by:` trailer when the acting user isn't the
commit author).

- **With a resolved per-user identity**, the commit's author and committer
  are that identity — mirroring exactly how the GitHub provider does it
  (`internal/gitprovider/github_write.go`'s `commitAuthorFor`, and Gitea's
  own `commitIdentityFor`, both read the same `CommitAttribution` off the
  request context and both call `EffectiveAuthor()` for the same
  (name, email) pair).
- **Without one**, both author and committer fall back to Sharko's service
  identity (`Sharko Bot <sharko-bot@users.noreply.github.com>` — the same
  constant every provider falls back to; the `.github.com` domain in that
  address is a historical accident, not a claim about which provider is in
  use), and the response carries `attribution_warning: "no_per_user_pat"`
  for Tier 2 (configuration) writes.

## Honest limits

- **No per-user Gitea token yet.** Settings → My Account's personal-token
  field (`internal/auth.Store.SetUserGitHubToken` /
  `GetUserGitHubToken`) is GitHub-only today — there is no equivalent
  "personal Gitea token" storage. On a Gitea connection, Tier 2 writes
  always take the fallback path: the service token authors the commit, and
  the acting user shows up only as the `Co-authored-by:` trailer. If you
  have configured a personal *GitHub* token on an account whose active
  connection is Gitea, don't expect it to be used here — the tiered
  resolver has no Gitea-specific lookup to reach for instead, so a Gitea
  connection behaves as if no personal token exists.
- **`repo_url` is the field that actually matters at write time**, even
  though connection validation accepts `owner` + `repo` alone. Sharko
  builds the Gitea API client's base URL exclusively from `repo_url`
  (`deriveGiteaBaseURL` in `internal/service/connection.go` and
  `deriveBaseURL` in `internal/api/tiered_git.go`); a connection saved with
  only `owner`/`repo` and no `repo_url` passes validation but fails the
  first time Sharko actually tries to talk to Gitea, with "repo_url is
  empty". Always send `repo_url`.
- **No native batch-commit API.** `BatchCreateFiles` falls back to one
  commit per file (sequential `CreateOrUpdateFile` calls) — GitHub and
  Azure DevOps write a whole batch as a single tree/commit. A multi-file
  Sharko write (registering a cluster, adding an addon to the catalog)
  still opens exactly one pull request on Gitea, it's just made of more
  than one commit on the branch before that PR is opened.
- **Merge readiness is polled, not instant.** Gitea computes whether a pull
  request is mergeable asynchronously after it's opened; `MergePullRequest`
  polls (up to 10 attempts, a couple of seconds apart) until Gitea reports
  the PR mergeable before attempting the merge. Auto-merge on Gitea can
  take a few seconds longer than on GitHub as a result — this is normal,
  not a stuck PR.

## Related pages

- [Git Attribution](../user-guide/attribution.md) — the tiered model in full.
- [Local Playground](../developer-guide/playground.md) — the Gitea-backed
  local dev loop.
- [Git-Native Server Configuration](git-native-config.md) — declaring the
  connection's non-secret fields in Helm values (currently documents
  `provider: "github" | "azuredevops"` only — Gitea connections still work,
  configure them via the Settings UI or the API until that reference page
  is extended).
