# Addon Deployment Issues — Runbook

This runbook covers how to respond to alerts from the addon deployment monitors deployed via Datadog Operator on each cluster.

**Dashboard:** Addon Deployment Health — `<cluster-name>`
**Monitors:** Deployed as DatadogMonitor CRDs via `charts/datadog-operator-crds/`

---

## 1. Addon Pod CrashLoopBackOff

**Severity:** Critical
**Monitor:** `addon-crashloop-<cluster>`
**Meaning:** A container in an addon namespace keeps crashing and restarting.

**Steps:**
1. Open the Addon Deployment Health dashboard, select the affected namespace
2. Check **Pod Details** table — identify which pod is restarting
3. Check **Logs** section — look for error messages right before the crash
4. Common causes:
   - **Missing config/secrets:** Pod can't find a ConfigMap or Secret it needs. Check if ExternalSecrets are synced
   - **Startup probe failure:** App takes too long to start. Check probe config in Helm values
   - **Dependency not ready:** Pod depends on a service that isn't available yet (e.g. DB, API)
   - **Bad image:** Wrong image tag or broken build. Check the ArgoCD app for recent syncs
5. Get pod logs: `kubectl logs -n <namespace> <pod-name> --previous` (shows logs from the crashed container)
6. Describe the pod for events: `kubectl describe pod -n <namespace> <pod-name>`

---

## 2. K8s Error Events

**Severity:** Warning
**Monitor:** `addon-error-events-<cluster>`
**Meaning:** More than 5 Kubernetes error events in an addon namespace within 10 minutes.

**Steps:**
1. Open the dashboard, check **Kubernetes Events** section for the affected namespace
2. Common event types:
   - **FailedScheduling:** Node doesn't have enough resources or pod has unsatisfiable affinity/taints. Check node capacity
   - **FailedMount:** Volume mount failed. Check PVC status and storage class
   - **BackOff:** Container keeps crashing (usually accompanied by CrashLoop alert)
   - **Unhealthy:** Liveness or readiness probe failed. Check probe endpoints and thresholds
   - **FailedCreate:** ReplicaSet can't create pods. Check RBAC and resource quotas
3. Get events directly: `kubectl get events -n <namespace> --sort-by='.lastTimestamp' --field-selector type=Warning`

---

## 3. Addon Container OOMKilled

**Severity:** Critical
**Monitor:** `addon-oomkilled-<cluster>`
**Meaning:** A container was killed because it exceeded its memory limit.

**Steps:**
1. Open the dashboard, check **Resource Usage → Memory** chart for the affected namespace
2. Look for memory climbing steadily to the limit before the OOM event
3. Determine if it's a **memory leak** (gradual increase) or **legitimate load** (spike):
   - **Memory leak:** File a bug with the addon team. As a temporary fix, increase memory limit
   - **Legitimate load:** Increase memory limit in the addon's Helm values
4. Check current limits: `kubectl get pods -n <namespace> -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.spec.containers[*].resources.limits.memory}{"\n"}{end}'`
5. Update memory limit in `configuration/addons-clusters-values/<cluster>.yaml` or the addon's global values

---

## 4. High Restart Rate

**Severity:** Warning (>3 restarts/10min), Critical (>5 restarts/10min)
**Monitor:** `addon-high-restarts-<cluster>`
**Meaning:** Containers are restarting frequently, even if not in CrashLoopBackOff yet.

**Steps:**
1. Open the dashboard, check **Pod Restarts Over Time** chart
2. Identify which pods are restarting and the pattern:
   - **All pods restarting:** Likely a shared dependency issue (DB, config change, node problem)
   - **Single pod:** Likely pod-specific issue (resource limits, liveness probe)
3. Check if a recent ArgoCD sync triggered the restarts (new image, changed config)
4. Check pod logs for the restart reason: `kubectl logs -n <namespace> <pod-name> --previous`

---

## 5. CPU Near Limits

**Severity:** Warning (>80%), Critical (>90% for 15min)
**Monitor:** `addon-cpu-pressure-<cluster>`
**Meaning:** An addon is consistently using most of its CPU allocation.

**Steps:**
1. Open the dashboard, check **Resource Usage → CPU** chart
2. Compare Usage vs Requests vs Limits:
   - **Usage near Limits:** Pod is being CPU-throttled. Increase CPU limits
   - **Usage near Requests but below Limits:** Pod is fine, requests might be set too low
3. Check if this is a new pattern (recent deployment) or gradual increase (growing load)
4. If gradual: consider scaling horizontally (more replicas) rather than just increasing limits
5. Update CPU limit in the addon's Helm values

---

## 6. Memory Near Limits

**Severity:** Warning (>80%), Critical (>90% for 15min)
**Monitor:** `addon-memory-pressure-<cluster>`
**Meaning:** An addon is approaching its memory limit. OOMKill may follow.

**Steps:**
1. Open the dashboard, check **Resource Usage → Memory** chart
2. Distinguish between:
   - **Steady high usage:** Normal for the addon, increase limits proactively before OOM
   - **Climbing over time:** Possible memory leak, investigate with the addon team
   - **Spikes:** Check if correlated with specific operations (batch jobs, cache warming)
3. **Act before OOMKill:** If usage is >90% and climbing, increase memory limit now
4. Update memory limit in the addon's Helm values

---

## 7. Pods Stuck Pending

**Severity:** Warning (pending >10min)
**Monitor:** `addon-pending-pods-<cluster>`
**Meaning:** Pods can't be scheduled onto any node.

**Steps:**
1. Check why the pod can't schedule: `kubectl describe pod -n <namespace> <pod-name>` — look at the Events section
2. Common causes:
   - **Insufficient resources:** Nodes don't have enough CPU/memory. Check node utilization or wait for Karpenter to scale up
   - **Node affinity/taints:** Pod requires specific node labels or tolerations that no node matches. Check the addon's nodeSelector/tolerations config
   - **PVC binding:** Pod needs a PersistentVolumeClaim that can't be bound (wrong storage class, AZ mismatch)
   - **Too many pods:** Node has hit its pod limit. Check `kubectl get nodes -o jsonpath='{range .items[*]}{.metadata.name}{"\t"}{.status.allocatable.pods}{"\n"}{end}'`
3. If Karpenter is enabled, check if a NodePool exists that can satisfy the pod's requirements
4. For AZ-specific PVC issues: the pod and PV must be in the same AZ

---

## General Troubleshooting Tips

- **Always start with the dashboard** — select the affected namespace and check all sections before SSHing into the cluster
- **Check recent ArgoCD syncs** — many issues are caused by config changes. Look at the Application Operations dashboard for recent sync activity
- **Compare with other clusters** — if the same addon works on other clusters, diff the cluster-specific values
- **Escalation:** If the addon is owned by another team, share the dashboard link with the namespace filter pre-selected
