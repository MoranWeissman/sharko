# Sharko — Makefile

.PHONY: help demo dev build test test-go test-ui lint ui-build ui-install clean build-go release e2e test-e2e test-e2e-fast test-e2e-domain test-e2e-helm test-e2e-perf test-e2e-perf-capture test-e2e-perf-compare test-e2e-clean test-e2e-coverage test-e2e-fast-coverage test-e2e-junit test-e2e-report install-test-tools kind-up kind-down catalog-scan catalog-scan-pr generate-provider-types generate-schemas build-gitfake-image operator-dev-up operator-dev-down install uninstall deploy undeploy manifests

PORT ?= 8080

help: ## Show available targets
	@echo ""
	@echo "  🦈 Sharko"
	@echo ""
	@echo "  Quick Start:"
	@echo "    make demo             Build UI + start with mock backends (http://localhost:$(PORT))"
	@echo "    make dev              Hot-reload dev mode (http://localhost:5173)"
	@echo ""
	@echo "  Build & Test:"
	@echo "    make build            Build Go binary + UI"
	@echo "    make test             Run all tests (Go + UI)"
	@echo "    make lint             Go vet + UI build check"
	@echo ""
	@echo "  E2E (V2 Epic 7-1):"
	@echo "    make test-e2e-fast         In-process e2e suite (~30s, no kind/docker)"
	@echo "    make test-e2e              Full e2e suite (kind + real argocd, ~10-15 min)"
	@echo "    make test-e2e-domain       Run a single domain (DOMAIN=Cluster|Catalog|...)"
	@echo "    make test-e2e-helm         Wave-D Helm-mode subset (~5-8 min, requires docker+kind+helm)"
	@echo "    make test-e2e-perf         V2-1 perf baselines (~2-5 min in-process; cluster path needs kind)"
	@echo "    make test-e2e-perf-capture Run perf harness + capture timings to _dist/perf-timings.jsonl (CI)"
	@echo "    make test-e2e-perf-compare Compare captured timings against baselines YAML — exits 2 on >20% p99 regression"
	@echo "    make test-e2e-clean        Force-delete every sharko-e2e-* kind cluster (manual recovery)"
	@echo "    make test-e2e-coverage     Full e2e + coverage HTML in _dist/"
	@echo "    make test-e2e-fast-coverage  Fast e2e + coverage HTML in _dist/"
	@echo "    make test-e2e-junit        Full e2e + JUnit XML in _dist/"
	@echo "    make test-e2e-report       Full e2e + coverage HTML + JUnit XML"
	@echo "    make install-test-tools    Install test tooling (gotestsum)"
	@echo "    make kind-up               Provision a sharko-e2e kind topology"
	@echo "    make kind-down             Destroy stale sharko-e2e-* kind clusters"
	@echo ""
	@echo "  Operator Development (Phase 0 scaffold):"
	@echo "    make operator-dev-up       Provision persistent dev cluster + ArgoCD + Sharko"
	@echo "    make operator-dev-down     Delete ONLY sharko-operator-dev cluster (safe)"
	@echo "    make manifests             Generate CRD YAML + RBAC from Go markers (installs controller-gen)"
	@echo "    make install               Apply CRDs to cluster (no-op until Phase 1)"
	@echo "    make uninstall             Delete CRDs from cluster"
	@echo "    make deploy                Apply controller RBAC + Deployment (no-op until Phase 1)"
	@echo "    make undeploy              Delete controller RBAC + Deployment"
	@echo ""

demo: ## Build UI + start server in demo mode
	@# Kill any existing sharko demo server
	@-pkill -f "sharko serve --demo" 2>/dev/null || true
	@sleep 0.5
	@echo ""
	@echo "  🦈 Sharko Demo Mode"
	@echo "  Building UI..."
	@rm -rf ui/dist
	@cd ui && npm run build 2>&1 | grep -v "PLUGIN_TIMINGS\|chunks are larger\|dynamic import\|codeSplitting\|chunkSizeWarningLimit\|rolldown.rs"
	@echo "  Open http://localhost:$(PORT)"
	@echo "  Login: admin/admin (admin) or qa/sharko (viewer)"
	@echo ""
	go run ./cmd/sharko serve --demo --port $(PORT) --static ui/dist

