# Sharko — Makefile

.PHONY: help demo dev build test test-go test-ui lint ui-build ui-install clean build-go release e2e test-e2e test-e2e-fast test-e2e-domain test-e2e-coverage test-e2e-fast-coverage test-e2e-junit test-e2e-report install-test-tools kind-up kind-down catalog-scan catalog-scan-pr

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
	@echo "    make test-e2e-coverage     Full e2e + coverage HTML in _dist/"
	@echo "    make test-e2e-fast-coverage  Fast e2e + coverage HTML in _dist/"
	@echo "    make test-e2e-junit        Full e2e + JUnit XML in _dist/"
	@echo "    make test-e2e-report       Full e2e + coverage HTML + JUnit XML"
	@echo "    make install-test-tools    Install test tooling (gotestsum)"
	@echo "    make kind-up               Provision a sharko-e2e kind topology"
	@echo "    make kind-down             Destroy stale sharko-e2e-* kind clusters"
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
	CGO_ENABLED=0 go build -o bin/sharko ./cmd/sharko
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

# Test reports (V2 Epic 7-1.16). The KEY flag is
# -coverpkg=./internal/...,./cmd/... — without it, the coverage profile
# only measures tests/e2e/* itself (useless). With it, the report shows
# which lines of sharko's actual code got executed by the e2e suite.

install-test-tools: ## Install test tooling (gotestsum)
	go install gotest.tools/gotestsum@latest

test-e2e-coverage: ## Run E2E suite with coverage of internal/* and produce _dist/e2e-coverage.html
	@mkdir -p _dist
	GOTMPDIR=/tmp go test -tags=e2e -timeout=30m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@echo "==> Coverage HTML: _dist/e2e-coverage.html"
	@go tool cover -func=_dist/e2e-coverage.out | tail -1

test-e2e-fast-coverage: ## Fast in-process E2E with coverage of internal/* (~30s)
	@mkdir -p _dist
	GOTMPDIR=/tmp go test -tags=e2e -timeout=2m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		-run '^(TestHarnessGitFakeStandalone|TestHarnessSharkoInProcess|TestFoundationStack|TestAuthFlow|TestAuthUpdatePassword|TestRBACEnforcement|TestTokensCRUD|TestCatalogReads|TestMarketplaceAddFlow|TestAddonAdmin|TestAddonSecretsLifecycle|TestAIConfig|TestAIInvocation|TestGlobalValuesEditor|TestPerClusterValuesOverride|TestPRTracking|TestNotificationsLifecycle|TestConnectionsCRUDAndInit|TestDashboardAndReadsInProcess)$$' \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@echo "==> Coverage HTML: _dist/e2e-coverage.html"
	@go tool cover -func=_dist/e2e-coverage.out | tail -1

test-e2e-junit: ## Run E2E suite with gotestsum + produce _dist/e2e-junit.xml
	@mkdir -p _dist
	@command -v gotestsum >/dev/null || { echo "ERROR: gotestsum not installed. Run: make install-test-tools"; exit 1; }
	GOTMPDIR=/tmp gotestsum \
		--junitfile=_dist/e2e-junit.xml \
		--format=testname \
		-- -tags=e2e -timeout=30m ./tests/e2e/...
	@echo "==> JUnit XML: _dist/e2e-junit.xml"

test-e2e-report: ## Run E2E suite producing BOTH coverage HTML + JUnit XML in _dist/
	@mkdir -p _dist
	@command -v gotestsum >/dev/null || { echo "ERROR: gotestsum not installed. Run: make install-test-tools"; exit 1; }
	GOTMPDIR=/tmp gotestsum \
		--junitfile=_dist/e2e-junit.xml \
		--format=testname \
		-- -tags=e2e -timeout=30m \
		-coverprofile=_dist/e2e-coverage.out \
		-coverpkg=./internal/...,./cmd/... \
		./tests/e2e/...
	@go tool cover -html=_dist/e2e-coverage.out -o _dist/e2e-coverage.html
	@go tool cover -func=_dist/e2e-coverage.out | tail -1
	@echo "==> JUnit XML:        _dist/e2e-junit.xml"
	@echo "==> Coverage HTML:    _dist/e2e-coverage.html"
	@echo "==> Open coverage in browser: open _dist/e2e-coverage.html  (macOS)"

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
