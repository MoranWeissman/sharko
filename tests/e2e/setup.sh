#!/bin/bash
set -e

# Create Kind cluster
kind create cluster --name sharko-e2e --wait 60s

# Install ArgoCD
kubectl create namespace argocd
# Use server-side apply: the ApplicationSet CRD's metadata exceeds the 256KB
# size limit of the kubectl.kubernetes.io/last-applied-configuration annotation
# that client-side `kubectl apply` writes. Server-side apply doesn't use that
# annotation and handles large objects natively.
kubectl apply --server-side --force-conflicts -n argocd -f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml
kubectl wait --for=condition=available --timeout=120s deployment/argocd-server -n argocd

# Build and load Sharko image
docker build -t sharko:e2e .
kind load docker-image sharko:e2e --name sharko-e2e

# Install Sharko
helm install sharko charts/sharko/ \
  --namespace sharko --create-namespace \
  --set image.repository=sharko \
  --set image.tag=e2e \
  --set image.pullPolicy=Never

kubectl wait --for=condition=available --timeout=60s deployment/sharko -n sharko

echo "E2E environment ready"