dev: ## Start backend (demo) + frontend with hot reload
	@-pkill -f "sharko serve --demo" 2>/dev/null || true
	@sleep 0.5
	@echo ""
	@echo "  🦈 Sharko Dev Mode (hot reload)"
	@echo "  Backend: http://localhost:$(PORT) (demo mode)"
	@echo "  Frontend: http://localhost:5173 ← open this"
	@echo "  Login: admin/admin or qa/sharko"
	@echo ""
	@trap 'kill 0' EXIT; \
		go run ./cmd/sharko serve --demo --port $(PORT) & \
		cd ui && npm run dev & \
		wait

build: ui-build build-go ## Build Go binary + UI

build-go: ## Build Go binary
	@mkdir -p bin
	CGO_ENABLED=0 go build \
		-ldflags "-X main.version=$$(cat version.txt) -X main.commit=$$(git rev-parse --short HEAD 2>/dev/null || echo dev)" \
		-o bin/sharko ./cmd/sharko
	@echo "Built: bin/sharko"

ui-build: ## Build the React UI
	cd ui && npm run build

ui-install: ## Install UI dependencies
	cd ui && npm install

test: test-go test-ui ## Run all tests

test-go: ## Run Go tests
	go clean -testcache
	go test ./...

test-ui: ## Run UI tests
	cd ui && npm test -- --run

lint: ## Go vet + UI build check
	go vet ./...
	cd ui && npm run build

# V125-1-13.7 — code generator: parses internal/providers/provider.go's
# New() switch via go/ast and emits ui/src/generated/provider-types.ts as
# a frozen `as const` literal. The Settings dropdown imports
# VALID_PROVIDER_TYPES from that file so it cannot drift from the
# backend factory. CI's "Provider Types Up To Date" check runs this
# target then `git diff --exit-code` on the output to catch stale files.
#
# Coordination note: V125-1-13.8 adds a `test-e2e-helm` target — a
# different concern (e2e Helm install harness). Keep these two targets
# textually adjacent in the file but logically independent.
generate-provider-types: ## Regenerate ui/src/generated/provider-types.ts from internal/providers/provider.go
	go run ./cmd/gen-provider-types

# V125-1-9.3 + 9.4 — schema generator. Reflects the envelope Go types in
# internal/models (ManagedClustersSpec) and internal/config
# (AddonCatalogSpec) via cmd/schema-gen and writes to TWO mirrored
# locations:
#   docs/schemas/managed-clusters.v1.json      (human-facing)
#   docs/schemas/addon-catalog.v1.json         (human-facing)
#   internal/schema/managed-clusters.v1.json   (V125-1-9.4 embed source)
#   internal/schema/addon-catalog.v1.json      (V125-1-9.4 embed source)
#
# The internal/schema/ copies feed the runtime validator's go:embed in
# internal/schema/embed.go (Story 9.4); the docs/schemas/ copies are
# what the public schema URLs + editor headers point at. All four files
# are committed to git. CI's "Schemas Up To Date" check runs this target
# then `git diff --exit-code` against both paths to catch stale files —
# same shape as the swagger and provider-types drift gates.
#
# Idempotent by design: invopop/jsonschema preserves struct declaration
# order, encoding/json sorts map keys, so back-to-back runs produce
# byte-identical output at every location.
generate-schemas: ## Regenerate docs/schemas/*.v1.json + internal/schema/*.v1.json from the Sharko envelope Go types
	go run ./cmd/schema-gen

clean: ## Remove build artifacts
	rm -rf bin/ ui/dist/ _dist/

catalog-scan: ## Run the catalog-scan bot in --dry-run mode (V123-3.1 skeleton)
	@npm install --prefix scripts --silent
	@node scripts/catalog-scan.mjs --dry-run

