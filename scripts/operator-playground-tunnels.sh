#!/usr/bin/env bash
#
# operator-playground-tunnels.sh — Open browser tunnels for Sharko + ArgoCD + Gitea
#
# Opens three kubectl port-forward tunnels (Sharko on 8080, ArgoCD on 18443, Gitea on 13000)
# and blocks until Ctrl+C, then tears them ALL down cleanly.
#
# Guards: exits non-zero if sharko-play-hub context doesn't exist or Sharko isn't installed.

set -euo pipefail

CONTEXT="kind-sharko-play-hub"
SHARKO_NS="sharko"
ARGOCD_NS="argocd"

# --- Helpers ---

err() {
  echo "ERROR: $*" >&2
  exit 1
}

info() {
  echo "==> $*"
}

# --- Guards ---

# Check if context exists
if ! kubectl config get-contexts "$CONTEXT" &>/dev/null; then
  err "Context '$CONTEXT' not found. Run 'make operator-playground-up' first."
fi

# Check if Sharko release is installed
if ! kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy sharko &>/dev/null; then
  err "Sharko deployment not found in namespace '$SHARKO_NS'. Run 'make operator-playground-up' first."
fi

# --- Tunnel Cleanup ---

# Array to hold tunnel PIDs
TUNNEL_PIDS=()

cleanup() {
  echo ""
  info "Closing tunnels..."
  for pid in "${TUNNEL_PIDS[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
  echo "All tunnels closed."
}

trap cleanup EXIT INT TERM

# --- Open Tunnels ---

info "Opening tunnels..."

# 1. Sharko tunnel
kubectl --context "$CONTEXT" -n "$SHARKO_NS" port-forward svc/sharko 8080:80 >/dev/null 2>&1 &
TUNNEL_PIDS+=($!)

# 2. ArgoCD tunnel
kubectl --context "$CONTEXT" -n "$ARGOCD_NS" port-forward svc/argocd-server 18443:443 >/dev/null 2>&1 &
TUNNEL_PIDS+=($!)

# 3. Gitea tunnel
kubectl --context "$CONTEXT" -n "$SHARKO_NS" port-forward svc/gitea 13000:3000 >/dev/null 2>&1 &
TUNNEL_PIDS+=($!)

# Settle time for tunnels to accept connections
sleep 2

# --- Fetch ArgoCD Password ---

ARGOCD_PASSWORD=""
ARGOCD_PASSWORD=$(kubectl --context "$CONTEXT" -n "$ARGOCD_NS" get secret argocd-initial-admin-secret -o jsonpath='{.data.password}' 2>/dev/null | base64 -d 2>/dev/null || true)

if [ -z "$ARGOCD_PASSWORD" ]; then
  ARGOCD_PASSWORD="<not found — secret argocd-initial-admin-secret missing>"
fi

# --- Display URLs + Logins ---

echo ""
info "Tunnels are up!"
echo ""
echo "  Sharko   ->  http://localhost:8080      login: admin / admin"
echo "  ArgoCD   ->  https://localhost:18443    login: admin / $ARGOCD_PASSWORD   (accept the self-signed cert)"
echo "  Gitea    ->  http://localhost:13000     login: sharko / sharko-play"
echo ""
echo "Tunnels are up. Press Ctrl+C to close them all."
echo ""

# Block until interrupted
wait
