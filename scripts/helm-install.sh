#!/usr/bin/env bash
# Installer for ArgoCD Addons Platform.
# Builds the Docker image, pushes to registry, and deploys via Helm.
# Sources secrets from .env.secrets and passes them via --set (never in values files).
#
# Usage:
#   ./scripts/helm-install.sh                         # Sources .env.secrets from project root
#   ./scripts/helm-install.sh /path/to/.env.secrets   # Custom secrets file
#
# Required env vars in .env.secrets:
#   GITHUB_TOKEN          - GitHub PAT for Git provider access
#
# Optional env vars:
#   IMAGE_REGISTRY        - Container registry (e.g. 123456.dkr.ecr.us-east-1.amazonaws.com, ghcr.io/org)
#                           If unset, uses local image (minikube/kind/docker-desktop)
#   IMAGE_REPOSITORY      - Image name (default: aap-server)
#   ARGOCD_TOKEN, ARGOCD_NONPROD_SERVER_URL, ARGOCD_NONPROD_TOKEN
#   AI_API_KEY, AI_PROVIDER, AI_CLOUD_MODEL
#   DATADOG_API_KEY, DATADOG_APP_KEY

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

# Source the file (skip comments and empty lines)
set +u  # Allow unset/empty vars during sourcing
set -a
while IFS='=' read -r key value; do
  [[ -z "$key" || "$key" =~ ^[[:space:]]*# ]] && continue
  value="${value%\"}"
  value="${value#\"}"
  export "$key=$value"
done < "${SECRETS_FILE}"
set +a
set -u

# --- Validate required vars ---
if [[ -z "${GITHUB_TOKEN:-}" ]]; then
  echo "ERROR: GITHUB_TOKEN is required in ${SECRETS_FILE}"
  exit 1
fi

# --- Image config ---
IMAGE_REPO="${IMAGE_REPOSITORY:-aap-server}"
REGISTRY="${IMAGE_REGISTRY:-}"

if [[ -n "${REGISTRY}" ]]; then
  FULL_IMAGE="${REGISTRY}/${IMAGE_REPO}"
else
  FULL_IMAGE="${IMAGE_REPO}"
fi
IMAGE_TAG="${VERSION}"

# --- Update Helm chart + values with current version ---
CHART_YAML="${CHART_DIR}/Chart.yaml"
VALUES_PROD="${CHART_DIR}/values-production.yaml"
if [[ -f "${CHART_YAML}" ]]; then
  sed -i.bak "s/^version:.*/version: ${VERSION}/" "${CHART_YAML}" && rm -f "${CHART_YAML}.bak"
  sed -i.bak "s/^appVersion:.*/appVersion: \"${VERSION}\"/" "${CHART_YAML}" && rm -f "${CHART_YAML}.bak"
fi
if [[ -f "${VALUES_PROD}" ]]; then
  sed -i.bak "s/tag: \".*\"/tag: \"${VERSION}\"/" "${VALUES_PROD}" && rm -f "${VALUES_PROD}.bak"
fi

echo "=== ArgoCD Addons Platform Installer ==="
echo "  Version:   ${VERSION}"
echo "  Image:     ${FULL_IMAGE}:${IMAGE_TAG}"
echo "  Namespace: ${NAMESPACE}"
echo "  Chart:     ${CHART_DIR}"
echo "  Secrets:   ${SECRETS_FILE}"
echo "  AI:        ${AI_PROVIDER:-disabled} ${AI_CLOUD_MODEL:+(${AI_CLOUD_MODEL})}"
echo "  Datadog:   ${DATADOG_API_KEY:+enabled}${DATADOG_API_KEY:-disabled}"
echo ""

# --- Step 1: Login to registry (if configured) ---
if [[ -n "${REGISTRY}" ]]; then
  if [[ "${REGISTRY}" == ghcr.io* ]]; then
    GHCR_USER="${GHCR_USER:-$(echo "${REGISTRY}" | cut -d/ -f2)}"
    GHCR_PASS="${GHCR_TOKEN:-${GITHUB_TOKEN}}"
    echo ">>> Logging into GHCR as ${GHCR_USER}..."
    echo "${GHCR_PASS}" | docker login ghcr.io -u "${GHCR_USER}" --password-stdin
  elif [[ "${REGISTRY}" == *.dkr.ecr.*.amazonaws.com ]]; then
    AWS_REGION="$(echo "${REGISTRY}" | sed 's/.*\.dkr\.ecr\.\(.*\)\.amazonaws\.com/\1/')"
    echo ">>> Logging into ECR (${AWS_REGION})..."
    aws ecr get-login-password --region "${AWS_REGION}" | docker login --username AWS --password-stdin "${REGISTRY}"
  fi
fi

# --- Step 2: Build Docker image ---
echo ""
echo ">>> Building Docker image ${FULL_IMAGE}:${IMAGE_TAG} ..."

if [[ -n "${REGISTRY}" ]]; then
  # Remote registry: build multi-arch and push in one step
  # Ensure buildx builder exists
  if ! docker buildx inspect multiarch >/dev/null 2>&1; then
    echo "    Creating buildx builder 'multiarch'..."
    docker buildx create --name multiarch --use
  fi
  docker buildx build --builder multiarch \
    --platform linux/amd64,linux/arm64 \
    -t "${FULL_IMAGE}:${IMAGE_TAG}" \
    -t "${FULL_IMAGE}:latest" \
    --push "${PROJECT_ROOT}"
  echo "    Build + push complete."
else
  # Local: build for current platform only
  if command -v minikube >/dev/null 2>&1 && minikube status --format='{{.Host}}' 2>/dev/null | grep -q Running; then
    echo "    (Using minikube docker daemon)"
    eval "$(minikube docker-env)"
  fi
  docker build -t "${FULL_IMAGE}:${IMAGE_TAG}" -t "${FULL_IMAGE}:latest" "${PROJECT_ROOT}"
  echo "    Build complete."
fi

# --- Step 3: Helm upgrade --install ---
echo ""
echo ">>> Deploying with Helm..."

# Build --set args for secrets
SECRET_ARGS=(
  --set "image.repository=${FULL_IMAGE}"
  --set "image.tag=${IMAGE_TAG}"
  --set "secrets.GITHUB_TOKEN=${GITHUB_TOKEN}"
)

# Encryption key for migration credentials
[[ -n "${AAP_ENCRYPTION_KEY:-}" ]] && SECRET_ARGS+=(--set "secrets.AAP_ENCRYPTION_KEY=${AAP_ENCRYPTION_KEY}")

# ArgoCD tokens
[[ -n "${ARGOCD_TOKEN:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_TOKEN=${ARGOCD_TOKEN}")
[[ -n "${ARGOCD_NONPROD_SERVER_URL:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_NONPROD_SERVER_URL=${ARGOCD_NONPROD_SERVER_URL}")
[[ -n "${ARGOCD_NONPROD_TOKEN:-}" ]] && SECRET_ARGS+=(--set "secrets.ARGOCD_NONPROD_TOKEN=${ARGOCD_NONPROD_TOKEN}")

# AI
if [[ -n "${AI_API_KEY:-}" ]]; then
  SECRET_ARGS+=(--set "ai.apiKey=${AI_API_KEY}")
fi
[[ -n "${AI_BASE_URL:-}" ]] && SECRET_ARGS+=(--set "ai.baseURL=${AI_BASE_URL}")
[[ -n "${AI_AUTH_HEADER:-}" ]] && SECRET_ARGS+=(--set "ai.authHeader=${AI_AUTH_HEADER}")
[[ -n "${AI_MAX_ITERATIONS:-}" ]] && SECRET_ARGS+=(--set "ai.maxIterations=${AI_MAX_ITERATIONS}")

# GitOps actions
[[ "${GITOPS_ACTIONS_ENABLED:-}" == "true" ]] && SECRET_ARGS+=(--set "gitops.actions.enabled=true")

# Datadog
if [[ -n "${DATADOG_API_KEY:-}" ]]; then
  SECRET_ARGS+=(
    --set "datadog.apiKey=${DATADOG_API_KEY}"
    --set "datadog.appKey=${DATADOG_APP_KEY:-}"
  )
fi

# Basic auth
if [[ -n "${AAP_AUTH_USER:-}" ]]; then
  SECRET_ARGS+=(
    --set "auth.username=${AAP_AUTH_USER}"
    --set "auth.password=${AAP_AUTH_PASSWORD:-}"
  )
fi

# If no registry, prevent Kubernetes from trying to pull from Docker Hub
if [[ -z "${REGISTRY}" ]]; then
  SECRET_ARGS+=(--set "image.pullPolicy=Never")
fi

helm upgrade --install "${RELEASE}" "${CHART_DIR}" \
  --namespace "${NAMESPACE}" \
  --create-namespace \
  -f "${CHART_DIR}/values-production.yaml" \
  "${SECRET_ARGS[@]}"

echo ""
echo "=== Deployed successfully ==="
echo "  Version: ${VERSION}"
echo "  Image:   ${FULL_IMAGE}:${IMAGE_TAG}"
echo ""
echo "  kubectl -n ${NAMESPACE} get pods"
echo "  kubectl -n ${NAMESPACE} logs -f deploy/${RELEASE}-argocd-addons-platform"
echo "  kubectl -n ${NAMESPACE} port-forward svc/${RELEASE}-argocd-addons-platform 8080:8080"