catalog-scan-pr: ## Preview the catalog-scan PR body (V123-3.4) — runs scanner then pr-open --dry-run. Requires _dist/catalog-scan/changeset.json on disk; produce it with `GITHUB_TOKEN=$$(gh auth token) node scripts/catalog-scan.mjs --catalog catalog/addons.yaml`.
	@npm install --prefix scripts --silent
	@node scripts/catalog-scan/pr-open.mjs --dry-run

# E2E test suite (V2 Epic 7-1).
#
# The Go-native harness under tests/e2e/ replaced the legacy
# setup.sh/teardown.sh + port-forward shell flow in story 7-1.15. Two
# entry points: test-e2e-fast (in-process, ~30s, no kind required) and
# test-e2e (full suite, ~10-15 min, requires docker + kind). The
# in-process boot path uses httptest + an in-memory git server so the
# fast lane needs no external services. See
# docs/site/developer-guide/e2e-testing.md for the full reference.
#
# All targets set GOTMPDIR=/tmp because go-test writes large temp dirs
# under /var/folders on macOS by default and that path can run out of
# space during a full run.

test-e2e: ## Run the full E2E suite (kind + real argocd; ~10-15 min). Requires docker.
	@echo "==> Running comprehensive E2E suite (kind + real argocd)..."
	GOTMPDIR=/tmp go test -tags=e2e -timeout=30m -v ./tests/e2e/...

# test-e2e-fast: only top-level test functions that boot in-process
# (httptest + GitFake + GitMock). The five kind-required tests are
# excluded explicitly so this lane stays under ~2 min on a laptop
# without docker:
#   - TestHarnessKindMultiCluster   (kind harness smoke)
#   - TestPerClusterAddonLifecycle  (full cluster register + addon)
#   - TestClusterLifecycle          (cluster CRUD against argocd)
#   - TestConnectionsDiscoverAndTest (live kubeconfig probe)
#   - TestFleetStatusWithArgocd     (dashboard fleet status)
test-e2e-fast: ## Run only the in-process E2E tests (~30s, no kind needed).
	@echo "==> Running fast in-process E2E tests..."
	GOTMPDIR=/tmp go test -tags=e2e -timeout=2m -v -run '^(TestHarnessGitFakeStandalone|TestHarnessSharkoInProcess|TestFoundationStack|TestAuthFlow|TestAuthUpdatePassword|TestRBACEnforcement|TestTokensCRUD|TestCatalogReads|TestMarketplaceAddFlow|TestAddonAdmin|TestAddonSecretsLifecycle|TestAIConfig|TestAIInvocation|TestGlobalValuesEditor|TestPerClusterValuesOverride|TestPRTracking|TestNotificationsLifecycle|TestConnectionsCRUDAndInit|TestDashboardAndReadsInProcess)$$' ./tests/e2e/...

test-e2e-domain: ## Run a single domain (e.g. make test-e2e-domain DOMAIN=Cluster).
	@if [ -z "$(DOMAIN)" ]; then \
		echo "ERROR: usage: make test-e2e-domain DOMAIN=<Cluster|Catalog|Auth|RBAC|Tokens|Addon|AI|Values|PR|Notifications|Dashboard|Connections|Foundation|Harness>"; \
		exit 1; \
	fi
	GOTMPDIR=/tmp go test -tags=e2e -timeout=30m -v -run "$(DOMAIN)" ./tests/e2e/...

