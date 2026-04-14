# ArgoCD Diagnostics & AI Improvements

## 1. Surface ArgoCD connection errors in cluster UI

**Problem:** Sharko's cluster test passes (it tests its own credential provider connection), but ArgoCD may fail to connect using different credentials/roles. Users see "Failed" with no explanation.

**Plan:**
- [ ] Backend: Add ArgoCD cluster status to the cluster comparison endpoint (`GET /api/v1/clusters/{name}/comparison`)
  - Call ArgoCD API `GET /api/v1/clusters` and find the matching cluster
  - Extract `connectionState.status` and `connectionState.message` from ArgoCD's cluster resource
  - Add `argocd_connection_status` and `argocd_connection_message` fields to the response
- [ ] Frontend: Show ArgoCD connection error on ClusterDetail page
  - If `argocd_connection_status` is not "Successful", show a red banner with the error message
  - Label it clearly as "ArgoCD Connection Error" so users know it's ArgoCD, not Sharko
- [ ] Non-AI: The error message from ArgoCD is usually specific (e.g., "TLS handshake timeout", "Unauthorized", "x509: certificate signed by unknown authority") — display it verbatim

**Files to modify:**
- `internal/argocd/client.go` — add GetClusterStatus or extend ListClusters response
- `internal/service/cluster.go` — include ArgoCD connection state in comparison
- `internal/models/cluster.go` — add ArgoCD connection fields
- `ui/src/views/ClusterDetail.tsx` — render ArgoCD error banner

---

## 2. "Ask AI" button pre-fills context-aware prompt

**Problem:** Clicking "Ask AI" on a cluster page just opens an empty chat. It should pre-fill a prompt with relevant context.

**Plan:**
- [ ] Frontend: "Ask AI" buttons should pass context to the AI panel
  - From ClusterDetail: pre-fill with cluster name, status, ArgoCD error (if any), recent test results
  - From AddonDetail: pre-fill with addon name, health stats, version info
  - Format: "Why is {cluster} showing {status}? ArgoCD error: {message}. Last test: {result}."
- [ ] Wire the AI panel to accept an initial prompt via props or URL params
  - `Layout.tsx` AI panel already has state — add `initialPrompt` prop
  - When set, auto-send the prompt on panel open

**Files to modify:**
- `ui/src/views/ClusterDetail.tsx` — pass context when opening AI
- `ui/src/views/AddonDetail.tsx` — pass context when opening AI
- `ui/src/components/Layout.tsx` — accept and forward initial prompt to AI panel
- `ui/src/views/AIAssistant.tsx` — auto-send initial prompt

---

## 3. AI checks ArgoCD controller logs for specific errors

**Problem:** AI response is too generic — lists possible causes instead of checking actual errors. Should be able to read ArgoCD controller logs or at minimum use the ArgoCD API error messages.

**Plan:**
- [ ] Backend: Add AI tool `get_argocd_cluster_connection` that returns the connection state + error from ArgoCD API
  - This gives the AI the actual error message, not guesswork
- [ ] Backend: Add AI tool `get_argocd_controller_logs` (optional, requires K8s access to argocd namespace)
  - Read recent logs from `argocd-application-controller` pod filtered by cluster name
  - Return last 50 lines matching the cluster
  - This is powerful but requires RBAC permissions in the argocd namespace
- [ ] Update AI system prompt to instruct: "When a cluster shows connection failures, ALWAYS call get_argocd_cluster_connection first to get the actual error before suggesting generic troubleshooting"

**Files to modify:**
- `internal/ai/tools.go` — add get_argocd_cluster_connection tool
- `internal/ai/tools.go` — add get_argocd_controller_logs tool (optional)
- `internal/ai/agent.go` — update system prompt with diagnostic instructions
