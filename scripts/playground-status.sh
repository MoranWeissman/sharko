#!/usr/bin/env bash
#
# playground-status.sh — plain-English snapshot of playground state
#
# Shows:
#   1. Each spoke's ArgoCD cluster Secret addon labels (addon-key labels only — no slash)
#   2. Gitea state (deployment readiness + service reachability)
#   3. One-line summary
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

# Check if context exists
if ! kubectl config get-contexts "$CONTEXT" &>/dev/null; then
  err "Context '$CONTEXT' not found. Run 'make playground-up' first."
fi

# Check if Sharko release is installed
if ! kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy sharko &>/dev/null; then
  err "Sharko deployment not found in namespace '$SHARKO_NS'. Run 'make playground-up' first."
fi

echo ""
info "Playground Status"
echo ""

# --- 1. ArgoCD Cluster Secret Addon Labels ---
info "ArgoCD Cluster Secret Addon Labels:"
echo ""

# Get all cluster secrets
SECRETS_JSON=$(kubectl --context="$CONTEXT" -n "$ARGOCD_NS" get secret \
  -l argocd.argoproj.io/secret-type=cluster -o json 2>/dev/null || echo '{"items":[]}')
SECRET_COUNT=$(echo "$SECRETS_JSON" | jq -r '.items | length')

if [ "$SECRET_COUNT" -eq 0 ]; then
  echo "  (no ArgoCD cluster secrets found)"
  echo ""
else
  for i in $(seq 0 $((SECRET_COUNT - 1))); do
    SECRET_NAME=$(echo "$SECRETS_JSON" | jq -r ".items[$i].metadata.name")
    SECRET_LABELS=$(echo "$SECRETS_JSON" | jq -r ".items[$i].metadata.labels // {}")

    # Filter addon-key labels: either a v3 plain addon-name key (no '/' or
    # ':' — those are system/foreign labels) or a v4 "addons.sharko.dev/"
    # key (internal/models/addon_labels.go — V4AddonLabelPrefix). v4 addon
    # labels DO contain a '/', so a plain "no slash" filter would silently
    # hide every addon the v4 real-doors flow enables.
    ADDON_LABELS=$(echo "$SECRET_LABELS" | jq -r 'to_entries | map(select((.key | (contains("/") or contains(":")) | not) or (.key | startswith("addons.sharko.dev/")))) | map("\(.key)=\(.value)") | join(", ")')

    if [ -z "$ADDON_LABELS" ] || [ "$ADDON_LABELS" = "null" ]; then
      ADDON_LABELS="(none)"
    fi

    echo "  Cluster Secret: $SECRET_NAME"
    echo "    Addon labels: $ADDON_LABELS"
    echo ""
  done
fi

# --- 2. Gitea State ---
info "Gitea State:"
echo ""

GITEA_READY="no"
if kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy gitea &>/dev/null; then
  GITEA_AVAILABLE=$(kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy gitea \
    -o jsonpath='{.status.conditions[?(@.type=="Available")].status}' 2>/dev/null || echo "")
  if [ "$GITEA_AVAILABLE" = "True" ]; then
    GITEA_READY="yes"
  fi
  echo "  Deployment: found (Available=$GITEA_AVAILABLE)"
  echo "  Service:    svc/gitea.$SHARKO_NS:3000 (tunnel via 'make playground-tunnels')"
else
  echo "  Deployment: not found in namespace '$SHARKO_NS'"
fi
echo ""

# --- 3. One-Line Summary ---
info "Summary:"
echo ""

# Count how many spokes have addon labels present (non-empty addon-key labels)
SPOKES_WITH_LABELS=0
for i in $(seq 0 $((SECRET_COUNT - 1))); do
  SECRET_LABELS=$(echo "$SECRETS_JSON" | jq -r ".items[$i].metadata.labels // {}")
  ADDON_LABELS=$(echo "$SECRET_LABELS" | jq -r 'to_entries | map(select((.key | (contains("/") or contains(":")) | not) or (.key | startswith("addons.sharko.dev/")))) | map("\(.key)=\(.value)") | join(", ")')
  if [ -n "$ADDON_LABELS" ] && [ "$ADDON_LABELS" != "null" ] && [ "$ADDON_LABELS" != "(none)" ]; then
    SPOKES_WITH_LABELS=$((SPOKES_WITH_LABELS + 1))
  fi
done

echo "  Addon labels present on $SPOKES_WITH_LABELS/$SECRET_COUNT spokes. Gitea ready: $GITEA_READY."
echo ""