# V125-1-13.8 — Helm-mode E2E subset. Runs Wave D's three lifecycle
# tests through the real Helm-installed Sharko boot path
# (E2E_SHARKO_MODE=helm) instead of the in-process httptest server.
# Requires docker + kind + helm + kubectl on PATH; CI provisions all
# four. The harness (V125-1-13.1) honours SHARKO_E2E_IMAGE_TAG to skip
# the docker-build + kind-load roundtrip on cache hit, so re-runs of
# the same git SHA reuse a previously-built image. The default tag
# pins to the current commit's short SHA so back-to-back local runs
# share a build but a fresh commit forces a rebuild.
#
# Test selection covers Wave D's three top-level functions:
#   - TestClusterTest_ArgoCDProvider                       (V125-1-13.4)
#   - TestClusterTest_ProviderAutoDefault_HappyPath        (V125-1-13.5)
#   - TestClusterTest_ProviderCrossContamination_NamespaceSwitch (V125-1-13.6)
# These are the only suites that exercise the real Helm install path
# end-to-end; the rest of the e2e tree stays on the in-process boot.
test-e2e-helm: ## Run the Wave-D Helm-mode E2E subset (~5-8 min, requires docker + kind + helm + kubectl).
	@echo "==> Running Helm-mode E2E tests against kind + ArgoCD + Helm-installed Sharko"
	@SHARKO_E2E_IMAGE_TAG=$${SHARKO_E2E_IMAGE_TAG:-e2e-$$(git rev-parse --short HEAD)} \
	 E2E_SHARKO_MODE=helm \
	 GOTMPDIR=/tmp \
	 go test -tags=e2e -timeout=20m -v \
	 -run '^(TestClusterTest_ArgoCDProvider|TestClusterTest_ProviderAutoDefault_HappyPath|TestClusterTest_ProviderCrossContamination_NamespaceSwitch)$$' \
	 ./tests/e2e/lifecycle/...

# V2-1.1 + V2-1.2 — perf baseline harness.
#
# Build-tag combo `e2e perf` opts in to tests/e2e/lifecycle/perf_test.go,
# which loops each of the 4 locked critical paths (see
# tests/e2e/harness/phases.go) perfIterations=30 times and emits
# structured JSON timing lines per (path, phase, iteration). Each subtest
# logs a rolled-up p50/p95/p99 table to the test output; the canonical
# numbers live in docs/site/operator/perf-baselines.md (refreshed
# manually by re-running this target and pasting the table updates).
#
# The cluster_registration subtest is kind-backed and skip-graceful when
# kind / docker / kubectl are absent; the other three subtests run
# fully in-process and complete in <2 minutes on a developer laptop.
test-e2e-perf: ## V2-1 perf baselines (~2-5 min in-process; cluster path needs kind).
	@echo "==> Running V2-1 perf baseline harness (30+ iterations per path)"
	GOTMPDIR=/tmp go test -tags='e2e perf' -timeout=20m -v \
	 -run '^TestPerf$$' \
	 ./tests/e2e/lifecycle/...

# V2-1.4 — perf-regression CI gate plumbing.
#
# Two targets:
#
#   test-e2e-perf-capture — runs the perf harness AND tees the test log to
#     _dist/perf-timings.jsonl. The harness's PhaseTimer emissions land on
#     stderr alongside slog noise; the comparator's loader is robust to
#     that mix (lines that don't start with `{` are dropped). Used by
#     .github/workflows/perf-regression.yml.
#
#   test-e2e-perf-compare — invokes cmd/perf-baseline-compare against the
#     captured timings + the canonical baselines YAML, returning non-zero
#     when any p99 regresses >20%. The workflow's `make` invocation thus
#     fails the job naturally, no awk needed on the workflow side.
#
# These targets are intentionally additive — `make test-e2e-perf` retains
# its developer-laptop shape (no capture file, no comparator). Capture +
# compare only matters when the gate is the consumer.

test-e2e-perf-capture: ## Run the perf harness and capture timings to _dist/perf-timings.jsonl (V2-1.4 CI input).
	@mkdir -p _dist
	@echo "==> Running V2-1 perf harness and capturing timings to _dist/perf-timings.jsonl"
	@# The harness writes PhaseTimer emissions to stderr (default sink).
	@# We tee combined output to the capture file; cmd/perf-baseline-compare
	@# ignores non-JSON lines, so slog noise from the test process is fine.
	@GOTMPDIR=/tmp go test -tags='e2e perf' -timeout=30m -v \
	 -run '^TestPerf$$' \
	 ./tests/e2e/lifecycle/... 2>&1 | tee _dist/perf-timings.jsonl
	@echo "==> Captured: _dist/perf-timings.jsonl"

