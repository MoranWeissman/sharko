# One change, three ways to make it

Sharko has three ways to make changes: the web UI, the command line tool, and the REST API. All three run the same steps: check the request against your catalog and cluster list, show you what would change in Git, and then open a pull request.

Here is one change — turning on `datadog` for the `dev-us` cluster — shown three ways.

## The web UI

The UI lets you pick addons and clusters with buttons and dropdown menus. You see the exact file changes before the pull request is opened, with secret values hidden.

The GIF below shows enabling `datadog` on `dev-us` from the cluster detail page. You click "Add addon", pick the addon from the list, preview the file changes in a dialog, then click "Confirm — open PR".

![Enabling an addon on a cluster from the UI](../assets/gifs/enable-on-cluster.gif)

## The command line

The `sharko` CLI wraps the same API the UI calls. You log in once, then run commands that talk to your Sharko server.

### Preview the change

To see what Sharko would do without opening a pull request:

```bash
sharko enable-addon dev-us datadog --dry-run
```

The CLI prints the preview:

```
Dry-run: previewing enabling datadog on dev-us...

Dry-run preview (no changes made):
  PR:      sharko: enable addon datadog on cluster dev-us
  Addons:  datadog
  Files:
    [update] cluster-addons/dev-us.yaml
      --- cluster-addons/dev-us.yaml
      +++ cluster-addons/dev-us.yaml
      @@ -6,6 +6,8 @@
           cert-manager:
               enabled: true
               version: 1.13.6
      +    datadog:
      +        enabled: true
           metrics-server:
               enabled: true
           vault:

    [create] values/clusters/dev-us/datadog.yaml
      --- values/clusters/dev-us/datadog.yaml
      +++ values/clusters/dev-us/datadog.yaml
      @@ -0,0 +1,2 @@
      +# Helm values for datadog on dev-us only. These override values/global/datadog.yaml.
      +# An empty file means no cluster-specific overrides.
```

The second file is where settings for datadog on this cluster only will go. It starts with just two comment lines.

### Open the pull request

To actually open the pull request, run the same command with `--yes` (without it, the server answers "confirmation required: set yes: true in request body"):

```bash
sharko enable-addon dev-us datadog --yes
```

The CLI prints the result, including the pull request URL:

```
Enabling datadog on dev-us... done

  PR: https://github.com/your-org/your-repo/pull/50 (open — merge manually)
```

## The REST API

The API is what the UI and CLI both use. Your own tools — a portal, Backstage, Terraform, or a pipeline — can call it too, with an API key.

### Preview the change

To see what Sharko would do without opening a pull request, send the request with `dry_run: true`:

```bash
curl -X POST https://sharko.example.com/api/v1/v4/clusters/dev-us/addons/datadog \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "dry_run": true
  }'
```

The response shows which files would be written and what the pull request title would be:

```json
{
  "merged": false,
  "dry_run": {
    "effective_addons": [
      "datadog"
    ],
    "files_to_write": [
      {
        "path": "cluster-addons/dev-us.yaml",
        "action": "update",
        "diff": "--- cluster-addons/dev-us.yaml\n+++ cluster-addons/dev-us.yaml\n@@ -6,6 +6,8 @@\n     cert-manager:\n         enabled: true\n         version: 1.13.6\n+    datadog:\n+        enabled: true\n     metrics-server:\n         enabled: true\n     vault:\n"
      },
      {
        "path": "values/clusters/dev-us/datadog.yaml",
        "action": "create",
        "diff": "--- values/clusters/dev-us/datadog.yaml\n+++ values/clusters/dev-us/datadog.yaml\n@@ -0,0 +1,2 @@\n+# Helm values for datadog on dev-us only. These override values/global/datadog.yaml.\n+# An empty file means no cluster-specific overrides.\n"
      }
    ],
    "pr_title": "sharko: enable addon datadog on cluster dev-us",
    "secrets_to_create": []
  }
}
```

### Open the pull request

To actually open the pull request, send the same request with `yes: true` instead of `dry_run`:

```bash
curl -X POST https://sharko.example.com/api/v1/v4/clusters/dev-us/addons/datadog \
  -H "Authorization: Bearer YOUR_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "yes": true
  }'
```

The response includes the pull request URL:

```json
{
  "pr_url": "https://github.com/your-org/your-repo/pull/51",
  "pr_id": 51,
  "branch": "sharko/enable-addon-datadog-on-cluster-dev-us-(v4)-f7522a80",
  "merged": false
}
```

## What these three don't do

None of the three doors deploys anything to a cluster. They all write files to Git and open a pull request. Merging that pull request is what lets ArgoCD deploy the addon.

The one thing Sharko does write straight to clusters, without Git: the ArgoCD connection Secret for each managed cluster, and the addon secrets (credentials that some addons need). Every change to your addons goes to Git first.
