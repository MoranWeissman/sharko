# Section 9 — Batch Operations & Job Queue

> How Sharko handles concurrent and batch write operations safely.

---

## The Problem

Write operations modify Git (open PRs), ArgoCD (create cluster secrets), and remote clusters (create K8s Secrets). When multiple requests come in simultaneously:

- Two operations branching from the same main HEAD and modifying the same file → merge conflict
- Two operations creating ArgoCD cluster secrets simultaneously → possible race conditions
- A batch of 20 clusters means 20 PRs, 20 secret creation operations, 20 ArgoCD interactions

Without coordination, concurrent operations will conflict and fail.

---

## How Other Systems Handle This

**ArgoCD:** Parallel across independent resources, serialized on the same resource. The application controller runs multiple workers processing apps from a work queue. Two syncs on the SAME app are serialized. Two syncs on DIFFERENT apps run in parallel.

**Kubernetes controllers:** Same pattern. Each controller reconciles one resource at a time per key, but different keys are processed concurrently.

**The pattern:** Per-resource locking. Parallel where safe, serialized where necessary.

---

## Decision: Job Queue with Per-Resource Locking

Every write operation goes through a job queue. This applies to ALL write operations — single and batch.

### Architecture

```
API Request
    ↓
Validate input
    ↓
Create Job → assign job ID → return 202 Accepted with job ID
    ↓
Job enters queue
    ↓
Queue Worker picks up job
    ↓
Acquire lock on resource key
    ↓
Execute operation (create secrets → create ArgoCD secret → open PR)
    ↓
Release lock
    ↓
Update job status → caller polls for result
```

### Resource Locking

Operations lock on a resource key. Operations on different resources run in parallel. Operations on the same resource wait.

| Operation | Resource Key | Can Run in Parallel With |
|-----------|-------------|--------------------------|
| add-cluster prod-eu | `cluster:prod-eu` | Any other cluster operation |
| add-cluster prod-us | `cluster:prod-us` | Any other cluster operation |
| update-cluster prod-eu | `cluster:prod-eu` | Waits for any running prod-eu operation |
| remove-cluster prod-eu | `cluster:prod-eu` | Waits for any running prod-eu operation |
| add-addon istio | `catalog` | Waits for any other catalog operation |
| add-addon keda | `catalog` | Waits for istio if running |
| remove-addon istio | `catalog` | Waits for any other catalog operation |
| init | `global` | Exclusive — nothing else runs during init |

This means:
- Adding 20 different clusters can run with controlled parallelism (e.g., 5 at a time)
- Two operations on the same cluster are always serialized
- Addon catalog changes are serialized (shared config file)
- Init locks everything (one-time operation, exclusive access)

### Git Safety

Each operation creates its own PR touching its own files. For cluster operations, each PR only modifies `configuration/addons-clusters-values/{cluster-name}.yaml` — different files, no conflicts, PRs can merge independently.

For addon operations that modify the shared catalog, serialization via the `catalog` lock ensures one PR is merged before the next one branches from the updated main.

The queue worker ensures each operation branches from a fresh main HEAD:
1. Acquire resource lock
2. Fetch latest main HEAD
3. Create branch from that HEAD
4. Make changes, commit, open PR
5. If auto-merge: merge the PR, wait for merge to complete
6. Release lock
7. Next queued operation for this resource starts from updated main

---

## API Design

### All Write Operations Return 202 Accepted

Every write endpoint returns immediately with a job ID:

```
POST /api/v1/clusters
{ "name": "prod-eu", "addons": {"monitoring": true} }

Response (202 Accepted):
{
  "job_id": "job-a1b2c3d4",
  "status": "queued",
  "operation": "add-cluster",
  "target": "prod-eu",
  "poll_url": "/api/v1/jobs/job-a1b2c3d4",
  "created_at": "2026-04-04T10:00:00Z"
}
```

### Job Status Polling

