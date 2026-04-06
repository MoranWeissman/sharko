# Sharko — Post v1.1.0 TODO

> Issues and improvements found during v1.1.0 QA testing.

---

## Bugs

- [ ] **First-run wizard ArgoCD auto-discovery doesn't work** — wizard defaults to `https://argocd-server.argocd.svc.cluster.local` but the actual service name on this cluster is `argo-cd-argocd-server.argocd.svc.cluster.local`. The Phase 6 auto-discovery improvement (try common names like `argocd-server`, `argo-cd-argocd-server`, `argocd-argocd-server`) exists in `internal/argocd/client.go` but the wizard UI isn't using it — it shows a hardcoded default instead of calling the discover endpoint.
- [ ] **Wizard defaults to HTTPS for ArgoCD internal URL** — in-cluster ArgoCD services use HTTP (port 80), not HTTPS. The wizard should default to `http://` not `https://` for `.svc.cluster.local` addresses.
- [x] **CSP blocks Google Fonts stylesheets** — `style-src` was missing `https://fonts.googleapis.com`, causing the UI to break (fonts not loading, layout issues). Fixed in PR #90.
- [ ] **Dashboard crashes on uninitialized repo** — after creating a connection to an empty repo, the dashboard shows a raw 404 error (`GET .../cluster-addons.yaml: 404 Not Found`) instead of detecting the empty repo and suggesting `sharko init`. The fleet status endpoint should handle missing config files gracefully.
- [ ] **Wizard skips init step or doesn't detect empty repo** — after saving a connection, the wizard should check if the repo needs initialization and offer Step 4 (Initialize). Verify this flow works end-to-end.

---

## Improvements

(to be added during QA)

---

## Tech Debt

(to be added during QA)
