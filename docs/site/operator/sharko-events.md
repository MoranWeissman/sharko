# Sharko Kubernetes Events Reference

Sharko writes Kubernetes Events when something goes wrong, and for a few
things that went right. This lets you see trouble with `kubectl get events`
without reading the pod logs first.

The Events are attached to Sharko's own pod, in the namespace Sharko runs
in. The examples on this page use the namespace `sharko` and the Deployment
`sharko`. If you installed under a different namespace or release name, use
yours.

**What Sharko writes Events for:**

- AWS problems: taking on an IAM role, reading from Secrets Manager, creating
  an EKS token
- Not being able to reach ArgoCD, or ArgoCD refusing Sharko's token
- A cluster connection test that failed
- A pull request that could not be opened
- A cluster connection that someone else keeps changing, and repairs that
  someone asked for

**What Sharko does not write Events for:**

- ArgoCD syncs of each Application. ArgoCD writes its own Events for those.
- How far an addon has got in deploying. Use the ArgoCD UI or CLI for that.
- Each round of Sharko's background checks. Sharko writes an Event when
  something changes or really fails, not on every round.

**Only in Kubernetes.** Sharko only writes Events when it runs inside a
Kubernetes cluster. On your own machine it writes none.

## The Events Sharko sends

Each Event has a fixed `Reason` name. You can filter and alert on it.

| Reason | Type | What it means | What sends it |
|--------|------|---------------|---------------|
| **AWS** | | | |
| `AWSAssumeRoleFailed` | Warning | Taking on the IAM role failed | The connection doctor (`POST /api/v1/clusters/{name}/doctor`, the **Run connection doctor** button) |
| `AWSSecretsGetFailed` | Warning | Reading a secret from Secrets Manager failed | The connection doctor (`POST /api/v1/clusters/{name}/doctor`) |
| `AWSTokenMintFailed` | Warning | Creating an EKS token failed | Testing a cluster that needs AWS credentials (`POST /api/v1/clusters/{name}/test`) |
| **ArgoCD** | | | |
| `ArgoCDUnreachable` | Warning | Sharko could not reach the ArgoCD server (network or DNS) | Listing the clusters ArgoCD knows about (`GET /api/v1/clusters/available`) |
| `ArgoCDAuthFailed` | Warning | ArgoCD refused Sharko's token (401 or 403) | Listing the clusters ArgoCD knows about (`GET /api/v1/clusters/available`) |
| **Clusters** | | | |
| `ClusterTestFailed` | Warning | Sharko could not read from or write to the cluster during the test. The message has an error code | Testing a cluster (`POST /api/v1/clusters/{name}/test`) |
| `DriftDetected` | Warning | One of two things. Either the cluster's connection in ArgoCD no longer looks the way Sharko writes it, and Sharko did not change it. Or something else keeps overwriting Sharko's addon labels on a cluster Secret that Sharko does not own | Sharko's [cluster reconciler](cluster-reconciler.md) |
| `ConnectionRepaired` | Normal | Someone asked for a repair, and Sharko wrote the cluster's sign-in details again | A repair from the UI or the API |
| `AddonLabelsRepaired` | Normal | Someone asked for a repair, and Sharko only put the addon labels back | A repair from the UI or the API |
| **Git** | | | |
| `PROpenFailed` | Warning | Sharko could not open a pull request on your Git host | Any change that opens a pull request, for example registering a cluster or switching on an addon |

You may find more reason names in Sharko's source code. These are named
there but **never sent**, so do not alert on them: `AWSConfigLoadFailed`,
`AWSCredentialsInvalid`, `ArgoCDAPICallFailed`, `ClusterDoctorFailed`,
`ClusterConnectionFailed`, `ClusterRBACDenied`, `PRMergeFailed`,
`GitPushFailed`, `GitAuthFailed`, `GitCloneFailed`, `ReconcileFailed`,
`ClusterRegistered`, `ClusterReconciled`, `PRMerged` and
`ConnectionRestored`.

## Watching Events

### List recent Events

```bash
kubectl get events -n sharko --sort-by='.lastTimestamp'
```

### Watch Events as they happen

```bash
kubectl get events -n sharko --watch
```

### Show only warnings

```bash
kubectl get events -n sharko --field-selector type=Warning --sort-by='.lastTimestamp'
```

### Show each Event in full

```bash
kubectl describe events -n sharko
```

## What is in an Event

Each Event has:

- **Reason:** the fixed name from the table above
- **Type:** `Normal` or `Warning`
- **Message:** a plain sentence with details, such as the cluster name, the
  error, and what Sharko was doing

Event messages **never** contain secrets: no tokens, kubeconfigs,
credentials or secret values. Anyone who can read Events in that namespace
can read them.

## Using Events to find a problem

1. **Look at recent warnings:**
   ```bash
   kubectl get events -n sharko --field-selector type=Warning --sort-by='.lastTimestamp' | tail -20
   ```

2. **Then read the pod logs for the same time.** The Event tells you *what*
   failed. The logs tell you *why*. Note the time of the Event, then look at
   the logs around it:
   ```bash
   kubectl logs -n sharko deployment/sharko --since=10m
   ```

3. **Go to the matching runbook.** Some reasons have their own page, for
   example:
   - `ArgoCDUnreachable` → [ArgoCD cannot be reached](argocd-upstream-unreachable.md)
   - `AWSSecretsGetFailed` → [AWS Secrets Manager secret not found](aws-sm-secret-not-found.md)

## How long Events last

Kubernetes deletes Events after **1 hour** by default. The kube-apiserver
`--event-ttl` flag sets this, and it is usually `1h`.

If you want to keep them longer, send them somewhere else:

- Collect the pod logs centrally (CloudWatch, Elasticsearch and so on).
- Run an event exporter, for example
  [kubernetes-event-exporter](https://github.com/resmoio/kubernetes-event-exporter),
  to copy Events to storage outside the cluster.

Sharko's [Activity History](audit-log.md) is not a place to keep them. It
only holds the last 1000 entries, in memory, and is emptied when Sharko
restarts.

## Related pages

- [Cluster Reconciler](cluster-reconciler.md)
- [Connection Doctor](connection-doctor.md)
- [Troubleshooting](troubleshooting.md)
- [Failure Mode Index](failure-mode-index.md)
