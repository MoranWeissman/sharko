#!/usr/bin/env bash
#
# operator-playground-status.sh — Story 3: plain-English snapshot of operator convergence
#
# Shows:
#   1. ClusterAddons CRs (name, cluster, SYNCED, Ready) + Ready condition detail
#   2. Each spoke's ArgoCD cluster Secret addon labels (addon-key labels only — no slash)
#   3. Current drive mode (operator vs reconciler writer)
#   4. One-line summary
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
  err "Context '$CONTEXT' not found. Run 'make operator-playground-up' first."
fi

# Check if Sharko release is installed
if ! kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy sharko &>/dev/null; then
  err "Sharko deployment not found in namespace '$SHARKO_NS'. Run 'make operator-playground-up' first."
fi

echo ""
info "Operator Playground Status"
echo ""

# --- 1. ClusterAddons CRs ---
info "ClusterAddons CRs:"
echo ""

# Get all ClusterAddons across all namespaces
CA_JSON=$(kubectl --context="$CONTEXT" get ca -A -o json)
CA_COUNT=$(echo "$CA_JSON" | jq -r '.items | length')

if [ "$CA_COUNT" -eq 0 ]; then
  echo "  (no ClusterAddons CRs found)"
  echo ""
else
  # Print table header
  printf "  %-30s %-20s %-8s %-10s\n" "NAME" "CLUSTER" "SYNCED" "READY"
  printf "  %-30s %-20s %-8s %-10s\n" "----" "-------" "------" "-----"

  # Iterate over each CA and print summary + Ready condition detail
  for i in $(seq 0 $((CA_COUNT - 1))); do
    CA_NAME=$(echo "$CA_JSON" | jq -r ".items[$i].metadata.name")
    CA_CLUSTER=$(echo "$CA_JSON" | jq -r ".items[$i].spec.cluster")
    CA_SYNCED=$(echo "$CA_JSON" | jq -r ".items[$i].status.syncedAddons // 0")

    # Ready condition status
    READY_STATUS=$(echo "$CA_JSON" | jq -r ".items[$i].status.conditions[] | select(.type==\"Ready\") | .status // \"Unknown\"")
    READY_REASON=$(echo "$CA_JSON" | jq -r ".items[$i].status.conditions[] | select(.type==\"Ready\") | .reason // \"None\"")
    READY_MSG=$(echo "$CA_JSON" | jq -r ".items[$i].status.conditions[] | select(.type==\"Ready\") | .message // \"(no message)\"")

    printf "  %-30s %-20s %-8s %-10s\n" "$CA_NAME" "$CA_CLUSTER" "$CA_SYNCED" "$READY_STATUS"

    # Print Ready condition detail (reason + message) indented
    echo "    Ready: $READY_REASON — $READY_MSG"
    echo ""
  done
fi

# --- 2. ArgoCD Cluster Secret Addon Labels ---
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

    # Extract cluster name from Secret's server URL (stored in .data.name, base64-encoded)
    # For display, we'll use the secret name or extract the cluster label if present
    # The convention is that addon-key labels are those WITHOUT a slash

    # Filter addon-key labels: keys that do NOT contain '/' or ':' (those are system/foreign labels)
    ADDON_LABELS=$(echo "$SECRET_LABELS" | jq -r 'to_entries | map(select(.key | contains("/") or contains(":") | not)) | map("\(.key)=\(.value)") | join(", ")')

    if [ -z "$ADDON_LABELS" ] || [ "$ADDON_LABELS" = "null" ]; then
      ADDON_LABELS="(none)"
    fi

    echo "  Cluster Secret: $SECRET_NAME"
    echo "    Addon labels: $ADDON_LABELS"
    echo ""
  done
fi

# --- 3. Current Drive Mode ---
info "Current Drive Mode:"
echo ""

# Read SHARKO_OPERATOR_DRIVES_LABELS env from the deployment
DRIVE_ENV=$(kubectl --context="$CONTEXT" -n "$SHARKO_NS" get deploy sharko \
  -o jsonpath='{.spec.template.spec.containers[0].env[?(@.name=="SHARKO_OPERATOR_DRIVES_LABELS")].value}' 2>/dev/null || echo "")

if [ -z "$DRIVE_ENV" ] || [ "$DRIVE_ENV" = "false" ]; then
  MODE="DRIVE OFF"
  WRITER="reconciler"
  echo "  Mode: $MODE"
  echo "  Label writer: $WRITER (operator is read-only, status projection only)"
else
  MODE="DRIVE ON"
  WRITER="operator"
  echo "  Mode: $MODE"
  echo "  Label writer: $WRITER (operator drives addon labels; reconciler yields)"
fi
echo ""

# --- 4. One-Line Summary ---
info "Summary:"
echo ""

# Count how many spokes have addon labels present (non-empty addon-key labels)
SPOKES_WITH_LABELS=0
for i in $(seq 0 $((SECRET_COUNT - 1))); do
  SECRET_LABELS=$(echo "$SECRETS_JSON" | jq -r ".items[$i].metadata.labels // {}")
  ADDON_LABELS=$(echo "$SECRET_LABELS" | jq -r 'to_entries | map(select(.key | contains("/") or contains(":") | not)) | map("\(.key)=\(.value)") | join(", ")')
  if [ -n "$ADDON_LABELS" ] && [ "$ADDON_LABELS" != "null" ] && [ "$ADDON_LABELS" != "(none)" ]; then
    SPOKES_WITH_LABELS=$((SPOKES_WITH_LABELS + 1))
  fi
done

if [ "$MODE" = "DRIVE ON" ]; then
  echo "  DRIVE ON — the operator is the label writer; addon labels present on $SPOKES_WITH_LABELS/$SECRET_COUNT spokes."
else
  echo "  DRIVE OFF — the reconciler is the label writer (operator is read-only status only)."
fi
echo ""
