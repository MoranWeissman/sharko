# ArgoCD Addons Platform - Go Rewrite
# Makefile for development, testing, building, and deployment

# Configuration
K8S_NAMESPACE = argocd-addons-platform
IMAGE_NAME    = aap-server
MINIKUBE_PROFILE = minikube

# Read version from VERSION file
VERSION = $(shell cat VERSION 2>/dev/null || echo "0.0.0")

.PHONY: help dev run build-go test test-go lint build deploy update status logs undeploy
.PHONY: create-secrets list-secrets version
.PHONY: check-minikube ensure-namespace version-patch
.PHONY: test-coverage pre-commit-install

# Default target
help: ## Show all available targets
	@echo "ArgoCD Addons Platform (Go) - Available Targets"
	@echo "================================================"
	@echo ""
	@echo "Development:"
	@echo "  make dev            Run Go backend + Vite dev server concurrently"
	@echo "  make build-go       Build Go binary locally"
	@echo "  make test           Run all tests (Go + UI)"
	@echo "  make test-go        Run Go tests only"
	@echo "  make lint           Run golangci-lint + eslint"
	@echo ""
	@echo "Docker & Deployment:"
	@echo "  make build          Build Docker image (auto-increment patch version)"
	@echo "  make deploy         Build + deploy to minikube"
	@echo "  make update         Quick update: build + restart deployment"
	@echo "  make undeploy       Remove deployment from minikube"
	@echo ""
	@echo "Kubernetes Status:"
	@echo "  make status         Show deployment status"
	@echo "  make logs           Show pod logs"
	@echo ""
	@echo "Secrets Management:"
	@echo "  make create-secrets Create K8s secrets from .env.secrets"
	@echo "  make list-secrets   List secrets in namespace"
	@echo ""
	@echo "Versioning:"
	@echo "  make version        Show current version"

# ---------------------------------------------------------------------------
# Development
# ---------------------------------------------------------------------------

dev: ## Run Go backend + Vite dev server concurrently
	@if [ ! -f config.yaml ]; then \
		echo "Error: config.yaml not found."; \
		echo "Copy the example and fill in your values:"; \
		echo "  cp config.yaml.example config.yaml"; \
		exit 1; \
	fi
	@echo "Loading secrets from .env.secrets (if exists)..."
	@echo "Starting Go backend and Vite dev server..."
	@trap 'kill 0' EXIT; \
		( [ -f .env.secrets ] && set -a && . ./.env.secrets && set +a; \
		  go run ./cmd/aap-server ) & \
		( sleep 2 && cd ui && npm run dev ) & \
		wait

build-go: ## Build Go binary locally
	@echo "Building Go binary..."
	@mkdir -p bin
	CGO_ENABLED=0 go build -o bin/aap-server ./cmd/aap-server
	@echo "Binary built: bin/aap-server"

run: build-go ## Build and run locally (loads .env.secrets)
	@if [ ! -f config.yaml ]; then \
		echo "Error: config.yaml not found. Run: cp config.yaml.example config.yaml"; \
		exit 1; \
	fi
	@if [ -f .env.secrets ]; then set -a && . ./.env.secrets && set +a; fi && \
		./bin/aap-server --static ui/dist

# ---------------------------------------------------------------------------
# Testing
# ---------------------------------------------------------------------------

test: test-go ## Run all tests (Go + UI)
	@if [ -f ui/package.json ] && grep -q '"test"' ui/package.json; then \
		echo "Running UI tests..."; \
		cd ui && npm test -- --run 2>/dev/null || cd ui && npm test; \
	else \
		echo "No UI test script found, skipping."; \
	fi

test-go: ## Run Go tests only
	@echo "Running Go tests..."
	go test ./... -v

# ---------------------------------------------------------------------------
# Linting
# ---------------------------------------------------------------------------

lint: ## Run golangci-lint + eslint
	@echo "Running Go linter..."
	@if command -v golangci-lint >/dev/null 2>&1; then \
		golangci-lint run ./...; \
	else \
		echo "golangci-lint not installed. Install: https://golangci-lint.run/usage/install/"; \
	fi
	@echo ""
	@echo "Running ESLint..."
	@if [ -f ui/package.json ]; then \
		cd ui && npm run lint; \
	else \
		echo "No ui/package.json found, skipping ESLint."; \
	fi

# ---------------------------------------------------------------------------
# Versioning
# ---------------------------------------------------------------------------