test-e2e-perf-compare: ## Compare _dist/perf-timings.jsonl against docs/site/operator/perf-baselines.yaml (V2-1.4 gate).
	@go run ./cmd/perf-baseline-compare \
	 -timings _dist/perf-timings.jsonl \
	 -baselines docs/site/operator/perf-baselines.yaml

# V126-4.1 / task #188 — Manual recovery for leaked e2e kind clusters.
#
# Intent: a developer who hits Ctrl+C mid-run, or comes back to a host
# with a corrupted kind state file, can `make test-e2e-clean` once and
# get back to zero. Safe to run with no leaks present — kind get
# clusters returns nothing, the xargs no-ops, docker prune is a no-op,
# and the target exits 0. Companion to the in-test
# DestroyAllStaleE2EClusters helper (which only runs from inside a
# go test process). The sharko-e2e- name prefix is the load-bearing
# safety filter — only harness-provisioned clusters match; the
# maintainer's hand-managed kind clusters (sharko-dev, etc.) are never
# touched.
test-e2e-clean: ## Force-delete every sharko-e2e-* kind cluster (manual recovery).
	@kind get clusters 2>/dev/null | grep -E '^sharko-e2e-' | xargs -I{} kind delete cluster --name {} || true
	@docker container prune -f --filter "label=io.x-k8s.kind.cluster" >/dev/null
	@echo "e2e cleanup complete"

# V125-1-13.x.1 — Build the gitfake-server image.
#
# Mirrors the Sharko image-cache probe pattern used by test-e2e-helm:
# `docker image inspect` is the cheapest cache hit (exits 0 / non-zero).
# On a cache hit we skip the (~30s) docker build entirely; back-to-back
# local runs of downstream stories (13.x.2+) that re-deploy the same SHA
# pay zero rebuild cost.
#
# SHARKO_GITFAKE_IMAGE_TAG defaults to e2e-<short-sha> so a fresh commit
# forces a rebuild and a re-run on the same commit reuses the cached
# image. Build context is the repo root because the Dockerfile copies
# go.mod + the harness package from there.
SHARKO_GITFAKE_IMAGE_TAG ?= e2e-$(shell git rev-parse --short HEAD)
SHARKO_GITFAKE_IMAGE ?= sharko-gitfake:$(SHARKO_GITFAKE_IMAGE_TAG)

build-gitfake-image: ## Build the gitfake-server image (skips on docker-image cache hit).
	@if docker image inspect $(SHARKO_GITFAKE_IMAGE) >/dev/null 2>&1; then \
		echo "==> gitfake image $(SHARKO_GITFAKE_IMAGE) already present locally — skipping build"; \
	else \
		echo "==> Building $(SHARKO_GITFAKE_IMAGE) from tests/e2e/harness/gitfake/Dockerfile"; \
		docker build -f tests/e2e/harness/gitfake/Dockerfile -t $(SHARKO_GITFAKE_IMAGE) .; \
	fi

# Test reports (V2 Epic 7-1.16). The KEY flag is
# -coverpkg=./internal/...,./cmd/... — without it, the coverage profile
# only measures tests/e2e/* itself (useless). With it, the report shows
# which lines of sharko's actual code got executed by the e2e suite.
#
# gotestsum resolution (V2 Epic 7-1.17): `go install` writes binaries to
# $(go env GOPATH)/bin which most users do NOT have on their PATH.
# Resolve gotestsum in this order:
#   1. PATH (if user added GOPATH/bin themselves)
#   2. $(go env GOPATH)/bin/gotestsum (the standard install location)
# This way `make install-test-tools && make test-e2e-report` Just Works
# without forcing the user to fix their shell rc.
GOTESTSUM := $(shell command -v gotestsum 2>/dev/null || echo "$(shell go env GOPATH)/bin/gotestsum")

install-test-tools: ## Install test tooling (gotestsum)
	go install gotest.tools/gotestsum@latest
	@echo "==> Installed gotestsum to $(shell go env GOPATH)/bin/gotestsum"
	@echo "==> Makefile auto-resolves it from there — no PATH edit needed."

