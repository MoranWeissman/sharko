# AI notes were not added because the chart values look like they hold a secret

Sharko can ask your AI provider to add short notes to an addon's values file. Before it sends anything, it checks the chart's values for things that look like passwords, keys or tokens. If it finds one, it sends nothing to the AI. This page explains what to do next.

**How urgent:** Low. This is Sharko protecting you, not a fault. The addon still works. Only the AI notes in its values file are missing.

Before you start, set up the shell variables from [First checks](troubleshooting.md#set-up-once).

## What you see

- When you click **AI annotate** on an addon's values page, a message says:

  `AI annotate blocked: secret-like content detected (<pattern names>). No PR opened.`

- The API call `POST /api/v1/addons/{name}/values/annotate` answers with HTTP 422 and a body like this:

  ```json
  {
    "code": "secret_detected_blocked",
    "message": "Secret-like content detected in upstream values; AI annotation is hard-blocked.",
    "matches": [
      {"pattern": "API key / token / password assignment", "field": "password: ***", "line": 14},
      {"pattern": "PEM private key", "field": "(a secret-like value on this line; no plain field name to show — open the file at this line number)", "line": 52}
    ]
  }
  ```

  Each match gives the kind of thing found (`pattern`), the field name with the value hidden (`field`), and the line number in the chart's `values.yaml` (`line`). The value itself is never shown, logged or sent anywhere.

- When you add an addon, or refresh its values from the chart, the addon is still added or refreshed. Its values file just has no AI notes.
- The logs have these warnings:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'hard-blocked'
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep 'secret_leak_blocked'
  ```

  The first shows `ai annotate hard-blocked: secret-leak pattern matched` with the `chart`, `version` and `match_count`. The second shows the `addon`, `chart`, `version`, `source` (which action it came from: `ai_annotate`, `addon_add`, `catalog_add` or `values_refresh`) and the pattern names.

- The audit log has an entry with the event `secret_leak_blocked`:

  ```sh
  curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/audit?limit=200" \
    | jq '.entries[] | select(.event=="secret_leak_blocked") | {timestamp, resource, detail}'
  ```

- The metric `sharko_ai_annotate_total{outcome="secret_blocked"}` goes up. Sharko does not ship an alert for it.

If you get HTTP 503 with `AI is not configured` instead, see [AI features don't work](ai-provider-misconfigured.md).

## Why it happens

Many Helm charts ship a `values.yaml` with example passwords, keys or tokens in it. Sharko can't tell an example from a real secret, so it treats every one as real. If any line matches, Sharko sends nothing to the AI. There is no setting to turn this check off, and no way to skip it for one request.

What Sharko looks for:

| Pattern name | What it matches |
|---|---|
| `AWS access key` | A string that starts with `AKIA` or `ASIA` followed by 16 capital letters or digits |
| `GitHub fine-grained PAT` | A token that starts with `github_pat_` |
| `GitHub classic PAT` | A token that starts with `ghp_`, `gho_`, `ghu_` or `ghs_` |
| `JWT token` | A long three-part token that starts with `eyJ` |
| `PEM private key` | A line like `-----BEGIN ... PRIVATE KEY-----` |
| `Slack token` | A token that starts with `xoxb-`, `xoxa-`, `xoxp-`, `xoxr-` or `xoxs-` |
| `Google API key` | A key that starts with `AIza` |
| `API key / token / password assignment` | A line such as `password: ...`, `api_key: ...`, `secret: ...` or `token: ...` with a value of 16 or more letters and digits |

Comment lines are checked too, because a real key in a comment is just as bad. Short placeholders such as `password: changeme` are under 16 characters and do not match.

## How to fix it

### 1. Decide if you need the AI notes at all

The addon works without them. If you don't need the notes, you are done. Nothing else is waiting on this.

### 2. Look at the lines Sharko found

Download the chart's values file to your own machine. Use the chart repo, name and version of the addon:

```sh
helm show values <chart> --repo https://charts.example.com --version <version> > upstream-values.yaml
```

For a chart in an OCI registry:

```sh
helm show values oci://registry.example.com/charts/<chart> --version <version> > upstream-values.yaml
```

Then open the file at the line numbers from the 422 answer, or print those lines:

```sh
sed -n '14p;52p' upstream-values.yaml
```

### 3. Decide what kind of value it is

- **A placeholder or an example** (for example a long fake key shown in a comment). This is the usual case. You can live without AI notes for this addon, or write the notes by hand in the values editor. To stop Sharko from trying again for this addon, see step 4.
- **What looks like a real key.** This is rare, but it happens. Do not use that chart version. Tell the chart's maintainers, and pick an earlier version that does not have it.

### 4. Stop AI notes for this addon

If you don't want Sharko to try AI notes for this addon again, turn them off for this one addon. You can do it from the addon's **Catalog** tab, or with the API:

```sh
curl -s -X PUT -H "Authorization: Bearer $SHARKO_TOKEN" -H "Content-Type: application/json" \
  "$SHARKO_URL/api/v1/addons/<addon>/values/ai-opt-out" \
  -d '{"opt_out": true}' | jq
```

This opens a pull request (or merges it right away, if auto-merge is on) that adds one line to the top of the addon's values file. After it is merged, adding or refreshing the addon skips the AI step. A manual **AI annotate** then answers with HTTP 409 and says the addon is opted out. To turn AI notes back on, send `{"opt_out": false}`.

## How to check it's fixed

There are two good end states.

**You chose to live without AI notes, or opted out:**

- The addon shows as healthy, and the values file is there without notes.
- If you opted out, the values page says: `This addon is opted out of AI annotation.`

**You moved to a chart version without secret-looking values:**

- **AI annotate** works and opens a pull request:

  ```sh
  curl -s -X POST -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/addons/<addon>/values/annotate" | jq '{pr_url, merged, ai_skip_reason}'
  ```

  You get a `pr_url`, and no `ai_skip_reason`.

- No new `secret_leak_blocked` lines in the logs for that addon:

  ```sh
  kubectl -n "$SHARKO_NS" logs deploy/sharko --since=10m | grep 'secret_leak_blocked'
  ```

## If that didn't work

If you think Sharko is flagging something that is clearly not a secret, open an issue at <https://github.com/MoranWeissman/sharko/issues>. Include:

- the Sharko version (`sharko version`, or the `version` field from `/api/v1/health`)
- the log lines from the last hour that mention `secret_leak_blocked`
- the chart name and version, and the `pattern` and `line` from the 422 answer
- what you tried from this page

Never paste the value from the chart line into the issue, even if you think it is fake.

Related pages:

- [AI features don't work](ai-provider-misconfigured.md)
- [AI configuration](ai-config.md)
- [First checks](troubleshooting.md)
