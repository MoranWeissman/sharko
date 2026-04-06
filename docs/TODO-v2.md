# Sharko — Post v1.1.0 TODO

> Issues and improvements found during v1.1.0 QA testing.

---

## Bugs

- [ ] **First-run wizard ArgoCD auto-discovery doesn't work** — wizard defaults to `https://argocd-server.argocd.svc.cluster.local` but the actual service name on this cluster is `argo-cd-argocd-server.argocd.svc.cluster.local`. The Phase 6 auto-discovery improvement (try common names like `argocd-server`, `argo-cd-argocd-server`, `argocd-argocd-server`) exists in `internal/argocd/client.go` but the wizard UI isn't using it — it shows a hardcoded default instead of calling the discover endpoint.

---

## Improvements

(to be added during QA)

---

## Tech Debt

(to be added during QA)