version: ## Show current version
	@echo "Current version: $(VERSION)"

version-patch: ## Auto-increment patch version
	@NEW_VERSION=$$(./scripts/version.sh patch) && echo "New version: $$NEW_VERSION"

# ---------------------------------------------------------------------------
# Docker Build
# ---------------------------------------------------------------------------

build: version-patch ## Build Docker image (auto-increment patch version)
	$(eval BUILD_VERSION := $(shell cat VERSION))
	@echo "Building Docker image $(IMAGE_NAME):$(BUILD_VERSION)..."
	@eval $$(minikube docker-env) && \
		docker build -t $(IMAGE_NAME):$(BUILD_VERSION) . && \
		docker tag $(IMAGE_NAME):$(BUILD_VERSION) $(IMAGE_NAME):latest
	@echo "Image built: $(IMAGE_NAME):$(BUILD_VERSION)"

# ---------------------------------------------------------------------------
# Minikube Helpers
# ---------------------------------------------------------------------------

check-minikube: ## Check if minikube is running
	@if ! command -v minikube >/dev/null 2>&1; then \
		echo "Error: minikube not found. Please install minikube first."; \
		exit 1; \
	fi
	@if ! minikube status | grep -q "Running"; then \
		echo "Error: minikube is not running. Start it with: minikube start"; \
		exit 1; \
	fi

ensure-namespace: check-minikube ## Ensure namespace exists
	@kubectl create namespace $(K8S_NAMESPACE) --dry-run=client -o yaml | kubectl apply -f -

# ---------------------------------------------------------------------------
# Deployment
# ---------------------------------------------------------------------------

deploy: build ensure-namespace ## Build + deploy to minikube (loads secrets from .env.secrets)
	$(eval DEPLOY_VERSION := $(shell cat VERSION))
	@echo "Deploying $(IMAGE_NAME):$(DEPLOY_VERSION) to minikube..."
	@# Create config.yaml as a ConfigMap so the pod can read it
	@if [ -f config.yaml ]; then \
		kubectl create configmap aap-config \
			--from-file=config.yaml=config.yaml \
			-n $(K8S_NAMESPACE) \
			--dry-run=client -o yaml | kubectl apply -f -; \
		echo "ConfigMap aap-config created from config.yaml"; \
	else \
		echo "Warning: config.yaml not found. Pod will start without connections."; \
	fi
	@# Create secrets from .env.secrets so env vars resolve inside the pod
	@if [ -f .env.secrets ]; then \
		kubectl create secret generic aap-env-secrets \
			--from-env-file=.env.secrets \
			-n $(K8S_NAMESPACE) \
			--dry-run=client -o yaml | kubectl apply -f -; \
		echo "Secret aap-env-secrets created from .env.secrets"; \
	else \
		echo "Warning: .env.secrets not found. Env vars in config.yaml won't resolve."; \
	fi
	@kubectl apply -f k8s/namespace.yaml
	@kubectl apply -f k8s/deployment.yaml
	@kubectl apply -f k8s/service.yaml
	@kubectl apply -f k8s/ingress.yaml 2>/dev/null || echo "Ingress not applied (controller may not be available)"
	@echo ""
	@echo "Deployment complete."
	@echo "  Version:   $(DEPLOY_VERSION)"
	@echo "  Namespace: $(K8S_NAMESPACE)"
	@echo ""
	@echo "Check status: make status"
	@echo "Port-forward: kubectl port-forward -n $(K8S_NAMESPACE) service/aap-server 8080:8080"

update: build ensure-namespace ## Quick update: build + restart deployment
	$(eval UPDATE_VERSION := $(shell cat VERSION))
	@echo "Updating to $(IMAGE_NAME):$(UPDATE_VERSION)..."
	@kubectl set image deployment/aap-server aap-server=$(IMAGE_NAME):$(UPDATE_VERSION) -n $(K8S_NAMESPACE)
	@kubectl rollout restart deployment/aap-server -n $(K8S_NAMESPACE)
	@kubectl wait --for=condition=available --timeout=60s deployment/aap-server -n $(K8S_NAMESPACE)
	@echo "Update complete: $(IMAGE_NAME):$(UPDATE_VERSION)"

undeploy: ## Remove deployment from minikube
	@echo "Removing deployment from namespace $(K8S_NAMESPACE)..."
	@kubectl delete -f k8s/ingress.yaml --ignore-not-found=true
	@kubectl delete -f k8s/service.yaml --ignore-not-found=true
	@kubectl delete -f k8s/deployment.yaml --ignore-not-found=true
	@kubectl delete -f k8s/namespace.yaml --ignore-not-found=true
	@echo "Deployment removed."

