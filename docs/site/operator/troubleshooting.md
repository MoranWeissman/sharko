# First checks when something goes wrong

Start here when Sharko is not doing what you expect. This page shows the
checks to run first. Then use the
[list of problems and fixes](failure-mode-index.md) to find the page for
your exact error.

## Set up once

Every fix page uses the same few shell variables. Set them once on your
own machine (your laptop or a jump host). You need `kubectl` access to
the namespace Sharko runs in, plus `curl` and `jq`.

Never run commands inside the Sharko pod. The Sharko image has no shell
tools in it (no `curl`, no `kubectl`, no `aws`), so every command on
these pages runs from your own machine.

```sh
# The namespace Sharko runs in.
export SHARKO_NS=sharko

# If you have no Ingress, open a second terminal and leave this running.
# The Service is called "sharko" when the Helm release is "sharko";
# otherwise it is "<release>-sharko".
kubectl -n "$SHARKO_NS" port-forward svc/sharko 8080:80

# The address you reach Sharko on (your Ingress address, or the port-forward).
export SHARKO_URL=http://localhost:8080

# An admin API token. Sign in first, then make a token.
sharko login --server "$SHARKO_URL"
sharko token create --name oncall --role admin
export SHARKO_TOKEN=<the token it printed>
```

The token is shown only once. It expires after 90 days unless you pass
`--expires-in-days`. When you are done, remove it with
`sharko token revoke oncall`.

API calls on the fix pages then look like this:

```sh
curl -s -H "Authorization: Bearer $SHARKO_TOKEN" "$SHARKO_URL/api/v1/health" | jq
```

## Is the pod running?

```sh
kubectl -n "$SHARKO_NS" get pods -l app.kubernetes.io/name=sharko
kubectl -n "$SHARKO_NS" describe pod -l app.kubernetes.io/name=sharko
kubectl -n "$SHARKO_NS" get events --sort-by=.lastTimestamp
```

- `Running` with `1/1` ready is good.
- A `RESTARTS` number that keeps going up, `CrashLoopBackOff`, or
  `OOMKilled` in the `describe` output: see
  [The Sharko pod keeps restarting](oom-restart-loop.md).
- `Pending`: Kubernetes cannot place the pod. The `describe` output says
  why (not enough memory on any node, a volume that can't be attached,
  and so on).

## Is Sharko answering?

```sh
curl -s "$SHARKO_URL/api/v1/health" | jq
```

A good answer has `"status": "healthy"` and the Sharko `version`. This
check only says the Sharko process is up. It does not test ArgoCD or
your Git host.

If you get "connection refused", the port-forward or the Ingress is not
working. Check that the port-forward terminal is still running, or see
[Installation](installation.md) for the Ingress setup.

## Read the logs

```sh
# Follow the live logs.
kubectl -n "$SHARKO_NS" logs -f deploy/sharko

# The logs from before the last restart.
kubectl -n "$SHARKO_NS" logs deploy/sharko --previous

# Look for warnings and errors in the last hour.
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h | grep -E '"level":"(WARN|ERROR)"'
```

Each log line is one JSON object with `time`, `level` and `msg`, plus
more fields. A log line written while Sharko handles a request has a
`request_id` field (it looks like `req-<letters and numbers>`). To see
everything that happened during one request, filter on it:

```sh
kubectl -n "$SHARKO_NS" logs deploy/sharko --since=1h \
  | jq -R 'fromjson? | select(.request_id == "req-<id>")'
```

## Find your problem

Search the [list of problems and fixes](failure-mode-index.md) for the
error message, the alert name or the HTTP status you see. Each row links
to the page that fixes it.

Some common ones:

| What you see | Where to go |
|---|---|
| "401 Unauthorized" on the API or the CLI | Your session or token ran out. Sign in again with `sharko login`. If a wrong password still lets someone in, see [Someone can sign in without the right password](auth-bypass.md). |
| 502 or 503 from anything that talks to ArgoCD | [Sharko can't reach ArgoCD](argocd-upstream-unreachable.md) |
| 502 or 503 from anything that opens a pull request | [Sharko can't reach your Git host](git-provider-unreachable.md) |
| `sharko init` seems stuck, or stopped half way | [Sharko init is stuck or stopped](init-operation-deadlocked.md) |
| `sharko init` says the repository was not found | Create the repository on your Git host and push at least one commit, then run `sharko init` again. |
| The pod restarts again and again | [The Sharko pod keeps restarting](oom-restart-loop.md) |
| An addon's ArgoCD Application stays `Degraded` | [An addon stays Degraded in ArgoCD](addon-application-stuck-degraded.md) |
| Every catalog entry shows as Unverified | [Sharko can't load the signing trust root](catalog-trust-root-unavailable.md), then [Catalog trust policy](catalog-trust-policy.md) |
| A third-party catalog source does not load | [A catalog source can't be downloaded](catalog-source-http-fetch-failed.md) or [A catalog source has the wrong format](catalog-source-schema-validation-failed.md) |
| "Test" on a cluster returns 503 on EKS | [AWS IAM cluster sign-in](aws-iam-cluster-auth.md), or [ArgoCD uses a sign-in command Sharko doesn't know](argocd-exec-plugin-auth-unsupported.md) |
| One cluster's sign-in details can't be read | [Sharko can't read one cluster's sign-in details](single-cluster-credential-fetch-failed.md) |
| A corporate proxy breaks TLS for `argocd-repo-server` | [Corporate TLS proxies](corporate-mitm-tls.md) |
| A password or token shows up in the pod logs | [A secret shows up in the logs](credential-leak-in-logs.md) |
| The audit log says the earliest entries were cut off | [Audit log](audit-log.md) |

## If none of this helps

Open an issue at <https://github.com/MoranWeissman/sharko/issues>.
Include the Sharko version (the `version` field from `/api/v1/health`),
the warning and error log lines from the last hour, and what you already
tried.

If the problem is about security (someone gets in who should not, or a
secret is exposed), do not open a public issue. Report it privately as
described in
[SECURITY.md](https://github.com/MoranWeissman/sharko/blob/main/SECURITY.md).
