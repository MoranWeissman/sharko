# Git Attribution

Every change Sharko makes lands as a Git commit in your GitOps repo. Starting in **v1.20**, Sharko surfaces who triggered each change in three ways: the commit author, a `Co-authored-by:` trailer, and an attribution icon in the audit log.

## How it works

Sharko classifies every mutating action into one of two tiers:

- **Tier 1 — operational actions:** cluster register/remove, addon enable/disable on a cluster, addon upgrades, connection CRUD, AI config, dashboards, PR refresh/delete, reconcile triggers.
- **Tier 2 — configuration changes:** editing an addon's catalog metadata (sync wave, sync options, ignore differences, additional sources) or its Helm values.

| Tier | Token used | Commit author | Co-authored-by trailer |
|------|------------|---------------|------------------------|
| Tier 1 | Service token | `Sharko Bot` | The user |
| Tier 2 with personal PAT | Your personal PAT | You | (suppressed — you're already the author) |
| Tier 2 without personal PAT | Service token (fallback) | `Sharko Bot` | The user |

This means: operational actions always commit as the platform, with a trailer for your name. Configuration changes prefer to commit as you directly, falling back to a co-author trailer if you haven't set up a personal PAT.

## Setting up your personal GitHub PAT

1. Open **Settings → My Account**.
2. Generate a GitHub Personal Access Token with `repo` scope (or fine-grained equivalent: contents read/write, pull requests read/write) on the GitOps repository.
3. Paste it into the **Personal GitHub Token** field and click **Save token**.
4. Click **Test** to verify the token works against GitHub.

Your token is encrypted at rest using the same encryption key as Sharko's connection store. It is never returned to the UI after saving — only a `Token configured` indicator is shown.

To remove your token, click **Remove** in the same section. After removal, your Tier 2 actions will fall back to the service account with a co-author trailer.

## Reading the audit log

The audit log table includes an **Attr.** (attribution) icon column:

| Icon | `attribution_mode` | What it means |
|------|--------------------|---------------|
| Key (gray) | `service` | Service token used — no human identified (e.g. background webhooks) |
| Users (amber) | `co_author` | Service token used; you appear as a `Co-authored-by:` trailer |
| User (green) | `per_user` | Your personal PAT was used — the commit is authored by you in Git |

Hover any icon for a tooltip explanation.

## When the UX nudge appears

If you perform a Tier 2 action without a personal PAT configured, the response is augmented with `attribution_warning: "no_per_user_pat"`. The UI surfaces a yellow banner near the action explaining that the change was attributed to the service account, with a one-click link to **Settings → My Account** to fix it for next time. The action still succeeds — the nudge is informational.

## Why bother?

- **`git blame` works.** Tier 2 changes that landed using your personal PAT show your name in `git blame`, so post-incident debugging ("who set replicaCount to 100?") works the natural way.
- **PR review attribution.** With per-user PATs, the GitHub PR shows your avatar instead of `Sharko Bot`'s.
- **Compliance.** Every audit log entry now carries an attribution mode, so the report "who changed this addon catalog last quarter" doesn't rely solely on Sharko's audit log — Git history is a corroborating second source of truth.

For the full design rationale, see `docs/design/2026-04-16-attribution-and-permissions-model.md`.