test-e2e-coverage: ## Run E2E suite with coverage of internal/* and produce _dist/e2e-coverage.html
	@mkdir -p _dist
	GOTMPDIR=/tmp go test -tags=e2e -timeout=30m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@echo "==> Coverage HTML:    file://$$(pwd)/_dist/e2e-coverage.html"
	@go tool cover -func=_dist/e2e-coverage.out | tail -1

test-e2e-fast-coverage: ## Fast in-process E2E with coverage of internal/* (~30s)
	@mkdir -p _dist
	GOTMPDIR=/tmp go test -tags=e2e -timeout=2m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		-run '^(TestHarnessGitFakeStandalone|TestHarnessSharkoInProcess|TestFoundationStack|TestAuthFlow|TestAuthUpdatePassword|TestRBACEnforcement|TestTokensCRUD|TestCatalogReads|TestMarketplaceAddFlow|TestAddonAdmin|TestAddonSecretsLifecycle|TestAIConfig|TestAIInvocation|TestGlobalValuesEditor|TestPerClusterValuesOverride|TestPRTracking|TestNotificationsLifecycle|TestConnectionsCRUDAndInit|TestDashboardAndReadsInProcess)$$' \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@echo "==> Coverage HTML:    file://$$(pwd)/_dist/e2e-coverage.html"
	@go tool cover -func=_dist/e2e-coverage.out | tail -1

test-e2e-junit: ## Run E2E suite with gotestsum + produce _dist/e2e-junit.xml
	@mkdir -p _dist
	@test -x "$(GOTESTSUM)" || { echo "ERROR: gotestsum not found at $(GOTESTSUM). Run: make install-test-tools"; exit 1; }
	GOTMPDIR=/tmp $(GOTESTSUM) \
		--junitfile=_dist/e2e-junit.xml \
		--format=testname \
		-- -tags=e2e -timeout=30m ./tests/e2e/...
	@echo "==> JUnit XML:        file://$$(pwd)/_dist/e2e-junit.xml"

test-e2e-report: ## Run E2E suite producing BOTH coverage HTML + JUnit XML in _dist/
	@mkdir -p _dist
	@test -x "$(GOTESTSUM)" || { echo "ERROR: gotestsum not found at $(GOTESTSUM). Run: make install-test-tools"; exit 1; }
	GOTMPDIR=/tmp $(GOTESTSUM) \
		--junitfile=_dist/e2e-junit.xml \
		--format=testname \
		-- -tags=e2e -timeout=30m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@go tool cover -func=_dist/e2e-coverage.out | tail -1
	@echo "==> JUnit XML:        file://$$(pwd)/_dist/e2e-junit.xml"
	@echo "==> Coverage HTML:    file://$$(pwd)/_dist/e2e-coverage.html"

kind-up: ## Provision a sharko-e2e kind topology (1 mgmt + 1 target).
	@echo "==> Provisioning sharko-e2e kind topology..."
	GOTMPDIR=/tmp go test -tags=e2e -timeout=10m -v -run TestHarnessKindMultiCluster ./tests/e2e/harness/...

kind-down: ## Destroy all sharko-e2e-* kind clusters (sentinel-labeled only).
	@echo "==> Destroying stale sharko-e2e kind clusters..."
	@kind get clusters 2>/dev/null | grep "^sharko-e2e-" | xargs -I{} kind delete cluster --name {} || echo "(none)"

# Legacy alias — `make e2e` previously bash-scripted setup.sh +
# port-forward + teardown.sh. The Go harness replaces all of that;
# keep the alias so existing muscle memory still works.
e2e: test-e2e ## Alias for `make test-e2e` (legacy name).