```
GET /api/v1/jobs/{job_id}

Response (in-progress):
{
  "job_id": "job-a1b2c3d4",
  "status": "in-progress",
  "operation": "add-cluster",
  "target": "prod-eu",
  "created_at": "2026-04-04T10:00:00Z",
  "started_at": "2026-04-04T10:00:01Z",
  "steps": [
    { "name": "fetch_credentials", "status": "done", "duration_ms": 450 },
    { "name": "create_addon_secrets", "status": "done", "duration_ms": 1200 },
    { "name": "verify_secrets", "status": "done", "duration_ms": 300 },
    { "name": "create_argocd_secret", "status": "in-progress" },
    { "name": "open_pr", "status": "pending" }
  ]
}

Response (completed):
{
  "job_id": "job-a1b2c3d4",
  "status": "success",
  "operation": "add-cluster",
  "target": "prod-eu",
  "created_at": "2026-04-04T10:00:00Z",
  "started_at": "2026-04-04T10:00:01Z",
  "completed_at": "2026-04-04T10:00:12Z",
  "result": {
    "cluster": { "name": "prod-eu", "server": "https://..." },
    "pr_url": "https://github.com/org/addons/pull/42",
    "merged": true,
    "secrets_created": ["datadog-keys"]
  }
}

Response (partial success):
{
  "job_id": "job-a1b2c3d4",
  "status": "partial",
  "operation": "add-cluster",
  "target": "prod-eu",
  "steps": [
    { "name": "fetch_credentials", "status": "done" },
    { "name": "create_addon_secrets", "status": "done" },
    { "name": "create_argocd_secret", "status": "done" },
    { "name": "open_pr", "status": "failed", "error": "Git push failed: authentication error" }
  ],
  "message": "Cluster registered in ArgoCD but PR failed. Run 'sharko remove-cluster prod-eu' to clean up, or retry."
}
```

### List Jobs

```
GET /api/v1/jobs
GET /api/v1/jobs?status=in-progress
GET /api/v1/jobs?operation=add-cluster

Response:
{
  "jobs": [
    { "job_id": "job-a1b2c3d4", "status": "success", "operation": "add-cluster", "target": "prod-eu", ... },
    { "job_id": "job-e5f6g7h8", "status": "queued", "operation": "add-cluster", "target": "prod-us", ... }
  ],
  "queue_depth": 3,
  "active_workers": 2
}
```

### Batch Endpoint

```
POST /api/v1/clusters/batch
{
  "clusters": [
    { "name": "cluster-1", "addons": {"monitoring": true, "logging": true} },
    { "name": "cluster-2", "addons": {"monitoring": true, "logging": true} },
    { "name": "cluster-3", "addons": {"monitoring": true, "logging": true} }
  ]
}

Response (202 Accepted):
{
  "batch_id": "batch-x1y2z3",
  "total": 3,
  "jobs": [
    { "job_id": "job-001", "cluster": "cluster-1", "status": "queued" },
    { "job_id": "job-002", "cluster": "cluster-2", "status": "queued" },
    { "job_id": "job-003", "cluster": "cluster-3", "status": "queued" }
  ],
  "poll_url": "/api/v1/batches/batch-x1y2z3"
}
```

### Batch Status

```
GET /api/v1/batches/{batch_id}

Response:
{
  "batch_id": "batch-x1y2z3",
  "total": 3,
  "completed": 2,
  "failed": 1,
  "in_progress": 0,
  "jobs": [
    { "job_id": "job-001", "cluster": "cluster-1", "status": "success", "pr_url": "...pull/42" },
    { "job_id": "job-002", "cluster": "cluster-2", "status": "success", "pr_url": "...pull/43" },
    { "job_id": "job-003", "cluster": "cluster-3", "status": "failed", "error": "kubeconfig not found" }
  ]
}
```

### Discover Available Clusters

