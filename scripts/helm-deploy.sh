#!/usr/bin/env bash
# Deploy ArgoCD Addons Platform using a pre-built image from GHCR.
# Use this after the GitHub Actions release workflow has built and pushed the image.
#
# Usage:
#   ./scripts/helm-deploy.sh                         # Sources .env.secrets from project root
#   ./scripts/helm-deploy.sh /path/to/.env.secrets   # Custom secrets file
#
# The image is pulled from GHCR (built by the release workflow).
# Version is read from the VERSION file.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
CHART_DIR="${PROJECT_ROOT}/charts/argocd-addons-platform"
NAMESPACE="argocd-addons-platform"
RELEASE="aap"
# Read version: prefer .release-please-manifest.json, fall back to VERSION file
if [[ -f "${PROJECT_ROOT}/.release-please-manifest.json" ]]; then
  VERSION="$(grep -o '"\.": *"[^"]*"' "${PROJECT_ROOT}/.release-please-manifest.json" | grep -o '[0-9][0-9.]*')"
fi
if [[ -z "${VERSION:-}" ]]; then
  VERSION="$(cat "${PROJECT_ROOT}/VERSION" 2>/dev/null || echo "0.0.0")"
fi

# --- Source secrets ---
SECRETS_FILE="${1:-}"
if [[ -z "${SECRETS_FILE}" ]]; then
  if [[ -f "${PROJECT_ROOT}/secrets.env" ]]; then
    SECRETS_FILE="${PROJECT_ROOT}/secrets.env"
  elif [[ -f "${PROJECT_ROOT}/.env.secrets" ]]; then
    SECRETS_FILE="${PROJECT_ROOT}/.env.secrets"
  else
    echo "ERROR: No secrets file found. Create secrets.env (see secrets.env.example) or .env.secrets"
    echo "Usage: $0 [path-to-secrets-file]"
    exit 1
  fi
fi

set +u
set -a
while IFS='=' read -r key value; do
  [[ -z "$key" || "$key" =~ ^[[:space:]]*# ]] && continue
  value="${value%\"}"
  value="${value#\"}"
  export "$key=$value"
done < "${SECRETS_FILE}"
set +a
set -u

# --- Validate ---
if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "ERROR: GITHUB_TOKEN is required in ${SECRETS_FILE}"
  exit 1
fi

# --- Image config ---
IMAGE_REGISTRY="${IMAGE_REGISTRY:-ghcr.io/moranweissman}"
IMAGE_REPO="${IMAGE_REPOSITORY:-argocd-addons-platform}"
FULL_IMAGE="${IMAGE_REGISTRY}/${IMAGE_REPO}"

# --- Update Helm chart versions from VERSION file ---
CHART_YAML="${CHART_DIR}/Chart.yaml"
VALUES_PROD="${CHART_DIR}/values-production.yaml"
if [[ -f "${CHART_YAML}" ]]; then
  sed -i.bak "s/^version:.*/version: ${VERSION}/" "${CHART_YAML}" && rm -f "${CHART_YAML}.bak"
  sed -i.bak "s/^appVersion:.*/appVersion: \"${VERSION}\"/" "${CHART_YAML}" && rm -f "${CHART_YAML}.bak"
fi
if [[ -f "${VALUES_PROD}" ]]; then
  sed -i.bak "s/tag: \".*\"/tag: \"${VERSION}\"/" "${VALUES_PROD}" && rm -f "${VALUES_PROD}.bak"
fi

echo "=== ArgoCD Addons Platform Deploy ==="
echo "  Version:   ${VERSION}"
echo "  Image:     ${FULL_IMAGE}:${VERSION}"
echo "  Namespace: ${NAMESPACE}"
echo "  Chart:     ${CHART_DIR}"
echo ""

# --- Build Helm --set args ---
SECRET_ARGS=(
  --set "image.repository=${FULL_IMAGE}"
  --set "image.tag=${VERSION}"
  --set "secrets.GITHUB_TOKEN=${GITHUB_TOKEN}"
)

[[ -n "${AAP_ENCRYPTION_KEY:-}" ]] && SECRET_ARGS+=(--set "secrets.AAP_ENCRYPTION_KEY=${AAP_ENCRYPTION_KEY}")
[[ -n "${ARGOCD_TOKEN:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_TOKEN=${ARGOCD_TOKEN}")
[[ -n "${ARGOCD_SERVER_URL:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_SERVER_URL=${ARGOCD_SERVER_URL}")
[[ -n "${ARGOCD_NONPROD_SERVER_URL:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_NONPROD_SERVER_URL=${ARGOCD_NONPROD_SERVER_URL}")
[[ -n "${ARGOCD_NONPROD_TOKEN:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_NONPROD_TOKEN=${ARGOCD_NONPROD_TOKEN}")

if [[ -n "${AI_API_KEY:-}" ]]; then
  SECRET_ARGS+=(--set "ai.apiKey=${AI_API_KEY}")
fi
[[ -n "${AI_BASE_URL:-}" ]] && SECRET_ARGS+=(--set "ai.baseURL=${AI_BASE_URL}")
[[ -n "${AI_AUTH_HEADER:-}" ]] && SECRET_ARGS+=(--set "ai.authHeader=${AI_AUTH_HEADER}")
[[ -n "${AI_MAX_ITERATIONS:-}" ]] && SECRET_ARGS+=(--set "ai.maxIterations=${AI_MAX_ITERATIONS}")
[[ "${GITOPS_ACTIONS_ENABLED:-}" == "true" ]] && SECRET_ARGS+=(--set "gitops.actions.enabled=true")

if [[ -n "${DATADOG_API_KEY:-}" ]]; then
  SECRET_ARGS+=(
    --set "datadog.apiKey=${DATADOG_API_KEY}"
    --set "datadog.appKey=${DATADOG_APP_KEY:-}"
  )
fi

# --- Deploy ---
echo ">>> Deploying with Helm..."
helm upgrade --install "${RELEASE}" "${CHART_DIR}" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  -f "${CHART_DIR}/values-production.yaml" \
  "${SECRET_ARGS[@]}"

echo ""
echo "=== Deployed successfully ==="
echo "  Version: ${VERSION}"
echo "  Image:   ${FULL_IMAGE}:${VERSION}"
echo ""
echo "  kubectl -n ${NAMESPACE} get pods"
echo "  kubectl -n ${NAMESPACE} logs -f deploy/${RELEASE}-argocd-addons-platform"