release: ## Tag and push a release (usage: make release VERSION=1.0.0)
	@if [ -z "$(VERSION)" ]; then echo "Usage: make release VERSION=1.0.0"; exit 1; fi
	@echo ""
	@echo "  🦈 Sharko Release v$(VERSION)"
	@echo ""
	@echo "$(VERSION)" > version.txt
	@sed -i '' 's/^version:.*/version: $(VERSION)/' charts/sharko/Chart.yaml
	@sed -i '' 's/^appVersion:.*/appVersion: "$(VERSION)"/' charts/sharko/Chart.yaml
	@if git diff --quiet version.txt charts/sharko/Chart.yaml; then \
		echo "  Version already set to $(VERSION) — tagging current main"; \
		git tag -a "v$(VERSION)" -m "Release v$(VERSION)"; \
	else \
		git checkout -b release/v$(VERSION); \
		git add -f version.txt charts/sharko/Chart.yaml; \
		git commit -m "release: v$(VERSION)"; \
		git push -u origin release/v$(VERSION); \
		gh pr create --title "release: v$(VERSION)" --body "Version bump to $(VERSION)"; \
		gh pr merge --squash; \
		git checkout main; \
		git pull origin main; \
		git tag -a "v$(VERSION)" -m "Release v$(VERSION)"; \
	fi
	@echo ""
	@echo "  ✅ Tagged v$(VERSION). Push the tag:"
	@echo "    git push origin v$(VERSION)"
	@echo ""

# =====================================================================
# Operator Development Tooling (Phase 0)
# =====================================================================
#
# Phase 0 scaffolds the local operator development loop. Targets below are
# idempotent no-ops today (CRDs + controller code land in Phase 1+). They
# install controller-gen if missing and print honest "no CRDs yet" messages.
#
# The persistent dev cluster (sharko-operator-dev) is DISTINCT from the
# throwaway e2e clusters (sharko-e2e-*) so test-e2e-clean / kind-down never
# delete it. operator-dev-down guards by exact cluster name (same safety
# discipline as test-e2e-clean).

OPERATOR_DEV_CLUSTER_NAME := sharko-operator-dev

operator-dev-up: ## Provision persistent operator dev cluster + install Sharko
	@echo "==> Provisioning persistent operator dev cluster '$(OPERATOR_DEV_CLUSTER_NAME)'"
	@if kind get clusters 2>/dev/null | grep -qx "$(OPERATOR_DEV_CLUSTER_NAME)"; then \
		echo "    Cluster already exists — upgrading Sharko"; \
	else \
		echo "    Creating kind cluster"; \
		kind create cluster --name $(OPERATOR_DEV_CLUSTER_NAME) --wait 60s; \
	fi
	@echo "==> Installing ArgoCD"
	@kubectl --context kind-$(OPERATOR_DEV_CLUSTER_NAME) create namespace argocd 2>/dev/null || true
	@kubectl --context kind-$(OPERATOR_DEV_CLUSTER_NAME) apply --server-side --force-conflicts -n argocd \
		-f https://raw.githubusercontent.com/argoproj/argo-cd/stable/manifests/install.yaml >/dev/null
	@kubectl --context kind-$(OPERATOR_DEV_CLUSTER_NAME) wait --for=condition=available --timeout=180s \
		deployment/argocd-server -n argocd
	@echo "==> Installing Sharko via scripts/helm-install.sh"
	@KIND_CLUSTER_NAME=$(OPERATOR_DEV_CLUSTER_NAME) bash scripts/helm-install.sh || \
		echo "    helm-install.sh failed — check secrets.env is present with GITHUB_TOKEN"
	@echo ""
	@echo "  ✅ Operator dev cluster ready"
	@echo "     Cluster: kind-$(OPERATOR_DEV_CLUSTER_NAME)"
	@echo "     To access Sharko: kubectl --context kind-$(OPERATOR_DEV_CLUSTER_NAME) port-forward -n sharko svc/sharko 8080:80"
	@echo "     To access ArgoCD: kubectl --context kind-$(OPERATOR_DEV_CLUSTER_NAME) port-forward -n argocd svc/argocd-server 18080:443"

operator-dev-down: ## Delete ONLY the sharko-operator-dev cluster (safe — never touches other clusters)
	@echo "==> Deleting persistent operator dev cluster '$(OPERATOR_DEV_CLUSTER_NAME)'"
	@if kind get clusters 2>/dev/null | grep -qx "$(OPERATOR_DEV_CLUSTER_NAME)"; then \
		kind delete cluster --name $(OPERATOR_DEV_CLUSTER_NAME); \
		echo "    Cluster deleted"; \
	else \
		echo "    Cluster '$(OPERATOR_DEV_CLUSTER_NAME)' not found (already torn down)"; \
	fi