```
GET /api/v1/clusters/available

Response:
{
  "provider": "aws-sm",
  "available_clusters": [
    { "name": "cluster-1", "region": "eu-west-1", "registered": false },
    { "name": "cluster-2", "region": "eu-west-1", "registered": false },
    { "name": "prod-eu", "region": "eu-west-1", "registered": true }
  ]
}
```

Shows all clusters in the secrets provider, with `registered: true/false` indicating whether they're already onboarded. The UI and CLI can use this for a "pick which clusters to onboard" experience.

---

## CLI Experience

### Single Operation — Feels Synchronous

The CLI hides the async nature. It submits the job, then polls automatically:

```bash
$ sharko add-cluster prod-eu --addons monitoring,logging

Queued job job-a1b2c3d4...

Fetching credentials...                done
Creating addon secrets on prod-eu...   done
Creating ArgoCD cluster secret...      done
Opening PR...                          done
Merging PR...                          done

✓ Cluster prod-eu registered.
  PR: https://github.com/org/addons/pull/42
  Addons: monitoring, logging
  Secrets created: datadog-keys
```

The user doesn't know there's a queue. It feels like a synchronous command. But behind the scenes, if another operation is running on the same resource, the CLI shows:

```bash
$ sharko add-cluster prod-eu --addons istio

Queued job job-e5f6g7h8...
Waiting for lock on cluster:prod-eu (another operation in progress)...
Lock acquired.
Fetching credentials...                done
...
```

### Batch — Shows Parallel Progress

```bash
$ sharko add-clusters cluster-1,cluster-2,cluster-3 --addons monitoring,logging

Batch batch-x1y2z3: 3 clusters queued

 [1/3] cluster-1    ⟳ creating secrets...
 [2/3] cluster-2    ⟳ creating secrets...
 [3/3] cluster-3    ⟳ fetching credentials...

 [1/3] cluster-1    ✓ PR #42 (merged)
 [2/3] cluster-2    ✓ PR #43 (merged)
 [3/3] cluster-3    ✗ kubeconfig not found in provider

2 succeeded, 1 failed.
Failed: cluster-3 — kubeconfig not found in provider
Fix and retry: sharko add-cluster cluster-3 --addons monitoring,logging
```

### Discover and Onboard

```bash
$ sharko add-clusters --from-provider --addons monitoring,logging

Discovering clusters from AWS Secrets Manager...
Found 20 clusters, 15 already registered, 5 new:

  [ ] cluster-16 (eu-west-1)
  [ ] cluster-17 (eu-west-1)
  [ ] cluster-18 (us-east-1)
  [ ] cluster-19 (us-east-1)
  [ ] cluster-20 (ap-southeast-1)

Register all 5? [Y/n/select] > Y

Batch batch-abc123: 5 clusters queued
...
```

---

## UI Experience

### Batch Add from UI

Clusters page → "Add Clusters" button (plural) → multi-step form:

```
Step 1 — Select Clusters
  Sharko shows all clusters from the provider, checkboxes.
  Already registered clusters are greyed out.
  
Step 2 — Configure Addons
  Select which addons all clusters should get.
  (Default addons pre-selected if configured)

Step 3 — Review and Submit
  "5 clusters will be registered with addons: monitoring, logging"
  "5 PRs will be opened (one per cluster)"
  [Submit]
```

After submit: real-time progress table showing each cluster's status updating live. Green checks appearing as each completes. Red X for failures with error details.

### Job Queue Visibility

Settings or Admin page → "Job Queue" section:

- Active jobs with progress
- Recent completed jobs
- Failed jobs with error details
- Queue depth indicator

This is observability into Sharko's own operations — the equivalent of ArgoCD's "Applications" view but for Sharko's management operations.

---

## Implementation

### Queue Worker