# ---------------------------------------------------------------------------
# Status & Logs
# ---------------------------------------------------------------------------

status: ## Show deployment status
	@echo "ArgoCD Addons Platform - Deployment Status"
	@echo "==========================================="
	@echo "Version: $(VERSION)"
	@echo "Namespace: $(K8S_NAMESPACE)"
	@echo ""
	@echo "Pods:"
	@kubectl get pods -n $(K8S_NAMESPACE) 2>/dev/null || echo "  No pods found"
	@echo ""
	@echo "Deployments:"
	@kubectl get deployments -n $(K8S_NAMESPACE) 2>/dev/null || echo "  No deployments found"
	@echo ""
	@echo "Services:"
	@kubectl get services -n $(K8S_NAMESPACE) 2>/dev/null || echo "  No services found"
	@echo ""
	@echo "Ingress:"
	@kubectl get ingress -n $(K8S_NAMESPACE) 2>/dev/null || echo "  No ingress found"

logs: ## Show pod logs
	@kubectl logs -f deployment/aap-server -n $(K8S_NAMESPACE)

# ---------------------------------------------------------------------------
# Secrets Management
# ---------------------------------------------------------------------------

create-secrets: ensure-namespace ## Create K8s secrets from .env.secrets
	@if [ ! -f .env.secrets ]; then \
		echo "Error: .env.secrets file not found."; \
		echo "Create it with the following variables:"; \
		echo "  AZURE_DEVOPS_PAT"; \
		echo "  ARGOCD_NONPROD_SERVER_URL, ARGOCD_NONPROD_TOKEN, ARGOCD_NONPROD_NAMESPACE"; \
		echo "  ARGOCD_PROD_SERVER_URL, ARGOCD_PROD_TOKEN, ARGOCD_PROD_NAMESPACE"; \
		exit 1; \
	fi
	@echo "Creating secrets from .env.secrets..."
	@set -a && source .env.secrets && set +a && \
	kubectl create secret generic aap-azure-devops \
		--from-literal=pat="$$AZURE_DEVOPS_PAT" \
		-n $(K8S_NAMESPACE) \
		--dry-run=client -o yaml | kubectl apply -f -
	@set -a && source .env.secrets && set +a && \
	kubectl create secret generic aap-argocd-nonprod \
		--from-literal=server-url="$$ARGOCD_NONPROD_SERVER_URL" \
		--from-literal=token="$$ARGOCD_NONPROD_TOKEN" \
		--from-literal=namespace="$$ARGOCD_NONPROD_NAMESPACE" \
		-n $(K8S_NAMESPACE) \
		--dry-run=client -o yaml | kubectl apply -f -
	@set -a && source .env.secrets && set +a && \
	kubectl create secret generic aap-argocd-prod \
		--from-literal=server-url="$$ARGOCD_PROD_SERVER_URL" \
		--from-literal=token="$$ARGOCD_PROD_TOKEN" \
		--from-literal=namespace="$$ARGOCD_PROD_NAMESPACE" \
		-n $(K8S_NAMESPACE) \
		--dry-run=client -o yaml | kubectl apply -f -
	@echo "Secrets created successfully."

list-secrets: ## List secrets in namespace
	@echo "Secrets in $(K8S_NAMESPACE):"
	@kubectl get secrets -n $(K8S_NAMESPACE) 2>/dev/null || echo "  No secrets found"

# ---------------------------------------------------------------------------
# Code Quality
# ---------------------------------------------------------------------------

test-coverage: ## Run tests with coverage
	@echo "Running Go tests with coverage..."
	go test ./internal/... -coverprofile=coverage.out -covermode=atomic
	go tool cover -func=coverage.out | tail -1
	@echo ""
	@echo "Running UI tests with coverage..."
	@cd ui && npx vitest run --coverage 2>/dev/null || echo "Install @vitest/coverage-v8 for UI coverage"

pre-commit-install: ## Install pre-commit hooks
	@if command -v pre-commit >/dev/null 2>&1; then \
		pre-commit install; \
		pre-commit install --hook-type pre-push; \
		echo "Pre-commit hooks installed."; \
	else \
		echo "pre-commit not installed. Install: pip install pre-commit"; \
	fi