# Kubebuilder-standard targets. Phase 0: all are clean no-ops (exit 0 with a note).
# Phase 1+: these will generate/apply CRDs + RBAC + controller Deployment.

CONTROLLER_GEN := $(shell command -v controller-gen 2>/dev/null || echo "$(shell go env GOPATH)/bin/controller-gen")

install: ## Apply CRDs to the cluster (no-op in Phase 0 — CRDs populated in Phase 1)
	@if [ ! -d config/crd ]; then \
		echo "==> install: config/crd/ does not exist"; \
		exit 1; \
	fi; \
	CRD_COUNT=$$(find config/crd -name '*.yaml' -o -name '*.yml' 2>/dev/null | wc -l | tr -d ' '); \
	if [ "$$CRD_COUNT" -eq 0 ]; then \
		echo "==> install: no CRDs present yet (populated in Phase 1)"; \
	else \
		echo "==> Applying CRDs from config/crd/"; \
		kubectl apply -f config/crd/; \
	fi

uninstall: ## Delete CRDs from the cluster (no-op in Phase 0)
	@if [ ! -d config/crd ]; then \
		echo "==> uninstall: config/crd/ does not exist"; \
		exit 1; \
	fi; \
	CRD_COUNT=$$(find config/crd -name '*.yaml' -o -name '*.yml' 2>/dev/null | wc -l | tr -d ' '); \
	if [ "$$CRD_COUNT" -eq 0 ]; then \
		echo "==> uninstall: no CRDs to delete (populated in Phase 1)"; \
	else \
		echo "==> Deleting CRDs from config/crd/"; \
		kubectl delete -f config/crd/; \
	fi

deploy: ## Apply controller RBAC + Deployment (no-op in Phase 0 — manifests populated in Phase 1)
	@RBAC_COUNT=$$(find config/rbac -name '*.yaml' -o -name '*.yml' 2>/dev/null | wc -l); \
	if [ "$$RBAC_COUNT" -eq 0 ]; then \
		echo "==> deploy: no RBAC manifests yet (populated in Phase 1)"; \
	fi
	@MGR_COUNT=$$(find config/manager -name '*.yaml' -o -name '*.yml' 2>/dev/null | wc -l); \
	if [ "$$MGR_COUNT" -eq 0 ]; then \
		echo "==> deploy: no manager Deployment yet (populated in Phase 1)"; \
	fi
	@echo "==> deploy: Phase 0 no-op complete (controller not wired until Phase 1)"

undeploy: ## Delete controller RBAC + Deployment (no-op in Phase 0)
	@echo "==> undeploy: Phase 0 no-op (controller not wired until Phase 1)"

manifests: ## Generate CRD YAML + RBAC from Go markers
	@if [ ! -x "$(CONTROLLER_GEN)" ]; then \
		echo "==> Installing controller-gen (sigs.k8s.io/controller-tools)"; \
		go install sigs.k8s.io/controller-tools/cmd/controller-gen@latest; \
		echo "    Installed to $(shell go env GOPATH)/bin/controller-gen"; \
	fi
	@echo "==> manifests: controller-gen available at $(CONTROLLER_GEN)"
	@if [ ! -d api/v1alpha1 ]; then \
		echo "==> manifests: no API types present yet"; \
		exit 1; \
	fi
	@echo "==> Generating deepcopy code + CRD YAML from Go markers"
	@$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths="./api/..."
	@$(CONTROLLER_GEN) crd paths="./api/..." output:crd:artifacts:config=config/crd
	@echo "    Generated: api/v1alpha1/zz_generated.deepcopy.go"
	@echo "    Generated: config/crd/*.yaml"
	@echo "==> Copying CRDs to Helm chart"
	@mkdir -p charts/sharko/crds
	@cp config/crd/*.yaml charts/sharko/crds/ 2>/dev/null || true
	@echo "    Copied to: charts/sharko/crds/"