```go
type JobQueue struct {
    mu        sync.Mutex
    jobs      map[string]*Job           // job ID → job
    batches   map[string]*Batch         // batch ID → batch
    locks     map[string]*sync.Mutex    // resource key → mutex
    pending   chan *Job                  // buffered channel as the queue
    workers   int                       // concurrent workers (configurable)
}

type Job struct {
    ID          string
    Status      string    // queued, in-progress, success, partial, failed
    Operation   string    // add-cluster, remove-cluster, add-addon, etc.
    Target      string    // resource name
    ResourceKey string    // lock key (cluster:prod-eu, catalog, global)
    Request     interface{} // the original request payload
    Steps       []JobStep
    Result      interface{}
    CreatedAt   time.Time
    StartedAt   *time.Time
    CompletedAt *time.Time
    Error       string
    BatchID     string    // empty for single operations
}
```

### Worker Pool

Configurable number of workers (default: 5). Each worker:

1. Picks a job from the pending channel
2. Acquires the resource lock
3. Executes the operation via the orchestrator
4. Updates job status after each step
5. Releases the lock
6. Picks next job

### Job Retention

- Completed jobs retained for 1 hour, then garbage collected
- Failed jobs retained for 24 hours (for debugging)
- In-memory storage — jobs are lost on server restart
- Acceptable for v1.0.0 — callers retry if they can't find their job after a restart

### Configuration

```yaml
# Helm values
queue:
  workers: 5              # concurrent workers
  maxDepth: 100           # max queued jobs before returning 429
  jobRetentionHours: 1    # how long to keep completed job results
  failedRetentionHours: 24
```

### Rate Limiting

API-level rate limiting:
- Single operations: 10 per minute per authenticated user
- Batch operations: 1 per minute per authenticated user (each batch can contain up to 50 clusters)
- Job status polling: 60 per minute (generous, it's just a GET)

If queue depth exceeds `maxDepth`: return 429 Too Many Requests with `Retry-After` header.

---

## How This Changes Existing Write Endpoints

Every existing write endpoint (from Step 8 — Write API) changes from synchronous to async:

| Endpoint | Before | After |
|----------|--------|-------|
| `POST /api/v1/clusters` | 201/207 with result | 202 with job ID |
| `DELETE /api/v1/clusters/{name}` | 200/207 with result | 202 with job ID |
| `PATCH /api/v1/clusters/{name}` | 200/207 with result | 202 with job ID |
| `POST /api/v1/clusters/{name}/refresh` | 200 | 202 with job ID |
| `POST /api/v1/addons` | 201 with result | 202 with job ID |
| `DELETE /api/v1/addons/{name}` | 200/400 with result | 202 with job ID (dry-run stays synchronous) |
| `POST /api/v1/init` | 201 with result | 202 with job ID |

**Exception:** `DELETE /api/v1/addons/{name}` WITHOUT `?confirm=true` (the dry-run) stays synchronous — it's a read operation (checking impact), not a write.

**New endpoints:**
```
GET    /api/v1/jobs                    → list jobs (filterable by status, operation)
GET    /api/v1/jobs/{job_id}           → job status with steps
POST   /api/v1/clusters/batch          → batch cluster registration
GET    /api/v1/batches/{batch_id}      → batch progress
GET    /api/v1/clusters/available      → discover unregistered clusters from provider
```

---

## Summary

| Question | Answer |
|----------|--------|
| Are write operations synchronous or async? | Async. Every write returns 202 with a job ID. |
| How does the caller get results? | Poll `GET /api/v1/jobs/{job_id}` |
| Does the CLI feel synchronous? | Yes. CLI auto-polls and shows progress. User doesn't know about the queue. |
| Can operations run in parallel? | Yes — different resources in parallel, same resource serialized. |
| How are Git conflicts prevented? | Per-resource locking. Each operation branches from fresh main HEAD after previous merge. |
| How does batch work? | `POST /api/v1/clusters/batch` creates N individual jobs. One PR per cluster. |
| Where is the queue? | In-memory. Lost on restart. Callers retry. |
| What are the limits? | 5 workers, 100 queue depth, rate limits per user. |
